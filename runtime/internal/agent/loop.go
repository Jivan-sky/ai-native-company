package agent

// 回合循环：一轮模型调用 → 若有工具调用就执行、把结果回灌 → 再调一次，直到没有工具调用。
//
// 三条不许：
//  1. **不许无限转**：步数上限必须由调用方给（默认 8），到顶就停并说清楚；
//  2. **不许把工具错误当成崩溃**：参数坏、工具报错，都变成一条 tool 消息回灌；
//  3. **不许在取消时假装完成**：ctx 一取消就返回，把已发生的消息交出去 —— 上层据此记「未完成」。

import (
	"context"
	"fmt"
	"io"
	"sort"

	"encoding/json"
)

// Tool 是一个可被模型调用的能力。Run 的返回值原样回灌给模型。
type Tool struct {
	Spec ToolSpec
	Run  func(ctx context.Context, args json.RawMessage) (string, error)
}

// Loop 是一次对话的运行时。
type Loop struct {
	Client   Client
	Tools    map[string]Tool
	MaxSteps int           // 0 = 8
	Out      io.Writer     // 文本增量写到哪（可为 nil）
	Record   func(Message) // 每条消息落地（可为 nil）
}

// Result 是一次 Run 的结果。
type Result struct {
	Messages []Message
	Usage    Usage
	Steps    int
	// Incomplete 表示「被取消 / 被截断」——**不是**正常结束。
	Incomplete bool
	Why        string
}

// New 造一个循环。
func New(c Client, tools []Tool) *Loop {
	m := map[string]Tool{}
	for _, t := range tools {
		m[t.Spec.Function.Name] = t
	}
	return &Loop{Client: c, Tools: m, MaxSteps: 8}
}

func (l *Loop) specs() []ToolSpec {
	names := make([]string, 0, len(l.Tools))
	for n := range l.Tools {
		names = append(names, n)
	}
	sort.Strings(names) // 顺序稳定：同一份会话两次跑，给模型的工具表一样
	out := make([]ToolSpec, 0, len(names))
	for _, n := range names {
		out = append(out, l.Tools[n].Spec)
	}
	return out
}

// Run 跑一次回合。msgs 是初始对话（含 system/user）。
func (l *Loop) Run(ctx context.Context, msgs []Message) (Result, error) {
	max := l.MaxSteps
	if max <= 0 {
		max = 8
	}
	res := Result{}
	cur := append([]Message(nil), msgs...)
	for step := 0; step < max; step++ {
		res.Steps = step + 1

		var text string
		calls := map[int]*ToolCall{}
		var usage Usage
		err := l.Client.Stream(ctx, cur, l.specs(), func(d Delta) error {
			switch {
			case d.Text != "":
				text += d.Text
				if l.Out != nil {
					_, _ = io.WriteString(l.Out, d.Text)
				}
			case d.Usage != nil:
				usage = usage.Add(*d.Usage)
			case d.ToolName != "" || d.ToolArgs != "" || d.ToolID != "":
				tc := calls[d.ToolIndex]
				if tc == nil {
					tc = &ToolCall{Type: "function"}
					calls[d.ToolIndex] = tc
				}
				if d.ToolID != "" {
					tc.ID = d.ToolID
				}
				if d.ToolName != "" {
					tc.Function.Name = d.ToolName
				}
				// arguments 是**分片**：一片一片接，不要覆盖。
				tc.Function.Arguments += d.ToolArgs
			}
			return nil
		})
		res.Usage = res.Usage.Add(usage)
		if err != nil {
			res.Incomplete = true
			res.Why = err.Error()
			res.Messages = cur
			return res, err
		}

		asst := Message{Role: "assistant", Content: text}
		for i := 0; i < len(calls); i++ {
			if tc := calls[i]; tc != nil {
				asst.ToolCalls = append(asst.ToolCalls, *tc)
			}
		}
		if len(calls) > 0 && len(asst.ToolCalls) != len(calls) {
			res.Incomplete = true
			res.Why = fmt.Sprintf("工具调用下标不连续（收到 %d 个，最大下标 %d）—— 流里漏了分片", len(asst.ToolCalls), len(calls)-1)
			res.Messages = append(cur, asst)
			return res, fmt.Errorf("%s", res.Why)
		}
		cur = append(cur, asst)
		if l.Record != nil {
			l.Record(asst)
		}

		if len(asst.ToolCalls) == 0 {
			res.Messages = cur
			return res, nil // 正常收口：没有工具调用就是这一回合结束
		}

		for _, tc := range asst.ToolCalls {
			out := l.execOne(ctx, tc)
			cur = append(cur, out)
			if l.Record != nil {
				l.Record(out)
			}
		}
	}

	res.Incomplete = true
	res.Why = fmt.Sprintf("超过步数上限（%d）—— 停在还没收口的地方", max)
	res.Messages = cur
	return res, fmt.Errorf("%s", res.Why)
}

// execOne 执行一个工具调用，并把「失败」也变成一条正常的 tool 消息。
func (l *Loop) execOne(ctx context.Context, tc ToolCall) Message {
	out := Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name}
	t, ok := l.Tools[tc.Function.Name]
	if !ok {
		out.Content = fmt.Sprintf("错误：没有这个工具 %q（可用：%s）", tc.Function.Name, l.toolNames())
		return out
	}
	args, err := MarshalArgs(tc.Function.Arguments)
	if err != nil {
		out.Content = "错误：" + err.Error() + "（把参数改成合法 JSON 再试）"
		return out
	}
	s, err := t.Run(ctx, args)
	if err != nil {
		out.Content = "错误：" + err.Error()
		return out
	}
	out.Content = s
	return out
}

func (l *Loop) toolNames() string {
	names := make([]string, 0, len(l.Tools))
	for n := range l.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return fmt.Sprint(names)
}

// Spec 造一个工具声明：name/description/JSON Schema。
func Spec(name, desc, schema string) ToolSpec {
	return ToolSpec{Type: "function", Function: ToolSchema{Name: name, Description: desc, Parameters: json.RawMessage(schema)}}
}

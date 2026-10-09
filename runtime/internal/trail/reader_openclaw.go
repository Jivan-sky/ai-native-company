package trail

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReaderOpenClaw 是 OpenClaw 的读取器名（写在接入口径表的 reader 字段里）。
const ReaderOpenClaw = "openclaw-jsonl"

// ocLine 是一行 OpenClaw 会话记录。这家把「会话事件」和「消息」写在同一个文件里：
// type = session / model_change / thinking_level_change / custom / message。
type ocLine struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"` // RFC3339 字符串
	Cwd       string `json:"cwd"`       // 只在 type=session 那一行上

	Message ocMessage `json:"message"`
}

// ocContent 是消息内容。**两种形状都真出现过**（实测 2026-10-09 本机记录）：
// 块数组 `[{"type":"text",…}]`，也有整段就是字符串的 `"content":"[OpenClaw heartbeat poll]"`。
// 只认前者的话，后者会整行解析不了 —— 那一轮的提问就没了，还会被当成「记录格式变了」。
type ocContent []ocBlock

func (c *ocContent) UnmarshalJSON(b []byte) error {
	var blocks []ocBlock
	if err := json.Unmarshal(b, &blocks); err == nil {
		*c = blocks
		return nil
	}
	var text string
	if err := json.Unmarshal(b, &text); err == nil {
		*c = []ocBlock{{Type: "text", Text: text}}
		return nil
	}
	return fmt.Errorf("content 既不是块数组也不是字符串")
}

// ocMessage 是一条消息。**用量挂在 assistant 消息上**（不是挂在会话上）。
type ocMessage struct {
	Role         string    `json:"role"` // user / assistant / toolResult
	Content      ocContent `json:"content"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	StopReason   string    `json:"stopReason"`   // stop / error / …
	ErrorMessage string    `json:"errorMessage"` // **模型这一轮自己失败了**时才有（实测："Connection error."）
	ResponseID   string    `json:"responseId"`
	Timestamp    int64     `json:"timestamp"` // 毫秒 epoch
	Usage        struct {
		Input      int `json:"input"`
		Output     int `json:"output"`
		CacheRead  int `json:"cacheRead"`
		CacheWrite int `json:"cacheWrite"`
		// ReasoningTokens 是 output 的一部分 —— 不单列（不然会和 claude 的账口径打架）。
		ReasoningTokens int `json:"reasoningTokens"`
		TotalTokens     int `json:"totalTokens"`
		Cost            struct {
			Total float64 `json:"total"`
		} `json:"cost"`
	} `json:"usage"`

	// toolResult 那两个字段。
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	IsError    bool   `json:"isError"`
}

// ocBlock 是消息里的一个内容块：text / thinking / toolCall。
type ocBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	ID   string `json:"id"`
	Name string `json:"name"`
	// Arguments 是**对象**（实测：`"arguments":{"who":"alice","kind":"report"}`）——
	// 和 codex / codebuddy 那种「arguments 是字符串」不是同一种形状，别混。
	Arguments json.RawMessage `json:"arguments"`
}

// usageOf 取这条 assistant 消息的账。
//
// **缓存读单列、不含在输入里**（实测：input 20629 + output 139 + cacheRead 14464 = totalTokens 35232）
// —— 正好和 codex 相反（那家 cacheRead 是 input 的一部分）。所以两家各写一条，不许混用。
func (m ocMessage) usageOf() Usage {
	return Usage{
		In:         m.Usage.Input,
		Out:        m.Usage.Output,
		CacheRead:  m.Usage.CacheRead,
		CacheWrite: m.Usage.CacheWrite,
	}
}

// textOf 取这一条里人话（text 块）。
func (m ocMessage) textOf() string {
	var parts []string
	for _, b := range m.Content {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, " ")
}

// scanOpenClaw 读一份 OpenClaw 会话记录，折进 s。
//
// 形态是 2026-10-09 在本机**真跑出来的记录**上读出来的（一条 MCP 工具调用：提问 → toolCall →
// toolResult → 收尾回答），不是照文档猜的。口径：
//
//  1. **一轮 = 一条 role=user 的消息**；用量挂在**每条 assistant 消息**上，按 message.responseId
//     去重取大（同一条消息若被重写，用量取终值）。
//  2. **失败判据是 toolResult.isError**（记录里现成的布尔），工具名同一条上有 `toolName`；
//     模型自己失败的那一轮另有 `stopReason:"error"` + `errorMessage`（实测「Connection error.」），
//     也记成失败 —— 不然那几轮会变成静默的空轮。
//  3. **成本记录里有**（`usage.cost.total`，美元）—— 这一家不用我们自己算，直接搬。
//  4. toolCall 的**名字带 MCP 命名空间前缀**（实测：`anc__anc_send_envelope`）—— ANC 的工具
//     在这家记录里叫 `anc__<工具名>`。原样搬，不剥前缀（剥了就和记录对不上）。
func scanOpenClaw(path string, s *Session) (map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLine)

	var (
		turns    []Turn
		ends     []time.Time
		byResp   = map[string]*Usage{}
		costResp = map[string]float64{}
		respTurn = map[string]int{}
		tools    = map[string]int{}
		uses     = map[string]int{}
		acts     []Act              // 一次次行使的原始事实（归集用）
		actIdx   = map[string]int{} // toolCall id → acts 下标
		failures []Failure
		badLines int
		cur      = -1
	)

	observe := func(ts time.Time) {
		if ts.IsZero() {
			return
		}
		if s.Started.IsZero() || ts.Before(s.Started) {
			s.Started = ts
		}
		if ts.After(s.Ended) {
			s.Ended = ts
		}
	}
	mark := func(ts time.Time) {
		if cur >= 0 && cur < len(ends) && ts.After(ends[cur]) {
			ends[cur] = ts
		}
	}

	for sc.Scan() {
		raw := sc.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var l ocLine
		if err := json.Unmarshal(raw, &l); err != nil {
			badLines++
			continue
		}
		var ts time.Time
		if l.Timestamp != "" {
			if parsed, perr := time.Parse(time.RFC3339Nano, l.Timestamp); perr == nil {
				ts = parsed
			}
		}
		if l.Type != "message" {
			// type=session 那一行带 cwd（实测）—— 认领这份分片属于哪个项目就靠它。
			if l.Cwd != "" && s.Cwd == "" {
				s.Cwd = l.Cwd
			}
			observe(ts)
			continue
		}

		m := l.Message
		if ts.IsZero() && m.Timestamp > 0 {
			ts = time.UnixMilli(m.Timestamp)
		}

		switch m.Role {
		case "user":
			turns = append(turns, Turn{At: ts, Prompt: head(m.textOf(), promptHead)})
			ends = append(ends, ts)
			cur = len(turns) - 1

		case "assistant":
			key := m.ResponseID
			if key == "" {
				// 没有 responseId 就没法去重（记录格式变了吗）——退到这条记录自己的 id，
				// 宁可**少并**（可能多算）也要把它算进来，并在 Problems 里说清楚。
				key = "id:" + l.ID
			}
			u := m.usageOf()
			if prev, ok := byResp[key]; ok {
				*prev = maxOf(*prev, u)
			} else {
				cp := u
				byResp[key] = &cp
			}
			respTurn[key] = cur
			// 金额与用量同源、同一条消息：也按 responseId 去重取大，不然重写一次就翻倍。
			if c := m.Usage.Cost.Total; c > costResp[key] {
				costResp[key] = c
			}
			// **模型这一轮自己失败了**也要记：记录里有现成的 `stopReason:"error"` + `errorMessage`
			// （实测：「Connection error.」）。不记的话，这几轮就成了「花了 0 元、什么也没干」的
			// 静默轮 —— 人会以为它在正常跑。
			if m.StopReason == "error" {
				why := m.ErrorMessage
				if strings.TrimSpace(why) == "" {
					why = m.textOf()
				}
				fa := Failure{Kind: "failed", Tool: "", Why: reasonHead(why), At: ts}
				failures = append(failures, fa)
				if cur >= 0 && cur < len(turns) {
					turns[cur].Failures = append(turns[cur].Failures, fa)
				}
			}
			for _, b := range m.Content {
				if b.Type != "toolCall" || b.Name == "" {
					continue
				}
				tools[b.Name]++
				if cur >= 0 {
					turns[cur].Tools = bumpBy(turns[cur].Tools, b.Name, 1)
					if b.ID != "" {
						uses[b.ID] = cur
					}
				}
				acts = putAct(acts, actIdx, Act{CallID: b.ID, At: ts, Tool: b.Name, Input: b.Arguments})
			}

		case "toolResult":
			// 行使的结果：判据是记录里现成的 isError。
			res, why := ResultOK, ""
			if m.IsError {
				res, why = ResultFailed, reasonHead(m.textOf())
			}
			setActResult(acts, actIdx, m.ToolCallID, res, why)
			// 没成功的动作：isError 为真。原因照抄记录原话（截断），不转述。
			if m.IsError {
				i := cur
				if j, ok := uses[m.ToolCallID]; ok {
					i = j
				}
				fa := Failure{Kind: "failed", Tool: m.ToolName, Why: reasonHead(m.textOf()), At: ts}
				failures = append(failures, fa)
				if i >= 0 && i < len(turns) {
					turns[i].Failures = append(turns[i].Failures, fa)
				}
			}
		}
		mark(ts)
		observe(ts)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if badLines > 0 {
		s.Problems = append(s.Problems, fmt.Sprintf(
			"%s 里有 %d 行解析不了 —— 记录格式可能变了，这份账不全", filepath.Base(path), badLines))
	}

	for _, c := range costResp {
		s.CostUSD += c
	}
	for key, u := range byResp {
		if i := respTurn[key]; i >= 0 && i < len(turns) {
			turns[i].Usage = turns[i].Usage.Add(*u)
		}
	}
	for i := range turns {
		turns[i].Usage = clampUsage(turns[i].Usage)
		s.Usage = s.Usage.Add(turns[i].Usage)
		sortCounts(turns[i].Tools)
		if i < len(ends) && ends[i].After(turns[i].At) {
			turns[i].Duration = ends[i].Sub(turns[i].At)
		}
	}
	s.Turns = append(s.Turns, turns...)
	s.Failures = append(s.Failures, failures...)
	s.Acts = append(s.Acts, settleActs(acts)...)
	for name, n := range tools {
		s.Tools = bumpBy(s.Tools, name, n)
	}
	sortCounts(s.Tools)

	// 两处「这份账仍然说不准」的地方，明说，别让人以为读到的是全的。
	s.Problems = append(s.Problems,
		"OpenClaw 的用量挂在**每条 assistant 消息**上，一轮可能有多条 —— 本读取器按 responseId 去重求和；"+
			"缓存读**单列、不含在输入里**（实测 input + output + cacheRead = totalTokens）",
		"模型自己失败的那一轮（实测 stopReason=error / 「Connection error.」）用量是 0、也没有 responseId —— "+
			"已按失败记，但**这一轮到底烧没烧 token 记录里看不出来**",
		"工具名带 MCP 命名空间前缀（实测 `anc__anc_send_envelope`）—— 原样搬，没有剥前缀")
	return uses, nil
}

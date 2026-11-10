// Package agent —— ANC 自己的回合循环：流式模型调用 + 工具调用 + 可回放的 transcript。
//
// 为什么单独一个包：在此之前 `anc` 是**一堆纯代码工具**（init/render/audit/job…），
// 每个命令自己读、自己写、自己退出。agent 不一样 —— 它要多轮、要流式、要在中途做决定、
// 要能被取消而不留下半截账。这四件事必须有**一个**内核，否则接 SSE 的和接 CLI 的各写一套，
// 第一次断连就会分成两个事实。
//
// 三条口径：
//  1. **流不是文本**：`tool_calls` 的 arguments 会分片到达，按 index 累积；`[DONE]` 之外
//     的心跳/注释行一律忽略（但不能当成结束）。
//  2. **工具失败回灌，不崩**：参数不合 schema、工具报错，都变成一条 tool 消息交回模型 ——
//     那是它的输入，不是我们的异常。
//  3. **可回放**：transcript 记的是「模型说了什么 + 工具返回了什么」，不是只记调用。
//     只记调用的话，回放时工具结果对不上，整段历史就成了故事。
package agent

import "encoding/json"

// Message 是 chat 协议里的一条消息（OpenAI 兼容形状）。
type Message struct {
	Role       string     `json:"role"` // system | user | assistant | tool
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall 是模型要求的一次工具调用。Arguments 是**原始 JSON 字符串**，
// 流式时它是分片拼起来的 —— 不要假设一次到齐。
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type,omitempty"`
	Function Func   `json:"function"`
}

type Func struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Usage 是一次调用的 token 账（与 trail 的四类对齐）。
type Usage struct {
	In         int `json:"in"`
	Out        int `json:"out"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
}

func (u Usage) Add(v Usage) Usage {
	return Usage{u.In + v.In, u.Out + v.Out, u.CacheRead + v.CacheRead, u.CacheWrite + v.CacheWrite}
}

// MarshalArgs 校验并压缩工具参数的 JSON。空串当 `{}`（很多模型的第一次分片是空的）。
func MarshalArgs(raw string) (json.RawMessage, error) {
	s := raw
	if s == "" {
		s = "{}"
	}
	if !json.Valid([]byte(s)) {
		return nil, &BadArgsError{Raw: raw}
	}
	return json.RawMessage(s), nil
}

// BadArgsError 表示模型给的参数根本不是合法 JSON —— 这不是程序的错，是它的输入。
type BadArgsError struct{ Raw string }

func (e *BadArgsError) Error() string { return "工具参数不是合法 JSON：" + e.Raw }

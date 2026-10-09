package trail

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ReaderCodeBuddy 是 CodeBuddy / WorkBuddy CLI 的读取器名（写在接入口径表的 reader 字段里）。
const ReaderCodeBuddy = "codebuddy-jsonl"

// codebuddyLine 是一行 CodeBuddy 记录里 trail 关心的字段。
//
// 这家**会写十几种非消息行**（file-history-snapshot / summary / topic / turn-metrics /
// model-usage / credit-usage…），我们只认下面这几种，其余一律跳过 —— 跳过不是丢账，
// 是因为它们本来就不是「谁问了什么、干了什么、花了多少」。
type codebuddyLine struct {
	Type      string `json:"type"`
	Role      string `json:"role"`
	Timestamp int64  `json:"timestamp"` // 毫秒 epoch —— 不是 RFC3339 字符串
	ID        string `json:"id"`
	ParentID  string `json:"parentId"`

	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`

	// function_call / function_call_result
	CallID string `json:"callId"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Output struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"output"`

	ProviderData struct {
		MessageID string `json:"messageId"`
		StepSeq   int    `json:"stepSeq"`
		Model     string `json:"model"`
		Agent     string `json:"agent"`
		Usage     struct {
			InputTokens  int `json:"inputTokens"`
			OutputTokens int `json:"outputTokens"`
			TotalTokens  int `json:"totalTokens"`
			InputDetails []struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"inputTokensDetails"`
		} `json:"usage"`
	} `json:"providerData"`
}

// usageOf 从一条记录里取用量。用量挂在**每一步**上，不在消息里。
func (l codebuddyLine) usageOf() Usage {
	u := l.ProviderData.Usage
	cached := 0
	if len(u.InputDetails) > 0 {
		cached = u.InputDetails[0].CachedTokens
	}
	return Usage{In: u.InputTokens, Out: u.OutputTokens, CacheRead: cached}
}

// textOf 取这一行里人说的话：user 行是 input_text，assistant 行是 output_text。
func (l codebuddyLine) textOf() string {
	var parts []string
	for _, b := range l.Content {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, " ")
}

// scanCodeBuddy 读一份 CodeBuddy 记录，折进 s。
//
// 形态是 2026-10-09 在沙箱里**真跑**出来的（用户提问 → Read 工具调用 → 工具结果 → 收尾回答），
// 不是照文档猜的。落盘位置与文件名跟 claude 同形（`<记录根>/projects/<cwd 转义>/<会话 id>.jsonl`），
// 但**行的语义不一样**，所以是另一个读取器：
//
//  1. **没有 cost-state 那种累计快照。** 用量挂在每一步的 `providerData.usage` 上，
//     同一步的 function_call 与 function_call_result 会带上**同一份**用量，
//     所以按 `providerData.messageId` 去重、取最大值 —— 按行相加会把账翻倍。
//  2. **工具调用不是 content block，是独立的行**（function_call / function_call_result）。
//  3. **时间戳是毫秒 epoch 数字**，不是 RFC3339 字符串。
//  4. 记录里**没有成本字段**（观测到的形态就是没有）。成本按 0 记并在 Problems 里说明 ——
//     「读不到」不能当成「花了 0 元」。
func scanCodeBuddy(path string, s *Session) (map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLine)

	var (
		turns    []Turn
		ends     []time.Time // 每一轮最后一条记录的时刻
		byStep   = map[string]*Usage{}
		stepTurn = map[string]int{}
		tools    = map[string]int{}
		uses     = map[string]int{} // callId → 轮次
		failures []Failure
		badLines int
		cur      = -1
	)

	mark := func(ts time.Time) {
		if cur >= 0 && cur < len(ends) && ts.After(ends[cur]) {
			ends[cur] = ts
		}
	}
	// 一步的账按 messageId 归并：同一步的两条记录带同一份用量，取大不取和。
	account := func(l codebuddyLine, ts time.Time) string {
		key := l.ProviderData.MessageID
		if key == "" {
			// 没有 messageId 就没法归并（记录格式变了吗）——用行自己的时间戳当键，
			// 宁可**少并**（可能多算）也要把它算进来，并在 Problems 里说清楚。
			key = "ts:" + strconv.FormatInt(l.Timestamp, 10)
		}
		u := l.usageOf()
		if prev, ok := byStep[key]; ok {
			*prev = maxOf(*prev, u)
		} else {
			cp := u
			byStep[key] = &cp
		}
		stepTurn[key] = cur
		return key
	}

	for sc.Scan() {
		raw := sc.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var l codebuddyLine
		if err := json.Unmarshal(raw, &l); err != nil {
			badLines++
			continue
		}
		var ts time.Time
		if l.Timestamp > 0 {
			ts = time.UnixMilli(l.Timestamp)
			if s.Started.IsZero() || ts.Before(s.Started) {
				s.Started = ts
			}
			if ts.After(s.Ended) {
				s.Ended = ts
			}
		}

		switch l.Type {
		case "message":
			switch l.Role {
			case "user":
				turns = append(turns, Turn{At: ts, Prompt: head(l.textOf(), promptHead)})
				ends = append(ends, ts)
				cur = len(turns) - 1
				account(l, ts)
				mark(ts)
			case "assistant":
				account(l, ts)
				mark(ts)
			}

		case "function_call":
			account(l, ts)
			if l.Name != "" {
				tools[l.Name]++
				if cur >= 0 {
					turns[cur].Tools = bumpBy(turns[cur].Tools, l.Name, 1)
				}
			}
			if l.CallID != "" && cur >= 0 {
				uses[l.CallID] = cur
			}
			mark(ts)

		case "function_call_result":
			account(l, ts)
			// 没成功的动作：status 不是 completed 的那些。**原因照抄记录原话**（截断），
			// 不转述 —— 「文件不在」和「权限没给」是两件完全不同的事，判据层再分。
			if l.Status != "" && l.Status != "completed" {
				fa := Failure{Kind: "failed", Tool: l.Name, Why: reasonHead(l.Output.Text), At: ts}
				failures = append(failures, fa)
				if cur >= 0 {
					turns[cur].Failures = append(turns[cur].Failures, fa)
				}
			}
			mark(ts)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if badLines > 0 {
		s.Problems = append(s.Problems, fmt.Sprintf(
			"%s 里有 %d 行解析不了 —— 记录格式可能变了，这份账不全", filepath.Base(path), badLines))
	}

	for key, u := range byStep {
		if i := stepTurn[key]; i >= 0 && i < len(turns) {
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
	for name, n := range tools {
		s.Tools = bumpBy(s.Tools, name, n)
	}
	sortCounts(s.Tools)

	// 两处「这份账仍然说不准」的地方，明说，别让人以为读到的是全的。
	s.Problems = append(s.Problems,
		"这家记录里没有成本字段（2026-10-09 观测到的形态）—— cost_usd 记 0 是**读不到**，不是花了 0 元",
		"缓存读（inputTokensDetails.cached_tokens）是否已含在输入里未验 —— 别把 in 与 cache_read 相加")
	return uses, nil
}

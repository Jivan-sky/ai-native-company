package trail

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 下面几个 helper 造的是 Codex rollout 的一行记录。
//
// 形状照 **2026-10-09 本机真记录** 写（一个 fork 出来的会话，5 轮 / 541 行），不是照文档猜的：
// 一行一个事件、事件的种类写在外层 type 上、payload 再带一个二级 type。

const cxSession = "01a120e7-dac9-7230-a2df-e2728e5c17f7"

func cxLine(ts, typ, payload string) string {
	return `{"timestamp":"` + ts + `","ordinal":1,"type":"` + typ + `","payload":` + payload + `}`
}

func cxMeta(ts, sessionID string) string {
	return cxLine(ts, "session_meta", `{"session_id":"`+sessionID+`","cwd":"D:\\work"}`)
}

func cxUser(turn, ts, text string) string {
	return cxLine(ts, "event_msg", `{"type":"item_completed","turn_id":"`+turn+
		`","item":{"type":"UserMessage","id":"um1","content":[{"type":"text","text":`+jsonString(text)+`}]}}`)
}

func cxCmd(turn, ts, callID, status, errText string) string {
	return cxLine(ts, "event_msg", `{"type":"item_completed","turn_id":"`+turn+
		`","item":{"type":"CommandExecution","id":"`+callID+`","status":"`+status+
		`","stderr":`+jsonString(errText)+`,"aggregated_output":`+jsonString(errText)+`}}`)
}

func cxCall(ts, turn, callID, name string) string {
	return cxLine(ts, "response_item", `{"type":"function_call","name":"`+name+`","call_id":"`+callID+
		`","internal_chat_message_metadata_passthrough":{"turn_id":"`+turn+`"}}`)
}

// cxUsage 造一条 token_usage_record。**故意把 thread_token_usage 写得极大** ——
// 它是线程累计（fork 出来的会话把前史一起带进来），读取器不许拿它当本会话的账。
func cxUsage(turn, ts, respID string, in, cached, out int) string {
	return cxLine(ts, "token_usage_record", `{"turn_id":"`+turn+`","response_id":"`+respID+
		`","usage":{"input_tokens":`+itoa(in)+`,"cached_input_tokens":`+itoa(cached)+
		`,"cache_write_input_tokens":0,"output_tokens":`+itoa(out)+`,"reasoning_output_tokens":1,"total_tokens":`+
		itoa(in+out)+`},"turn_token_usage":{"input_tokens":`+itoa(in)+`},"thread_token_usage":{"input_tokens":999999999,"output_tokens":88888888}}`)
}

// 一份**完整**的真形状：session_meta → 提问 → 工具调用 → 命令完成 → 两次调用的用量。
// （末行时间戳故意乱序：时长取最晚那条，不取最后一行。）
func TestScanCodexReadsRealShape(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-x.jsonl")
	writeLines(t, p,
		cxMeta("2026-10-09T13:44:24.000Z", cxSession),
		cxUser("t1", "2026-10-09T13:44:24.000Z", "跑一下 pwd 看在哪"),
		cxCall("2026-10-09T13:44:25.000Z", "t1", "call_1", "exec_command"),
		cxCmd("t1", "2026-10-09T13:44:26.000Z", "call_1", "completed", ""),
		cxUsage("t1", "2026-10-09T13:44:27.000Z", "r1", 100, 30, 10),
		cxUsage("t1", "2026-10-09T13:44:24.900Z", "r2", 200, 50, 20),
	)
	var s Session
	s.ID = cxSession
	uses, err := scanWith(ReaderCodex, p, &s)
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	// 一轮的账 = 该轮各次调用求和（实测与末条 turn_token_usage 逐字段相等）。
	if s.Usage.In != 300 || s.Usage.Out != 30 || s.Usage.CacheRead != 80 {
		t.Errorf("该是 in 300 / out 30 / cache_read 80（100+200 / 10+20 / 30+50），拿到 in %d / out %d / cache_read %d",
			s.Usage.In, s.Usage.Out, s.Usage.CacheRead)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("该只有一轮，拿到 %d", len(s.Turns))
	}
	if s.Turns[0].Prompt != "跑一下 pwd 看在哪" {
		t.Errorf("提问该从 item_completed/UserMessage 里取，拿到 %q", s.Turns[0].Prompt)
	}
	if s.Turns[0].Duration != 3*time.Second {
		t.Errorf("这一轮该干了 3s（**最晚**那条记录 27.000 - 提问 24.000：末行时间戳故意写成乱序，不许按最后一行算），拿到 %v",
			s.Turns[0].Duration)
	}
	if got := sumCounts(s.Tools); got != 1 || s.Tools[0].Name != "exec_command" {
		t.Errorf("工具该只有 exec_command ×1，拿到 %+v", s.Tools)
	}
	if len(s.Failures) != 0 {
		t.Errorf("这一轮没有没成功的动作，拿到 %+v", s.Failures)
	}
	if uses["call_1"] != 0 {
		t.Errorf("call_1 该归到第 1 轮（下标 0），拿到 %d", uses["call_1"])
	}
	if s.Started.IsZero() || s.Ended.IsZero() {
		t.Error("起止时间该从 RFC3339 时间戳解出来")
	}
}

// **thread_token_usage 不是本段会话的账。** 这条用例把它写到 9.9 亿，读取器必须一个字都不取。
func TestScanCodexIgnoresThreadCumulative(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-y.jsonl")
	writeLines(t, p,
		cxUser("t1", "2026-10-09T13:00:00.000Z", "你好"),
		cxUsage("t1", "2026-10-09T13:00:01.000Z", "r1", 10, 0, 2),
	)
	var s Session
	if _, err := scanWith(ReaderCodex, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if s.Usage.In != 10 || s.Usage.Out != 2 {
		t.Errorf("该只算这一次调用的 10 / 2，拿到 %d / %d —— 取到 thread_token_usage（9.9 亿）就是严重多报",
			s.Usage.In, s.Usage.Out)
	}
}

// 同一次调用被写多条（中间态）要按 response_id 取大；不同 response_id 才相加。
func TestScanCodexUsageDedupeByResponseID(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-z.jsonl")
	writeLines(t, p,
		cxUser("t1", "2026-10-09T13:00:00.000Z", "跑"),
		cxUsage("t1", "2026-10-09T13:00:01.000Z", "r1", 0, 0, 0),
		cxUsage("t1", "2026-10-09T13:00:02.000Z", "r1", 40, 5, 12),
		cxUsage("t1", "2026-10-09T13:00:03.000Z", "r2", 7, 1, 3),
	)
	var s Session
	if _, err := scanWith(ReaderCodex, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if s.Usage.In != 47 || s.Usage.Out != 15 {
		t.Errorf("同 r1 取大 40/12，再与 r2 相加，该是 in 47 / out 15；拿到 in %d / out %d", s.Usage.In, s.Usage.Out)
	}
}

// 失败判据在 CommandExecution.status 上：failed 才算，completed 不算。
// 工具名要靠 call_id 对上那条 function_call（记录里就这两条能对上）。
func TestScanCodexFailedCommandIsFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-f.jsonl")
	writeLines(t, p,
		cxUser("t1", "2026-10-09T13:00:00.000Z", "跑两条命令"),
		cxCall("2026-10-09T13:00:01.000Z", "t1", "call_1", "exec_command"),
		cxCmd("t1", "2026-10-09T13:00:02.000Z", "call_1", "failed", "命令报错的原话"),
		cxCall("2026-10-09T13:00:03.000Z", "t1", "call_2", "exec_command"),
		cxCmd("t1", "2026-10-09T13:00:04.000Z", "call_2", "completed", ""),
	)
	var s Session
	if _, err := scanWith(ReaderCodex, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Failures) != 1 {
		t.Fatalf("该只有 1 次没成功，拿到 %+v", s.Failures)
	}
	f := s.Failures[0]
	if f.Kind != "failed" || f.Tool != "exec_command" || !strings.Contains(f.Why, "命令报错的原话") {
		t.Errorf("失败该是 failed/exec_command/原话，拿到 %+v", f)
	}
	if len(s.Turns) != 1 || len(s.Turns[0].Failures) != 1 {
		t.Errorf("这一轮该一起记下这 1 次失败，拿到 %+v", s.Turns[0].Failures)
	}
}

// 合成注入的「用户消息」（response_item 的 message/role=user，环境上下文那种）**不算一轮** ——
// 提问只认 event_msg/item_completed 的 UserMessage。混起来会把轮次翻倍。
func TestScanCodexSyntheticUserMessageIsNotATurn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-s.jsonl")
	writeLines(t, p,
		cxLine("2026-10-09T13:00:00.000Z", "response_item",
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>…"}]}`),
		cxUser("t1", "2026-10-09T13:00:01.000Z", "真正的问题"),
	)
	var s Session
	if _, err := scanWith(ReaderCodex, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("该只有 1 轮（合成的那个不算），拿到 %d", len(s.Turns))
	}
	if s.Turns[0].Prompt != "真正的问题" {
		t.Errorf("提问该是真正那句，拿到 %q", s.Turns[0].Prompt)
	}
}

// 「这份账说不准」的地方必须写在 Problems 里：没有金额、线程累计不是本段账、缓存读含在输入里。
func TestScanCodexProblemsSayWhatIsUnreadable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-p.jsonl")
	writeLines(t, p,
		cxMeta("2026-10-09T13:00:00.000Z", "别的会话 id"),
		cxUser("t1", "2026-10-09T13:00:00.000Z", "你好"),
	)
	var s Session
	s.ID = cxSession
	if _, err := scanWith(ReaderCodex, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	joined := strings.Join(s.Problems, "\n")
	for _, want := range []string{"没有金额", "thread_token_usage", "含在输入里", "session_id"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Problems 里该提到 %q，拿到：\n%s", want, joined)
		}
	}
	if s.CostUSD != 0 || s.UnknownCost {
		t.Errorf("金额读不到就别编：CostUSD 该是 0 且不标 UnknownCost（那是给 harness 自己标的情形），拿到 %v/%v",
			s.CostUSD, s.UnknownCost)
	}
}

// 提问整段挂着环境上下文时（实测：`<in-app-browser-context …>` + `## My request:` + 真人那句），
// 取人问的那句 —— 不然每一轮看到的一行全是那套样板，人到底问了什么看不见。
func TestScanCodexPromptStripsAmbientContext(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-a.jsonl")
	ambient := "<in-app-browser-context source=\"ambient-ui-state\">\n" +
		"This block is automatically supplied ambient UI state, not part of the user's request.\n" +
		"- Current URL: http://127.0.0.1:8787/#/overview\n</in-app-browser-context>\n\n" +
		"## My request:\n零依赖优先。"
	writeLines(t, p,
		cxUser("t1", "2026-10-09T13:00:00.000Z", ambient),
		cxUsage("t1", "2026-10-09T13:00:01.000Z", "r1", 10, 0, 2),
	)
	var s Session
	if _, err := scanWith(ReaderCodex, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("该只有 1 轮，拿到 %d", len(s.Turns))
	}
	if s.Turns[0].Prompt != "零依赖优先。" {
		t.Errorf("提问该取 `## My request:` 后面那句，拿到 %q", s.Turns[0].Prompt)
	}
}

// 只有环境上下文、没有 `## My request:` 记号时，剥掉开头的块；剥完是空的就退回原文 ——
// 宁可看到的是一行样板，也不能把这一轮的提问抹成空白。
func TestScanCodexPromptNeverBecomesBlank(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-b.jsonl")
	only := "<environment_context>\n  <current_date>2026-10-09</current_date>\n</environment_context>"
	writeLines(t, p, cxUser("t1", "2026-10-09T13:00:00.000Z", only))
	var s Session
	if _, err := scanWith(ReaderCodex, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("该只有 1 轮，拿到 %d", len(s.Turns))
	}
	if s.Turns[0].Prompt == "" {
		t.Error("提问不许是空 —— 剥不出来就退回原文")
	}
	if strings.Contains(s.Turns[0].Prompt, "current_date") {
		t.Logf("退回原文时看到的是样板，这是有意为之：%q", s.Turns[0].Prompt)
	}
}

// 表里没有的记号不猜：不是环境上下文的方括号 / 尖括号开头，原样留着。
func TestScanCodexPromptKeepsUnknownTags(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-c.jsonl")
	writeLines(t, p, cxUser("t1", "2026-10-09T13:00:00.000Z", "<some-other-block>这也要原样留着</some-other-block>"))
	var s Session
	if _, err := scanWith(ReaderCodex, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if !strings.HasPrefix(s.Turns[0].Prompt, "<some-other-block>") {
		t.Errorf("没见过的记号该原样留着，拿到 %q", s.Turns[0].Prompt)
	}
}

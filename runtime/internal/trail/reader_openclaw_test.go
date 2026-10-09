package trail

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func boolLit(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// 下面几个 helper 造的是 OpenClaw 会话文件的一行记录。
//
// 形状照 **2026-10-09 本机真跑出来** 的记录写（一条 MCP 工具调用：提问 → toolCall →
// toolResult → 收尾回答），不是照文档猜的：用量与**金额**都挂在每条 assistant 消息上。

const ocSession = "8ac385b3-f8d2-4b59-b416-4966732ef098"

func ocRec(ts, typ, extra string) string {
	return `{"type":"` + typ + `","id":"rec1","timestamp":"` + ts + `"` + extra + `}`
}

func ocSessionLine(ts string) string {
	return ocRec(ts, "session", `,"version":3,"id":"`+ocSession+`","cwd":"C:\\work"`)
}

// ocUser 造一条用户消息 —— **一轮就是它**。
func ocUser(ts, text string, ms int64) string {
	return ocRec(ts, "message", `,"message":{"role":"user","content":[{"type":"text","text":`+jsonString(text)+
		`}],"timestamp":`+itoa64(ms)+`}`)
}

// ocAssistant 造一条 assistant 消息：content 里可以带 toolCall，用量与金额都挂在这一条上。
func ocAssistant(ts, respID string, in, cached, out int, cost float64, toolCallID, toolName string) string {
	blocks := `{"type":"text","text":"好的"}`
	if toolCallID != "" {
		blocks = `{"type":"toolCall","id":"` + toolCallID + `","name":"` + toolName + `","arguments":{}}`
	}
	return ocRec(ts, "message", `,"message":{"role":"assistant","content":[`+blocks+
		`],"provider":"deepseek","model":"deepseek-v4-flash","usage":{"input":`+itoa(in)+
		`,"output":`+itoa(out)+`,"cacheRead":`+itoa(cached)+`,"cacheWrite":0,"reasoningTokens":5,"totalTokens":`+
		itoa(in+out+cached)+`,"cost":{"total":`+ftoa(cost)+`}},"responseId":"`+respID+`"}`)
}

func ocToolResult(ts, toolCallID, toolName, text string, isErr bool) string {
	return ocRec(ts, "message", `,"message":{"role":"toolResult","toolCallId":"`+toolCallID+
		`","toolName":"`+toolName+`","content":[{"type":"text","text":`+jsonString(text)+`}],"isError":`+boolLit(isErr)+`}`)
}

// 一份**完整**的真形状：会话头 → 非消息行 → 提问 → toolCall → toolResult → 收尾回答。
func TestScanOpenClawReadsRealShape(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		ocSessionLine("2026-10-09T14:44:10.000Z"),
		ocRec("2026-10-09T14:44:10.000Z", "model_change", `,"provider":"deepseek","modelId":"deepseek-v4-flash"`),
		ocRec("2026-10-09T14:44:10.000Z", "custom", `,"customType":"model-snapshot","data":{}`),
		ocUser("2026-10-09T14:44:10.800Z", "递一封信", 1791557050822),
		ocAssistant("2026-10-09T14:44:12.200Z", "resp-1", 20629, 14464, 139, 0.00292698, "call_1", "anc__anc_send_envelope"),
		ocToolResult("2026-10-09T14:44:14.100Z", "call_1", "anc__anc_send_envelope", "收了", false),
		ocAssistant("2026-10-09T14:44:15.500Z", "resp-2", 100, 20, 30, 0.0001, "", ""),
	)
	var s Session
	uses, err := scanWith(ReaderOpenClaw, p, &s)
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("该只有一轮（非消息行不算），拿到 %d", len(s.Turns))
	}
	if s.Turns[0].Prompt != "递一封信" {
		t.Errorf("提问 = %q", s.Turns[0].Prompt)
	}
	// 一轮里两条 assistant 消息各带一份用量，求和。
	if s.Usage.In != 20729 || s.Usage.Out != 169 || s.Usage.CacheRead != 14484 {
		t.Errorf("该是 in 20729 / out 169 / cache_read 14484（20629+100 / 139+30 / 14464+20），拿到 in %d / out %d / cache_read %d",
			s.Usage.In, s.Usage.Out, s.Usage.CacheRead)
	}
	// **金额记录里有**，直接搬：0.00292698 + 0.0001。
	if got := s.CostUSD; got < 0.003026 || got > 0.003028 {
		t.Errorf("金额该是 0.00302698（记录里现成的 usage.cost.total 求和），拿到 %v", got)
	}
	if got := sumCounts(s.Tools); got != 1 || s.Tools[0].Name != "anc__anc_send_envelope" {
		t.Errorf("工具名该原样搬（带 MCP 命名空间前缀），拿到 %+v", s.Tools)
	}
	if len(s.Failures) != 0 {
		t.Errorf("这一轮没有没成功的动作，拿到 %+v", s.Failures)
	}
	if uses["call_1"] != 0 {
		t.Errorf("call_1 该归到第 1 轮（下标 0），拿到 %d", uses["call_1"])
	}
	if s.Turns[0].Duration != 4700*time.Millisecond {
		t.Errorf("这一轮该干了 4.7s（14:44:15.500 - 14:44:10.800），拿到 %v", s.Turns[0].Duration)
	}
}

// 失败判据是 toolResult.isError：true 才算，false 不算（把正常的说成故障，人会去查不存在的问题）。
func TestScanOpenClawToolErrorIsFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		ocUser("2026-10-09T14:00:00.000Z", "读两个文件", 1),
		ocAssistant("2026-10-09T14:00:01.000Z", "r1", 10, 0, 2, 0.000001, "c1", "read"),
		ocToolResult("2026-10-09T14:00:02.000Z", "c1", "read", "文件不在：D:\\nope.txt", true),
		ocAssistant("2026-10-09T14:00:03.000Z", "r2", 20, 0, 3, 0.000002, "c2", "read"),
		ocToolResult("2026-10-09T14:00:04.000Z", "c2", "read", "ok", false),
	)
	var s Session
	if _, err := scanWith(ReaderOpenClaw, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Failures) != 1 {
		t.Fatalf("该只有 1 次没成功，拿到 %+v", s.Failures)
	}
	f := s.Failures[0]
	if f.Kind != "failed" || f.Tool != "read" || !strings.Contains(f.Why, "文件不在") {
		t.Errorf("失败该是 failed/read/原话，拿到 %+v", f)
	}
	if len(s.Turns) != 1 || len(s.Turns[0].Failures) != 1 {
		t.Errorf("这一轮该一起记下这 1 次失败，拿到 %+v", s.Turns[0].Failures)
	}
}

// 缓存读**单列、不含在输入里**（实测 input + output + cacheRead = totalTokens）——
// 这是它和 codex 相反的的地方，混用会把输入算错。Problems 里也要说明。
func TestScanOpenClawCacheReadIsSeparateFromInput(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		ocUser("2026-10-09T14:00:00.000Z", "你好", 1),
		ocAssistant("2026-10-09T14:00:01.000Z", "r1", 100, 900, 7, 0, "", ""),
	)
	var s Session
	if _, err := scanWith(ReaderOpenClaw, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if s.Usage.In != 100 {
		t.Errorf("输入该就是 100（缓存读 900 是单列的，不许加进去），拿到 %d", s.Usage.In)
	}
	if s.Usage.CacheRead != 900 {
		t.Errorf("缓存读该单列 900，拿到 %d", s.Usage.CacheRead)
	}
	if !strings.Contains(strings.Join(s.Problems, "\n"), "不含在输入里") {
		t.Errorf("Problems 里该说明缓存读的口径，拿到 %v", s.Problems)
	}
}

// 这家会写好几种非消息行（session / model_change / thinking_level_change / custom）：
// 跳过它们，既不算轮次也不当坏行。但**真解析不了的行要数出来报**。
func TestScanOpenClawSkipsNonMessageLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		ocSessionLine("2026-10-09T14:00:00.000Z"),
		ocRec("2026-10-09T14:00:00.000Z", "thinking_level_change", `,"thinkingLevel":"high"`),
		ocUser("2026-10-09T14:00:01.000Z", "你好", 1),
		`{这不是 JSON`,
	)
	var s Session
	if _, err := scanWith(ReaderOpenClaw, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Turns) != 1 {
		t.Errorf("非消息行不该新增轮次，拿到 %d 轮", len(s.Turns))
	}
	var sawBad bool
	for _, pr := range s.Problems {
		if strings.Contains(pr, "解析不了") {
			sawBad = true
		}
	}
	if !sawBad {
		t.Errorf("有一行解析不了，必须报出来（静默少报和假绿是同一类错误），拿到 %v", s.Problems)
	}
}

// 同一条 assistant 消息若被重写，按 responseId 去重取大，不是相加。
func TestScanOpenClawUsageDedupeByResponseID(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		ocUser("2026-10-09T14:00:00.000Z", "跑", 1),
		ocAssistant("2026-10-09T14:00:01.000Z", "r1", 0, 0, 0, 0, "", ""),
		ocAssistant("2026-10-09T14:00:02.000Z", "r1", 40, 5, 12, 0.5, "", ""),
	)
	var s Session
	if _, err := scanWith(ReaderOpenClaw, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if s.Usage.In != 40 || s.Usage.Out != 12 {
		t.Errorf("同 responseId 取大该是 in 40 / out 12，拿到 in %d / out %d", s.Usage.In, s.Usage.Out)
	}
	if s.CostUSD != 0.5 {
		t.Errorf("金额也必须按 responseId 去重（用量去重、金额相加 = 自相矛盾），该是 0.5，拿到 %v", s.CostUSD)
	}
}

// 模型这一轮自己失败了：记录里有现成的 stopReason:"error" + errorMessage（实测「Connection error.」）。
// 不记的话，这几轮就成了「花了 0 元、什么也没干」的静默轮 —— 人会以为它在正常跑。
func ocAssistantError(ts, errText string) string {
	return ocRec(ts, "message", `,"message":{"role":"assistant","content":[{"type":"text","text":"[assistant turn failed before producing content]"}],"provider":"deepseek","model":"deepseek-v4-flash","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"total":0}},"stopReason":"error","errorMessage":`+jsonString(errText)+`}`)
}

func TestScanOpenClawModelErrorIsFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		ocUser("2026-10-09T14:32:46.000Z", "只回复两个字：通了", 1791556366894),
		ocAssistantError("2026-10-09T14:32:48.000Z", "Connection error."),
		ocUser("2026-10-09T14:34:32.000Z", "只回复两个字：通了", 1791556472531),
		ocAssistant("2026-10-09T14:34:35.000Z", "r9", 21135, 12800, 93, 0.00298494, "", ""),
	)
	var s Session
	if _, err := scanWith(ReaderOpenClaw, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Turns) != 2 {
		t.Fatalf("人问了两次就是两轮，拿到 %d", len(s.Turns))
	}
	if len(s.Failures) != 1 {
		t.Fatalf("该只有 1 次失败（第二次成了），拿到 %+v", s.Failures)
	}
	f := s.Failures[0]
	if f.Kind != "failed" || !strings.Contains(f.Why, "Connection error") {
		t.Errorf("失败该是 failed / 原话 Connection error.，拿到 %+v", f)
	}
	if f.Tool != "" {
		t.Errorf("这是模型自己失败、不是某个工具失败，Tool 该留空，拿到 %q", f.Tool)
	}
	if len(s.Turns[0].Failures) != 1 {
		t.Errorf("失败该挂在第 1 轮上，拿到 %+v", s.Turns[0].Failures)
	}
	if s.Usage.In != 21135 || s.Usage.Out != 93 {
		t.Errorf("失败那轮的用量是 0，账该只有成了的那一轮 21135 / 93，拿到 %d / %d", s.Usage.In, s.Usage.Out)
	}
}

// content **也有整段是字符串**的时候（实测 `"content":"[OpenClaw heartbeat poll]"`）。
// 只认块数组的话，这一行会整行解析不了 —— 提问丢了，还被报成「记录格式可能变了」。
func TestScanOpenClawStringContentIsAccepted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		ocRec("2026-10-09T14:51:46.000Z", "message", `,"message":{"role":"user","content":"[OpenClaw heartbeat poll]","timestamp":1791557501617}`),
		ocAssistant("2026-10-09T14:51:48.000Z", "r1", 21135, 12800, 93, 0.00298494, "", ""),
	)
	var s Session
	if _, err := scanWith(ReaderOpenClaw, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("该只有 1 轮，拿到 %d", len(s.Turns))
	}
	if s.Turns[0].Prompt != "[OpenClaw heartbeat poll]" {
		t.Errorf("提问该是那一整段字符串，拿到 %q", s.Turns[0].Prompt)
	}
	for _, pr := range s.Problems {
		if strings.Contains(pr, "解析不了") {
			t.Errorf("这一行是合法记录，不该报成解析不了：%v", s.Problems)
		}
	}
	if s.Usage.In != 21135 {
		t.Errorf("账该照算 21135，拿到 %d", s.Usage.In)
	}
}

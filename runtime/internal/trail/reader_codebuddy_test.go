package trail

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 下面几个 helper 造的是 CodeBuddy / WorkBuddy CLI 的一行记录。
//
// 形状照 **2026-10-09 沙箱里真跑出来** 的记录写（用户提问 → Read 工具调用 → 工具结果 →
// 收尾回答），不是照文档猜的：时间戳是**毫秒 epoch 数字**、工具调用是**独立的行**、
// 用量挂在每一步的 `providerData.usage` 上、同一步的两条记录带**同一份**用量。
const cbSession = "01a12103-4bb7-7e37-80aa-bcd1334d4293"

func ms64(n int64) string { return strconv.FormatInt(n, 10) }

func cbUser(ts int64, text string) string {
	return `{"id":"u1","timestamp":` + ms64(ts) + `,"type":"message","role":"user","content":[{"type":"input_text","text":` +
		jsonString(text) + `}],"sessionId":"` + cbSession + `","cwd":"d:\\work"}`
}

func cbAssistant(ts int64, msgID, text string, in, out int) string {
	return `{"id":"a1","parentId":"u1","timestamp":` + ms64(ts) + `,"type":"message","role":"assistant","providerData":{` +
		`"messageId":"` + msgID + `","stepSeq":2,"usage":{"inputTokens":` + itoa(in) + `,"outputTokens":` + itoa(out) +
		`,"totalTokens":` + itoa(in+out) + `}},"status":"completed","content":[{"type":"output_text","text":` +
		jsonString(text) + `}],"sessionId":"` + cbSession + `"}`
}

func cbFuncCall(ts int64, msgID, callID, name, args string, in, out int) string {
	return `{"id":"f1","timestamp":` + ms64(ts) + `,"type":"function_call","providerData":{"messageId":"` + msgID +
		`","stepSeq":1,"usage":{"inputTokens":` + itoa(in) + `,"outputTokens":` + itoa(out) + `}},"callId":"` + callID +
		`","name":"` + name + `","arguments":` + jsonString(args) + `,"sessionId":"` + cbSession + `"}`
}

func cbFuncResult(ts int64, msgID, callID, name, status, text string, in, out int) string {
	return `{"id":"r1","timestamp":` + ms64(ts) + `,"type":"function_call_result","providerData":{"messageId":"` + msgID +
		`","usage":{"inputTokens":` + itoa(in) + `,"outputTokens":` + itoa(out) + `}},"callId":"` + callID +
		`","name":"` + name + `","status":"` + status + `","output":{"type":"text","text":` + jsonString(text) +
		`},"sessionId":"` + cbSession + `"}`
}

// 一份**完整**的真形状：用户行 → 非消息行 → 工具调用 → 工具结果 → 收尾回答。
func TestScanCodeBuddyReadsRealShape(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		cbUser(1000, `读一下 probe\readme.txt`),
		`{"timestamp":1010,"type":"file-history-snapshot","messageId":"u1","snapshot":{}}`,
		cbFuncCall(1500, "m1", "call_1", "Read", `{"file_path":"D:\\probe\\readme.txt"}`, 40, 12),
		cbFuncResult(1700, "m1", "call_1", "Read", "completed", "probe file for tool-call shape", 40, 12),
		cbAssistant(1900, "m2", "读到文件，已阅。", 60, 6),
	)
	var s Session
	uses, err := scanWith(ReaderCodeBuddy, p, &s)
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	// 同一步的 function_call 与 function_call_result 带**同一份**用量，只能算一次：
	// 40+60 / 12+6。按行相加会得到 140 / 30 —— 和 claude 是同一类坑（账翻倍），触发方式不同。
	if s.Usage.In != 100 || s.Usage.Out != 18 {
		t.Errorf("按步去重后该是 in 100 / out 18，拿到 in %d / out %d", s.Usage.In, s.Usage.Out)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("该只有一轮，拿到 %d", len(s.Turns))
	}
	if s.Turns[0].Prompt != `读一下 probe\readme.txt` {
		t.Errorf("这一轮的提问 = %q", s.Turns[0].Prompt)
	}
	if s.Turns[0].Duration != 900*time.Millisecond {
		t.Errorf("这一轮实际干了 900ms（最后一条记录 1900 - 提问 1000），拿到 %v", s.Turns[0].Duration)
	}
	if got := sumCounts(s.Tools); got != 1 || s.Tools[0].Name != "Read" {
		t.Errorf("工具该只有 Read ×1，拿到 %+v", s.Tools)
	}
	if len(s.Failures) != 0 {
		t.Errorf("这一轮没有失败的动作用，拿到 %+v", s.Failures)
	}
	if uses["call_1"] != 0 {
		t.Errorf("call_1 该归到第 1 轮（下标 0），拿到 %d", uses["call_1"])
	}
	if s.Started.IsZero() || s.Ended.IsZero() {
		t.Error("起止时间该从毫秒 epoch 时间戳解出来")
	}
}

// 同一个 messageId 会写多条（中间态把 output 写成 0，最后一条才是终值）——取最大值，不是相加。
func TestScanCodeBuddyStepUsageTakesMax(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		cbUser(1000, "跑一下"),
		cbFuncCall(1400, "m1", "call_1", "Read", "{}", 0, 0),
		cbFuncResult(1600, "m1", "call_1", "Read", "completed", "ok", 40, 12),
	)
	var s Session
	if _, err := scanWith(ReaderCodeBuddy, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if s.Usage.In != 40 || s.Usage.Out != 12 {
		t.Errorf("同一步取最大值该是 in 40 / out 12，拿到 in %d / out %d（相加会得到 40 / 12 之外的数）",
			s.Usage.In, s.Usage.Out)
	}
}

// 没成功的动作：`function_call_result.status != completed` 才算，原因照抄记录原话。
// `completed` 的那种**不能**记成失败 —— 把正常的说成故障，人会去查一个不存在的问题。
func TestScanCodeBuddyFailedToolResult(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		cbUser(1000, "读两个文件"),
		cbFuncCall(1200, "m1", "call_1", "Read", `{"file_path":"D:\\nope.txt"}`, 10, 2),
		cbFuncResult(1300, "m1", "call_1", "Read", "error", "Error in agent run", 10, 2),
		cbFuncCall(1400, "m2", "call_2", "Read", `{"file_path":"D:\\ok.txt"}`, 20, 3),
		cbFuncResult(1500, "m2", "call_2", "Read", "completed", "ok", 20, 3),
	)
	var s Session
	if _, err := scanWith(ReaderCodeBuddy, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Failures) != 1 {
		t.Fatalf("该只有 1 次没成功，拿到 %+v", s.Failures)
	}
	f := s.Failures[0]
	if f.Kind != "failed" || f.Tool != "Read" || !strings.Contains(f.Why, "Error in agent run") {
		t.Errorf("失败该是 failed/Read/原话，拿到 %+v", f)
	}
	if len(s.Turns) != 1 || len(s.Turns[0].Failures) != 1 {
		t.Errorf("这一轮该一起记下这 1 次失败，拿到 %+v", s.Turns[0].Failures)
	}
}

// 这家会写十几种非消息行（file-history-snapshot / summary / turn-metrics…）：跳过它们，
// 既不算轮次也不当坏行。但**真解析不了的行要数出来报** —— 静默少报和假绿是同一类错误。
func TestScanCodeBuddySkipsNonMessageLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		cbUser(1000, "你好"),
		`{"timestamp":1010,"type":"file-history-snapshot","messageId":"u1"}`,
		`{"timestamp":1020,"type":"summary","summary":"一段摘要"}`,
		`{"timestamp":1030,"type":"turn-metrics","turns":1}`,
		cbAssistant(1100, "m1", "在", 5, 1),
		`{这不是 JSON`,
	)
	var s Session
	if _, err := scanWith(ReaderCodeBuddy, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Turns) != 1 {
		t.Errorf("非消息行不该新增轮次，拿到 %d 轮", len(s.Turns))
	}
	var sawBad, sawCost bool
	for _, pr := range s.Problems {
		if strings.Contains(pr, "行解析不了") {
			sawBad = true
		}
		if strings.Contains(pr, "没有成本字段") {
			sawCost = true
		}
	}
	if !sawBad {
		t.Errorf("有一行解析不了，该报出来，拿到 %v", s.Problems)
	}
	if !sawCost {
		t.Errorf("这家没有成本字段，该明说「读不到」而不是记成 0 元，拿到 %v", s.Problems)
	}
}

// 读取器名不认识**必须报错**：静默退回默认读法会把别家的记录按错的口径读成一堆数字。
func TestScanWithRejectsUnknownReader(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p, cbUser(1000, "你好"))
	var s Session
	if _, err := scanWith("猜的读取器", p, &s); err == nil {
		t.Error("不认识的读取器却不报错")
	}
}

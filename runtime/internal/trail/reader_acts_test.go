package trail

import (
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/harness"
)

// 这一份测的是**行使（Act）**：一次工具调用的原始事实（谁调的、对谁、成没成）。
// 四家读取器都得填 —— 归集（internal/audit）只认 Acts，不再自己解析任何一家的记录。
//
// 形状全部照**真记录**写：claude 是 content 块数组，codex / codebuddy 的入参是
// **字符串**形式的 arguments，openclaw 的是**对象**。这一条差别是最容易写错的地方。

func actOf(t *testing.T, s Session, callID string) Act {
	t.Helper()
	for _, a := range s.Acts {
		if a.CallID == callID {
			return a
		}
	}
	t.Fatalf("没有 id 为 %q 的行使，拿到 %+v", callID, s.Acts)
	return Act{}
}

// ---- claude：tool_use 与 tool_result 按 id 配对 ----

func TestActsClaudePairsUseAndResult(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		promptAt("p1", "2026-10-07T12:00:00Z", "读一下 a.md"),
		assistant("2026-10-07T12:00:01Z", "m1", 10, 1,
			`[{"type":"tool_use","name":"Read","id":"c1","input":{"file_path":"/v/a.md"}}]`),
		`{"type":"user","timestamp":"2026-10-07T12:00:02Z","toolUseResult":{"ok":true},"message":{"content":[{"type":"tool_result","tool_use_id":"c1","is_error":false,"content":"body"}]}}`,
		assistant("2026-10-07T12:00:03Z", "m2", 10, 1,
			`[{"type":"tool_use","name":"Write","id":"c2","input":{"file_path":"/v/b.md"}}]`),
		toolResultErr("2026-10-07T12:00:04Z", "c2", "permission", "Permission to use Write has been denied. IMPORTANT: 给模型看的长段"),
		assistant("2026-10-07T12:00:05Z", "m3", 10, 1,
			`[{"type":"tool_use","name":"Edit","id":"c3","input":{"file_path":"/v/c.md"}}]`),
		toolResultErr("2026-10-07T12:00:06Z", "c3", "", "File does not exist."),
		assistant("2026-10-07T12:00:07Z", "m4", 10, 1,
			`[{"type":"tool_use","name":"Read","id":"c4","input":{"file_path":"/v/d.md"}}]`),
	)
	var s Session
	if _, err := scanWith("claude-jsonl", p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Acts) != 4 {
		t.Fatalf("该 4 次行使，拿到 %d：%+v", len(s.Acts), s.Acts)
	}
	if a := actOf(t, s, "c1"); a.Result != ResultOK || a.Tool != "Read" ||
		string(a.Input) != `{"file_path":"/v/a.md"}` {
		t.Errorf("c1：该 ok / Read / 带 file_path，拿到 %+v", a)
	}
	if a := actOf(t, s, "c2"); a.Result != ResultDenied {
		t.Errorf("c2 被权限规则挡下该是 denied，拿到 %+v", a)
	} else if a.Why == "" || len(a.Why) > 200 {
		t.Errorf("c2 的理由该照抄原话并切掉给模型看的那段，拿到 %q", a.Why)
	}
	if a := actOf(t, s, "c3"); a.Result != ResultFailed {
		t.Errorf("c3 真失败该是 failed（不是 denied），拿到 %+v", a)
	}
	if a := actOf(t, s, "c4"); a.Result != ResultUnknown {
		t.Errorf("c4 没配到结果该是 unknown，拿到 %+v", a)
	}
}

// 同一条 tool_use 会被写多行（流式中间态 input 还没流完）—— 后到的覆盖先到的，
// 而且**配到结果之后不再被调用行覆盖回去**。
func TestActsClaudeLastWriteWinsAndKeepsResult(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		assistant("2026-10-07T12:00:01Z", "m1", 10, 1, `[{"type":"tool_use","name":"Read","id":"c1","input":{}}]`),
		assistant("2026-10-07T12:00:02Z", "m1", 10, 1,
			`[{"type":"tool_use","name":"Read","id":"c1","input":{"file_path":"/v/final.md"}}]`),
		`{"type":"user","timestamp":"2026-10-07T12:00:03Z","toolUseResult":{"ok":true},"message":{"content":[{"type":"tool_result","tool_use_id":"c1","is_error":false,"content":"body"}]}}`,
		assistant("2026-10-07T12:00:04Z", "m1", 10, 1, `[{"type":"tool_use","name":"Read","id":"c1","input":{}}]`),
	)
	var s Session
	if _, err := scanWith("claude-jsonl", p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Acts) != 1 {
		t.Fatalf("同一条调用只该落一条，拿到 %d", len(s.Acts))
	}
	if a := s.Acts[0]; string(a.Input) != `{"file_path":"/v/final.md"}` || a.Result != ResultOK {
		t.Errorf("该留最后那份完整 input、结果仍是 ok，拿到 %+v", a)
	}
}

// ---- codex：入参是 arguments 字符串；成败在 item_completed 的 status 上 ----

func acxCxCall(ts, turn, callID, name, args string) string {
	return cxLine(ts, "response_item", `{"type":"function_call","name":"`+name+`","call_id":"`+callID+
		`","arguments":`+jsonString(args)+`,"internal_chat_message_metadata_passthrough":{"turn_id":"`+turn+`"}}`)
}

func acxCxFco(ts, turn, callID, out string) string {
	return cxLine(ts, "response_item", `{"type":"function_call_output","call_id":"`+callID+
		`","output":`+jsonString(out)+`,"internal_chat_message_metadata_passthrough":{"turn_id":"`+turn+`"}}`)
}

// cxMcp 是 McpToolCall 那一项：**也带 status**（实测），成败另外还有 result.isError。
func acxCxMcp(ts, turn, callID, server, tool, status string, isErr bool, text string) string {
	e := "false"
	if isErr {
		e = "true"
	}
	return cxLine(ts, "event_msg", `{"type":"item_completed","turn_id":"`+turn+`","item":{"type":"McpToolCall","id":"`+
		callID+`","server":"`+server+`","tool":"`+tool+`","status":"`+status+
		`","result":{"isError":`+e+`,"content":[{"type":"text","text":`+jsonString(text)+`}]}}}`)
}

func TestActsCodexStatusOutputAndCwd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-x.jsonl")
	writeLines(t, p,
		cxMeta("2026-10-09T13:44:24.000Z", cxSession),
		acxCxCall("2026-10-09T13:44:25.000Z", "t1", "call_1", "exec_command", `{"cmd":"pwd","workdir":"D:/work"}`),
		cxCmd("t1", "2026-10-09T13:44:26.000Z", "call_1", "completed", ""),
		acxCxCall("2026-10-09T13:44:27.000Z", "t1", "call_2", "exec_command", `{"cmd":"ls /nope"}`),
		cxCmd("t1", "2026-10-09T13:44:28.000Z", "call_2", "failed", "No such file or directory\nIMPORTANT: 这是给模型看的"),
		acxCxCall("2026-10-09T13:44:29.000Z", "t1", "call_3", "list_mcp_resources", `{}`),
		acxCxMcp("2026-10-09T13:44:30.000Z", "t1", "call_3", "codex", "list_mcp_resources", "completed", false, `{"resources":[]}`),
		acxCxCall("2026-10-09T13:44:31.000Z", "t1", "call_4", "view_image", `{"path":"D:/a.png"}`),
		acxCxFco("2026-10-09T13:44:32.000Z", "t1", "call_4", "image bytes"),
		acxCxCall("2026-10-09T13:44:33.000Z", "t1", "call_5", "write_stdin", `{"session_id":1}`),
	)
	var s Session
	s.ID = cxSession
	if _, err := scanWith(ReaderCodex, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if s.Cwd != `D:\work` {
		t.Errorf("cwd 该从 session_meta 里取出来（归集靠它认领项目），拿到 %q", s.Cwd)
	}
	if len(s.Acts) != 5 {
		t.Fatalf("该 5 次行使，拿到 %d：%+v", len(s.Acts), s.Acts)
	}
	if a := actOf(t, s, "call_1"); a.Result != ResultOK || string(a.Input) != `{"cmd":"pwd","workdir":"D:/work"}` {
		t.Errorf("call_1：该 ok 且入参是那段 arguments 字符串，拿到 %+v", a)
	}
	if a := actOf(t, s, "call_2"); a.Result != ResultFailed || a.Why == "" {
		t.Errorf("call_2 status=failed 该落 failed 并带上原话，拿到 %+v", a)
	}
	if a := actOf(t, s, "call_3"); a.Result != ResultOK {
		t.Errorf("call_3 的 McpToolCall status=completed 该落 ok，拿到 %+v", a)
	}
	if a := actOf(t, s, "call_4"); a.Result != ResultOK {
		t.Errorf("call_4 没有 status，但有 function_call_output —— 该落 ok，拿到 %+v", a)
	}
	if a := actOf(t, s, "call_5"); a.Result != ResultUnknown {
		t.Errorf("call_5 既没有 status 也没有输出 —— 该落 unknown（不是「没发生」），拿到 %+v", a)
	}
}

// ---- openclaw：入参是**对象**；成败在 toolResult 的 isError 上 ----

func acxOcSession(ts, cwd string) string {
	return `{"type":"session","timestamp":"` + ts + `","id":"s1","cwd":` + jsonString(cwd) + `}`
}

func acxOcToolCall(ts, callID, name, args string) string {
	return `{"type":"message","timestamp":"` + ts + `","message":{"role":"assistant","responseId":"r1",` +
		`"content":[{"type":"toolCall","id":"` + callID + `","name":"` + name + `","arguments":` + args + `}]}}`
}

func TestActsOpenClawToolCallObjectArgsAndCwd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		acxOcSession("2026-10-09T21:00:00.000Z", `D:\sandboxgentcwd`),
		acxOcToolCall("2026-10-09T21:00:01.000Z", "call_1", "anc__anc_send_envelope",
			`{"who":"alice","kind":"report"}`),
		`{"type":"message","timestamp":"2026-10-09T21:00:02.000Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"anc__anc_send_envelope","content":[{"type":"text","text":"收了"}]}}`,
		acxOcToolCall("2026-10-09T21:00:03.000Z", "call_2", "read", `{"path":"/v/a.md"}`),
		`{"type":"message","timestamp":"2026-10-09T21:00:04.000Z","message":{"role":"toolResult","toolCallId":"call_2","toolName":"read","isError":true,"content":[{"type":"text","text":"no such file"}]}}`,
	)
	var s Session
	if _, err := scanWith(ReaderOpenClaw, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if s.Cwd != `D:\sandboxgentcwd` {
		t.Errorf("cwd 该从 type=session 那一行取出来，拿到 %q", s.Cwd)
	}
	if len(s.Acts) != 2 {
		t.Fatalf("该 2 次行使，拿到 %d：%+v", len(s.Acts), s.Acts)
	}
	if a := actOf(t, s, "call_1"); a.Result != ResultOK || string(a.Input) != `{"who":"alice","kind":"report"}` {
		t.Errorf("call_1：该 ok 且入参原样是那个对象，拿到 %+v", a)
	}
	if a := actOf(t, s, "call_2"); a.Result != ResultFailed || a.Why == "" {
		t.Errorf("call_2 isError=true 该落 failed 并带原话，拿到 %+v", a)
	}
}

// ---- codebuddy：入参也是字符串；成败在 function_call_result 的 status 上 ----

func TestActsCodeBuddyCallAndResult(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		cbUser(1000, `读一下 probe\readme.txt`),
		cbFuncCall(1500, "m1", "call_1", "Read", `{"file_path":"D:/probe/readme.txt"}`, 40, 12),
		cbFuncResult(1700, "m1", "call_1", "Read", "completed", "probe file", 40, 12),
		cbFuncCall(1800, "m2", "call_2", "Read", `{"file_path":"D:/nope.txt"}`, 40, 12),
		cbFuncResult(1900, "m2", "call_2", "Read", "failed", "no such file", 40, 12),
	)
	var s Session
	if _, err := scanWith(ReaderCodeBuddy, p, &s); err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if len(s.Acts) != 2 {
		t.Fatalf("该 2 次行使，拿到 %d：%+v", len(s.Acts), s.Acts)
	}
	if a := actOf(t, s, "call_1"); a.Result != ResultOK || string(a.Input) != `{"file_path":"D:/probe/readme.txt"}` {
		t.Errorf("call_1：该 ok 且入参从 arguments 字符串转过来，拿到 %+v", a)
	}
	if a := actOf(t, s, "call_2"); a.Result != ResultFailed || a.Why == "" {
		t.Errorf("call_2 status=failed 该落 failed 并带原话，拿到 %+v", a)
	}
}

// ReadShard 是**按路径**读一份分片：目录不推、句柄按记录自报（codex 一份 fork 出来的
// rollout 文件名唯一，而记录里的 session_id 会和母会话重名）。
func TestReadShardReadsByPath(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "sessions", "2026", "10", "09", "rollout-x.jsonl")
	writeLines(t, path, cxMeta("2026-10-09T13:44:24.000Z", cxSession))
	fam, ok := harness.Default().Lookup("codex")
	if !ok {
		t.Fatal("口径表里没有 codex 这家")
	}
	s := ReadShard(fam, path)
	if !s.Found {
		t.Fatalf("该读到了：%v", s.Problems)
	}
	if s.Dir != filepath.Dir(path) || s.ID != cxSession {
		t.Errorf("目录该是分片所在那层、句柄该按记录自报，拿到 dir=%q id=%q", s.Dir, s.ID)
	}
	// 这一家每读一份都会追加那三条口径说明（没金额 / 账怎么算 / 缓存读包含在输入里），
	// 那不是「读不到」—— 这里只盯「读不到」这一类的。
	for _, x := range s.Problems {
		if strings.Contains(x, "找不到") || strings.Contains(x, "读不动") {
			t.Errorf("一份好好的记录不该报读不到：%v", s.Problems)
		}
	}
}

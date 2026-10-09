package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/harness"
)

// 这一份测的是「分片不能按项目推目录」那一家的归集口径（接入口径表里的 shard_dir /
// shard_glob）：**全量扫分片，再按记录里的 cwd 认领到项目**。
//
// 形状照 codex 的真记录写（2026-10-10 实测 84 份 rollout）：记录根下按 年/月/日 分层、
// 文件名一律 rollout-*.jsonl、工作目录只写在 session_meta 里。

func codexFamily(t *testing.T) harness.Family {
	t.Helper()
	fam, ok := harness.Default().Lookup("codex")
	if !ok {
		t.Fatal("口径表里没有 codex 这家")
	}
	return fam
}

// writeCodexShard 在 <记录根>/sessions/<年>/<月>/<日>/ 下放一份 rollout 分片。
// file 是文件名里那一段（唯一），sessionID 是记录自报的会话 id（fork 出来的会和母会话重名）。
func writeCodexShard(t *testing.T, home, file, sessionID, cwd string, lines ...string) {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "10", "09")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var body []string
	if cwd != "" {
		body = append(body, `{"timestamp":"2026-10-09T13:00:00.000Z","type":"session_meta","payload":{"session_id":"`+
			sessionID+`","cwd":`+jsonStringTest(cwd)+`}}`)
	}
	body = append(body, lines...)
	if err := os.WriteFile(filepath.Join(dir, "rollout-"+file+".jsonl"),
		[]byte(strings.Join(body, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// jsonStringTest 把一段文字变成 JSON 字符串字面量（造记录用）。
func jsonStringTest(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func cxCallLine(ts, callID, name, args string) string {
	return `{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"function_call","name":"` + name +
		`","call_id":"` + callID + `","arguments":` + jsonStringTest(args) +
		`,"internal_chat_message_metadata_passthrough":{"turn_id":"t1"}}}`
}

func cxDoneLine(ts, callID, status string) string {
	return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"item_completed","turn_id":"t1","item":{"type":"CommandExecution","id":"` +
		callID + `","status":"` + status + `"}}}`
}

func TestCollectGlobalEnumClaimsByCwd(t *testing.T) {
	home := t.TempDir()
	wd := filepath.Join(t.TempDir(), "homes", "alice")
	other := filepath.Join(t.TempDir(), "homes", "bob")

	// 我们的：cwd 对得上配置里的 work_dir。
	writeCodexShard(t, home, "ours", "sess-ours", wd,
		cxCallLine("2026-10-09T13:00:01.000Z", "call_1", "exec_command", `{"cmd":"ls"}`),
		cxDoneLine("2026-10-09T13:00:02.000Z", "call_1", "completed"),
		cxCallLine("2026-10-09T13:00:03.000Z", "call_2", "anc_send_envelope", `{"who":"alice"}`),
		cxDoneLine("2026-10-09T13:00:04.000Z", "call_2", "completed"),
	)
	// 别人的：cwd 是另一个目录 —— 这台机器上不止我们这几个项目，**跳过且不报**。
	writeCodexShard(t, home, "theirs", "sess-theirs", other,
		cxCallLine("2026-10-09T13:00:05.000Z", "call_9", "exec_command", `{"cmd":"pwd"}`),
		cxDoneLine("2026-10-09T13:00:06.000Z", "call_9", "completed"),
	)
	// 认不出归属的：记录里没有 cwd —— 这是**读不到**，必须报出来。
	writeCodexShard(t, home, "nocwd", "sess-nocwd", "",
		cxCallLine("2026-10-09T13:00:07.000Z", "call_8", "exec_command", `{"cmd":"pwd"}`),
	)

	res := Collect(CollectOptions{
		ClaudeHome: home,
		Family:     codexFamily(t),
		Projects:   map[string]string{"demo-alice": wd},
		ActorOf:    func(string) string { return "alice" },
	})
	if res.Shards != 3 {
		t.Errorf("该扫到 3 份分片，拿到 %d", res.Shards)
	}
	if len(res.Records) != 2 {
		t.Fatalf("该只收我们那两份分片里的 2 条，拿到 %d：%+v", len(res.Records), res.Records)
	}
	byTool := map[string]Record{}
	for _, r := range res.Records {
		byTool[r.Tool] = r
	}
	if r := byTool["exec_command"]; r.Actor != "alice" || r.Action != ActionInvoke ||
		r.Result != ResultOK || !strings.HasPrefix(r.ID, "rollout-ours:") {
		t.Errorf("exec_command 那条不对：%+v", r)
	}
	if r := byTool["anc_send_envelope"]; r.Action != ActionWrite || r.Object != "alice" {
		t.Errorf("anc_send_envelope 该是 write / 对象 alice：%+v", r)
	}
	joined := strings.Join(res.Problems, "\n")
	if !strings.Contains(joined, "没有 cwd") {
		t.Errorf("认不出归属的那份要报出来：%v", res.Problems)
	}
	if strings.Contains(joined, "theirs") || strings.Contains(joined, other) {
		t.Errorf("别人的记录不该出现在我们的归集结果里：%v", res.Problems)
	}
	// 别人的那两次调用**不进账**（Calls 只数我们自己的）。
	if res.Calls != 2 {
		t.Errorf("ours 只有 2 次行使、别人的不算，拿到 Calls=%d", res.Calls)
	}
}

// 幂等键是「文件名 + call id」：codex 一份 fork 出来的 rollout 文件名唯一，而记录里的
// session_id 会和母会话**重名**（fork 把前史一起带了进来）—— 拿 session_id 当键会撞。
func TestCollectGlobalIDUsesFileNameNotSessionID(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	// 两份分片共用同一个 session_id（fork 出来的那两份就是这样）。
	writeCodexShard(t, home, "fork-a", "same-session", wd,
		cxCallLine("2026-10-09T13:00:01.000Z", "call_1", "exec_command", `{"cmd":"ls"}`))
	writeCodexShard(t, home, "fork-b", "same-session", wd,
		cxCallLine("2026-10-09T13:00:02.000Z", "call_1", "exec_command", `{"cmd":"ls"}`))
	res := Collect(CollectOptions{
		ClaudeHome: home,
		Family:     codexFamily(t),
		Projects:   map[string]string{"demo-alice": wd},
	})
	if len(res.Records) != 2 {
		t.Fatalf("两份分片各一条，拿到 %d", len(res.Records))
	}
	if res.Records[0].ID == res.Records[1].ID {
		t.Errorf("两份分片的 id 不能一样：%s", res.Records[0].ID)
	}
}

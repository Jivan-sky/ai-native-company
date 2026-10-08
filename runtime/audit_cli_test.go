package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/audit"
)

// 验收判据 1 + 2 走完一圈：造一次跨域读取、造一次被拒绝的行使，
// 流水里都查得到「谁 / 何时 / 对谁 / 类别 / 结果」，且拒绝那条结果字段写的是 denied。
func TestAuditCLIAddThenLogAnswersFive(t *testing.T) {
	vault := copyFixture(t, "domains") // 夹具域表：trade → projects，logistics → shipments；alice 在 trade

	read := []string{vault, "--actor", "alice", "--action", "read", "--result", "ok",
		"--object", filepath.Join(vault, "shipments", "b.md"), "--why", "例行巡检"}
	if code := quiet(t, func() int { return cmdAuditAdd(read) }); code != 0 {
		t.Fatalf("记一条跨域读取应当 exit 0，实际 %d", code)
	}
	denied := []string{vault, "--actor", "alice", "--action", "write", "--result", "denied",
		"--object", filepath.Join(vault, "shipments", "b.md"),
		"--why", "Permission to use Write has been denied"}
	if code := quiet(t, func() int { return cmdAuditAdd(denied) }); code != 0 {
		t.Fatalf("记一条被拒的行使应当 exit 0，实际 %d", code)
	}

	out, code := captureStdout(t, func() int { return cmdAuditLog([]string{vault}) })
	if code != 0 {
		t.Fatalf("log 应当 exit 0，实际 %d\n%s", code, out)
	}
	for _, want := range []string{"alice", "read", "write", "ok", "denied", "logistics", "跨域"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里没有 %q：\n%s", want, out)
		}
	}
	if !strings.Contains(out, "域内 2") || !strings.Contains(out, "其中跨域 2") {
		t.Errorf("落点汇总不对：\n%s", out)
	}
}

func TestAuditCLIAddRejectsIncomplete(t *testing.T) {
	vault := copyFixture(t, "domains")
	cases := [][]string{
		{vault, "--action", "read", "--result", "ok"},   // 缺 actor
		{vault, "--actor", "alice", "--result", "ok"},   // 缺 action
		{vault, "--actor", "alice", "--action", "read"}, // 缺 result
		{vault, "--actor", "alice", "--action", "read", "--result", "ok",
			"--at", "昨天"}, // at 不是 RFC3339
	}
	for i, args := range cases {
		if code := quiet(t, func() int { return cmdAuditAdd(args) }); code != 1 {
			t.Errorf("第 %d 组缺东西却 exit %d，期望 1", i, code)
		}
	}
}

func TestAuditCLILogFilters(t *testing.T) {
	vault := copyFixture(t, "domains")
	quiet(t, func() int {
		return cmdAuditAdd([]string{vault, "--actor", "alice", "--action", "read", "--result", "ok",
			"--object", filepath.Join(vault, "projects", "a.md")})
	})
	quiet(t, func() int {
		return cmdAuditAdd([]string{vault, "--actor", "bob", "--action", "write", "--result", "denied",
			"--object", filepath.Join(vault, "shipments", "b.md")})
	})
	out, _ := captureStdout(t, func() int { return cmdAuditLog([]string{vault, "--result", "denied"}) })
	if !strings.Contains(out, "bob") || strings.Contains(out, "alice") {
		t.Errorf("--result denied 该只留 bob：\n%s", out)
	}
	// 只看跨域：bob 在 logistics 域、目标是 shipments（自己域）→ 不跨，所以这条筛不出来。
	out, _ = captureStdout(t, func() int { return cmdAuditLog([]string{vault, "--cross"}) })
	if !strings.Contains(out, "筛出 0 条") {
		t.Errorf("同域行使不该被 --cross 选出来：\n%s", out)
	}
}

// 归集：真读一份（假的）harness 原生记录，落盘一次，再跑一次应当一条不写（幂等）。
func TestAuditCLICollectWritesOnce(t *testing.T) {
	vault := copyFixture(t, "domains")
	home := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "homes", "alice")
	dir := filepath.Join(home, "projects", slugOf(workDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"assistant","timestamp":"2026-10-08T10:00:00.000Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"` +
		filepath.ToSlash(filepath.Join(vault, "shipments", "b.md")) + `"}}]}}
{"type":"user","timestamp":"2026-10-08T10:00:01.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}
`
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[[projects]]\nname = \"demo-alice\"\nwork_dir = "+tomlQ(workDir)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	args := []string{vault, "--config", cfg, "--claude-home", home, "--write"}
	out, code := captureStdout(t, func() int { return cmdAuditCollect(args) })
	if code != 0 {
		t.Fatalf("归集应当 exit 0，实际 %d\n%s", code, out)
	}
	if !strings.Contains(out, "新记录    1 条") || !strings.Contains(out, "已写 1 条") {
		t.Errorf("第一次该写 1 条：\n%s", out)
	}
	out2, _ := captureStdout(t, func() int { return cmdAuditCollect(args) })
	if !strings.Contains(out2, "新记录    0 条") {
		t.Errorf("第二次该一条不写（幂等）：\n%s", out2)
	}
	if n := auditCount(t, vault); n != 1 {
		t.Errorf("流水里该只有 1 条，实际 %d", n)
	}
	// 归集出来的那条：谁是成员名（不是 project 名）、对谁是 logistics 域、跨域。
	doc, err := audit.Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	r := doc.Records[0]
	if r.Actor != "alice" {
		t.Errorf("actor 该解成成员名 alice，得到 %q", r.Actor)
	}
	if r.Source != "collect:claude" || r.Tool != "Read" {
		t.Errorf("来源与工具没记下来：%+v", r)
	}
}

func TestAuditCLIToolsPrintsTable(t *testing.T) {
	out, code := captureStdout(t, func() int { return cmdAuditTools(nil) })
	if code != 0 {
		t.Fatalf("tools 应当 exit 0，实际 %d", code)
	}
	for _, want := range []string{"Read", "file_path", "Bash", "目标抽不出来"} {
		if !strings.Contains(out, want) {
			t.Errorf("工具表里没有 %q：\n%s", want, out)
		}
	}
}

func TestAuditCLILogJSONContract(t *testing.T) {
	vault := copyFixture(t, "domains")
	quiet(t, func() int {
		return cmdAuditAdd([]string{vault, "--actor", "alice", "--action", "read", "--result", "ok",
			"--object", filepath.Join(vault, "projects", "a.md")})
	})
	out, code := captureStdout(t, func() int { return cmdAuditLog([]string{vault, "--json"}) })
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var got struct {
		Schema  string            `json:"schema"`
		Total   int               `json:"total"`
		Records []audit.EntryView `json:"records"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("不是合法 JSON：%v\n%s", err, out)
	}
	if got.Schema != auditSchema || got.Total != 1 || len(got.Records) != 1 {
		t.Errorf("契约不对：schema=%q total=%d n=%d", got.Schema, got.Total, len(got.Records))
	}
}

// slugOf / tomlQ / auditCount —— 测试用的三个小工具。
func slugOf(workDir string) string {
	var b strings.Builder
	for _, r := range workDir {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func tomlQ(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

func auditCount(t *testing.T, vault string) int {
	t.Helper()
	doc, err := audit.Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	return doc.Len()
}

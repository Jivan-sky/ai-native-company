package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anc/internal/probe"
)

// writeProbeFixture 造一个最小可探的现场：config 声明 + data 目录。
func writeProbeFixture(t *testing.T, projects []string, withSocket bool) (vault, data string) {
	t.Helper()
	root := t.TempDir()
	vault = filepath.Join(root, "vault")
	if err := os.MkdirAll(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, p := range projects {
		b.WriteString("\n[[projects]]\nname = " + `"` + p + `"` + "\n")
	}
	cfg := filepath.Join(root, "gateway", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	data = filepath.Join(root, "data")
	if err := os.MkdirAll(filepath.Join(data, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if withSocket {
		listenSock(t, data)
	}
	return vault, data
}

// listenSock 真起一个 unix socket，让「拨得通」这条走通（不然测的是 down 分支）。
func listenSock(t *testing.T, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(data, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(data, "run", "api.sock"))
	if err != nil {
		t.Skipf("这个平台起不了 unix socket：%v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
}

// writeSession 写一个会话文件。history 是 (role, 相对 now 的偏移) 序列；
// agentID 空 = agent 从没起来过。
func writeSession(t *testing.T, data, file, agentID string, now time.Time, history ...string) {
	t.Helper()
	type msg struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		Timestamp string `json:"timestamp"`
	}
	msgs := make([]msg, 0, len(history)/2)
	for i := 0; i+1 < len(history); i += 2 {
		d, err := time.ParseDuration(history[i+1])
		if err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, msg{history[i], "…", now.Add(d).Format(time.RFC3339Nano)})
	}
	one := map[string]any{
		"sessions": map[string]any{
			"s1": map[string]any{
				"id": "s1", "name": "default",
				"agent_session_id": agentID,
				"history":          msgs,
			},
		},
	}
	b, err := json.Marshal(one)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "sessions", file), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// 今天真踩的坑：消息收得到、session 起得来，但 agent 一次都没起来
// （agent_session_id 为空）。这不是「还没聊过」，是「连得上却回不了话」。
func TestProbeAgentNeverStartedIsFail(t *testing.T) {
	vault, data := writeProbeFixture(t, []string{"a"}, true)
	writeSession(t, data, "a_00000001.json", "", time.Now(), "user", "-5m")

	out := runProbeJSON(t, vault)
	if out.Bots[0].State != probe.StateFail {
		t.Fatalf("agent 从没起来过应当报红，实际 %+v", out.Bots[0])
	}
	if !strings.Contains(out.Bots[0].Why, "从没起来过") {
		t.Fatalf("红档要说清原因，实际 %q", out.Bots[0].Why)
	}
}

// socket 文件在、但拨不通 —— 进程崩了留下残留文件。
// 这是「看文件在不在」这种判活的经典假绿，探针必须抓得住。
// 变异验证：把 probe.DialUnix 退化成 os.Stat / 忽略 dial 错误，这个用例立刻变红。
func TestProbeStaleSocketIsDown(t *testing.T) {
	vault, data := writeProbeFixture(t, []string{"a"}, false)
	if err := os.MkdirAll(filepath.Join(data, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "run", "api.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out := runProbeJSON(t, vault)
	if out.Gateway != "down" {
		t.Fatalf("残留 socket 文件应当判 down，实际 %q（%s）", out.Gateway, out.GatewayWhy)
	}
	if out.Bots[0].State != probe.StateFail {
		t.Fatalf("gateway 不在跑时 bot 必然回不了话，应当报红，实际 %+v", out.Bots[0])
	}
}

// 真回过话、且在 --stale 窗口内 = 绿，退出码 0。
func TestProbeHealthyIsGreen(t *testing.T) {
	vault, data := writeProbeFixture(t, []string{"a"}, true)
	now := time.Now()
	writeSession(t, data, "a_00000001.json", "sess-1", now, "user", "-2m", "assistant", "-1m")

	out := runProbeJSON(t, vault)
	if out.Bots[0].State != probe.StateOK {
		t.Fatalf("刚回过话应当报绿，实际 %+v", out.Bots[0])
	}
	if code := quiet(t, func() int { return cmdProbe([]string{vault}) }); code != 0 {
		t.Fatalf("全绿时退出码应当 0，实际 %d", code)
	}
}

// 配置里已经删掉的 bot，磁盘上还留着它的会话 → 报出来（不是运行态问题，但不许静默）。
func TestProbeOrphanSessionsReported(t *testing.T) {
	vault, data := writeProbeFixture(t, []string{"a"}, true)
	now := time.Now()
	writeSession(t, data, "a_00000001.json", "sess-1", now, "user", "-2m", "assistant", "-1m")
	writeSession(t, data, "gone_00000002.json", "sess-2", now, "user", "-3m", "assistant", "-2m")

	out := runProbeJSON(t, vault)
	if joined := strings.Join(out.Extras, "\n"); !strings.Contains(joined, "gone") {
		t.Fatalf("孤儿会话应当报出来，实际 %v", out.Extras)
	}
	if code := quiet(t, func() int { return cmdProbe([]string{vault}) }); code != 1 {
		t.Fatalf("有残留时退出码应当 1，实际 %d", code)
	}
}

// 报红要能一眼看出「该交给谁」—— 来源是真相源的 company.admins，
// 解成「人名（岗位）」而不是甩内部标识符。
func TestProbeNamesHandlerFromTruthSource(t *testing.T) {
	vault := orgFixtureVault(t)
	data := filepath.Join(filepath.Dir(vault), "data")
	if err := os.MkdirAll(filepath.Join(data, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	listenSock(t, data)
	writeSession(t, data, "a_00000001.json", "sess-1", time.Now(), "user", "-2m", "assistant", "-1m")

	out := runProbeJSON(t, vault)
	if len(out.Handlers) != 1 || !strings.Contains(out.Handlers[0], "Alice") {
		t.Fatalf("该交给谁应当来自 company.admins 且带人名，实际 %v（%s）", out.Handlers, out.HandlerWhy)
	}
}

// 真相源读不动时**不猜**责任人 —— 一份红报告配上猜错的人更糟。
func TestProbeNoHandlerWhenTruthSourceUnreadable(t *testing.T) {
	vault, data := writeProbeFixture(t, []string{"a"}, true)
	writeSession(t, data, "a_00000001.json", "sess-1", time.Now(), "user", "-2m", "assistant", "-1m")

	out := runProbeJSON(t, vault)
	if len(out.Handlers) != 0 || out.HandlerWhy == "" {
		t.Fatalf("读不动真相源时应当明说没算出来，实际 %v / %q", out.Handlers, out.HandlerWhy)
	}
}

// orgFixtureVault 把 testdata 的 one 组织拷进临时目录，并补一份 gateway config。
func orgFixtureVault(t *testing.T) string {
	t.Helper()
	src := filepath.Join("testdata", "orgs", "one")
	dst := filepath.Join(t.TempDir(), "vault")
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(filepath.Dir(dst), "gateway", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("\n[[projects]]\nname = \"a\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dst
}

// runProbeJSON 跑一次探针并解出 JSON。
func runProbeJSON(t *testing.T, vault string) probe.Report {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "out-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdout
	os.Stdout = f
	code := cmdProbe([]string{vault, "--json"})
	os.Stdout = old
	if code != 0 && code != 1 {
		t.Fatalf("探针退出码 %d（期望 0 或 1）", code)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var rep probe.Report
	if err := json.NewDecoder(f).Decode(&rep); err != nil {
		t.Fatalf("解不出 JSON：%v", err)
	}
	return rep
}

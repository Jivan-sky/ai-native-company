package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		if err := os.MkdirAll(filepath.Join(data, "run"), 0o700); err != nil {
			t.Fatal(err)
		}
		// 真起一个 unix socket，让「拨得通」这条走通（不然测的是 down 分支）。
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
	return vault, data
}

// writeSession 写一个会话文件。history 是 (role, 时刻) 序列；agentID 空 = agent 从没起来过。
func writeSession(t *testing.T, data, file, agentID string, now time.Time, history ...string) {
	t.Helper()
	type msg struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		Timestamp string `json:"timestamp"`
	}
	msgs := make([]msg, 0, len(history)/2)
	for i := 0; i+1 < len(history); i += 2 {
		ts, err := time.ParseDuration(history[i+1])
		if err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, msg{history[i], "…", now.Add(ts).Format(time.RFC3339Nano)})
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

// 「没消息 = 没事」是反模式：一个从没人跟它说过话的 bot，不许报绿。
func TestProbeNoSessionsIsNotGreen(t *testing.T) {
	vault, _ := writeProbeFixture(t, []string{"a"}, true)
	out := runProbeJSON(t, vault)
	if len(out.Findings) != 1 || out.Findings[0].State == "ok" {
		t.Fatalf("没有会话记录时不许报绿，实际 %+v", out.Findings)
	}
}

// 今天真踩的坑：消息收得到、session 起得来，但 agent 一次都没起来
// （agent_session_id 为空）。这不是「还没聊过」，是「连得上却回不了话」。
func TestProbeAgentNeverStartedIsFail(t *testing.T) {
	vault, data := writeProbeFixture(t, []string{"a"}, true)
	now := time.Now()
	writeSession(t, data, "a_00000001.json", "", now, "user", "-5m")

	out := runProbeJSON(t, vault)
	if out.Findings[0].State != "fail" {
		t.Fatalf("agent 从没起来过应当报红，实际 %+v", out.Findings[0])
	}
	if !strings.Contains(out.Findings[0].Why, "agent 从没起来过") && !strings.Contains(out.Findings[0].Why, "从没起来过") {
		t.Fatalf("红档要说清原因，实际 %q", out.Findings[0].Why)
	}
}

// socket 文件在、但拨不通 —— 进程崩了留下残留文件。
// 这是「看文件在不在」这种判活的经典假绿，探针必须抓得住。
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
	if out.Findings[0].State != "fail" {
		t.Fatalf("gateway 不在跑时 bot 必然回不了话，应当报红，实际 %+v", out.Findings[0])
	}
}

// 真回过话、且在 --stale 窗口内 = 绿。
func TestProbeHealthyIsGreen(t *testing.T) {
	vault, data := writeProbeFixture(t, []string{"a"}, true)
	now := time.Now()
	writeSession(t, data, "a_00000001.json", "sess-1", now, "user", "-2m", "assistant", "-1m")

	out := runProbeJSON(t, vault)
	if out.Findings[0].State != "ok" {
		t.Fatalf("刚回过话应当报绿，实际 %+v", out.Findings[0])
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
	joined := strings.Join(out.Extras, "\n")
	if !strings.Contains(joined, "gone") {
		t.Fatalf("孤儿会话应当报出来，实际 %v", out.Extras)
	}
	if code := quiet(t, func() int { return cmdProbe([]string{vault}) }); code != 1 {
		t.Fatalf("有残留时退出码应当 1，实际 %d", code)
	}
}

// judgeProject 的档位判据（纯函数，定时定刻，不依赖机器时钟）。
func TestJudgeProjectLevels(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	mk := func(agentID string, hist ...string) []string {
		data := t.TempDir()
		if err := os.MkdirAll(filepath.Join(data, "sessions"), 0o700); err != nil {
			t.Fatal(err)
		}
		writeSession(t, data, "p_x.json", agentID, now, hist...)
		fs, _ := filepath.Glob(filepath.Join(data, "sessions", "*.json"))
		return fs
	}
	cases := []struct {
		name  string
		files func() []string
		want  string
	}{
		{"刚回过话", func() []string { return mk("s1", "user", "-2m", "assistant", "-1m") }, "ok"},
		{"最后一轮刚发出：在处理中，不是红", func() []string { return mk("s1", "user", "-1m") }, "warn"},
		{"最后一轮超过 stall 没回：卡住", func() []string { return mk("s1", "user", "-40m") }, "fail"},
		{"久无成功交互：不报绿", func() []string { return mk("s1", "user", "-50h", "assistant", "-49h") }, "warn"},
		{"agent 从没起来过", func() []string { return mk("", "user", "-5m") }, "fail"},
		{"没有会话文件", func() []string { return nil }, "warn"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := judgeProject("p", c.files(), now, 24*time.Hour, 10*time.Minute)
			if got.State != c.want {
				t.Fatalf("档位 %s，期望 %s（%s）", got.State, c.want, got.Why)
			}
		})
	}
}

// runProbeJSON 跑一次探针并解出 JSON。
func runProbeJSON(t *testing.T, vault string) probeReport {
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
	var rep probeReport
	if err := json.NewDecoder(f).Decode(&rep); err != nil {
		t.Fatalf("解不出 JSON：%v", err)
	}
	return rep
}

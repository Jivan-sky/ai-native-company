package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anc/internal/probe"
)

// 端到端：真相源里给一个成员声明 unwired: true → 探针落灰，且**不挡绿**（退出码 0）。
// 这就是 W1 门那条「探针全绿」的口径：绿的意思是「**已接凭据的**都绿了」。
func TestProbeUnwiredMemberIsGrayAndDoesNotBlockGreen(t *testing.T) {
	vault, data := writeUnwiredFixture(t)
	// 只有 alice 有真实回复；devbot 一条会话都没有（它就是那个「没接凭据」的）。
	writeSession(t, data, "demo-alice_00000001.json", "sess-1", time.Now(), "user", "-2m", "assistant", "-1m")

	out := runProbeJSON(t, vault)
	byProject := map[string]probe.Finding{}
	for _, f := range out.Bots {
		byProject[f.Project] = f
	}
	if got := byProject["demo-alice"].State; got != probe.StateOK {
		t.Fatalf("alice 刚回过话应当报绿，实际 %q（%s）", got, byProject["demo-alice"].Why)
	}
	dev := byProject["demo-devbot"]
	if dev.State != probe.StateUnwired {
		t.Fatalf("声明 unwired 的成员应当落灰，实际 %q（%+v）", dev.State, out.Bots)
	}
	if !strings.Contains(dev.Why, "未接") {
		t.Fatalf("灰档要说清「为什么不算事故」，实际 %q", dev.Why)
	}
	if len(out.Extras) != 0 {
		t.Fatalf("这里没有残留，实际 %v", out.Extras)
	}
	if code := quiet(t, func() int { return cmdProbe([]string{vault}) }); code != 0 {
		t.Fatalf("已接凭据的全绿时退出码应当 0（灰不挡绿），实际 %d", code)
	}
}

// writeUnwiredFixture 把 testdata 的 one 组织拷进临时目录，
// 给 devbot 的 persona 加 unwired: true（**声明**，不是拿 app_id 长相猜的），
// 并补一份声明了 demo-alice / demo-devbot 两个 project 的 gateway config。
//
// 根目录用 os.MkdirTemp 的短名，不用 t.TempDir()：Windows 上 AF_UNIX 的 sun_path 上限
// 108 字节，而 t.TempDir() 会带上测试函数名 —— 一顶爆，listenSock 就变成 skip，
// 用例看起来是绿的，其实什么都没验。
func writeUnwiredFixture(t *testing.T) (vault, data string) {
	t.Helper()
	root, err := os.MkdirTemp("", "anc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	if err := os.CopyFS(filepath.Join(root, "vault"), os.DirFS(filepath.Join("testdata", "orgs", "one"))); err != nil {
		t.Fatal(err)
	}
	vault = filepath.Join(root, "vault")
	persona := filepath.Join(vault, "members", "devbot", "persona.md")
	b, err := os.ReadFile(persona)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(b), "disabled: false", "disabled: false\nunwired: true", 1)
	if patched == string(b) {
		t.Fatalf("fixture 的 devbot persona 里没找到锚点，补丁没打上")
	}
	if err := os.WriteFile(persona, []byte(patched), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "gateway", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("\n[[projects]]\nname = \"demo-alice\"\n\n[[projects]]\nname = \"demo-devbot\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data = filepath.Join(root, "data")
	if err := os.MkdirAll(filepath.Join(data, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	listenSock(t, data)
	return vault, data
}

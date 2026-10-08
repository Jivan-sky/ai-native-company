package probe

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortDir 给 unix socket 一个**短**根目录。
// Windows 上 AF_UNIX 的 sun_path 上限是 108 字节，而 t.TempDir() 会带上测试函数名，
// 长一点的用例名直接把它顶爆（bind: invalid argument）—— 于是用例悄悄变成 skip，
// 看起来是绿的，其实什么都没验。所以这里不拿测试名当路径。
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "anc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// listenSock 真起一个 unix socket，让 Run 的「网关在跑」这条走通（不然测的是 down 分支）。
func listenSock(t *testing.T, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(data, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(data, "run", "api.sock"))
	if err != nil {
		t.Fatalf("起不了 unix socket：%v（路径长度 %d）", err, len(filepath.Join(data, "run", "api.sock")))
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

// 第四档（灰）：声明了「还没接平台凭据」的 project 单列灰 —— 既不判绿也不判黄；
// 而且它自己的会话文件不该跑进「残留」（残留是「删过 bot 却留着记录」的签名，不是同一回事）。
func TestRunUnwiredIsItsOwnLevel(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	data := filepath.Join(shortDir(t), "data")
	listenSock(t, data)
	sess := filepath.Join(data, "sessions")
	writeSessions(t, sess, "a_x.json", "s1", now, "user", "-2m", "assistant", "-1m")
	writeSessions(t, sess, "b_x.json", "s1", now, "user", "-30m")
	writeSessions(t, sess, "c_x.json", "s1", now, "user", "-1m")

	rep, err := Run(Options{DataDir: data, Now: now, Projects: []string{"a", "b", "c"}, Unwired: []string{"b"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]State{}
	for _, f := range rep.Bots {
		got[f.Project] = f.State
	}
	if got["b"] != StateUnwired {
		t.Fatalf("声明未接凭据的应当单列灰，实际 %q（%+v）", got["b"], rep.Bots)
	}
	if got["a"] != StateOK || got["c"] != StateWarn {
		t.Fatalf("灰档不许影响别人：a 应绿、c 应黄，实际 %v", got)
	}
	if len(rep.Extras) != 0 {
		t.Fatalf("灰档自己的会话文件不是残留，实际 %v", rep.Extras)
	}
	if ok, warn, fail, unwired := rep.Tally(); ok != 1 || warn != 1 || fail != 0 || unwired != 1 {
		t.Fatalf("计数应 1 绿 / 1 黄 / 0 红 / 1 灰，实际 %d/%d/%d/%d", ok, warn, fail, unwired)
	}
}

// 灰的判断在「网关挂没挂」之前 —— 没接凭据的 bot 本来就不该回话，
// 拿它去凑红是噪声。网关不在跑时，它照样是灰。
func TestRunUnwiredStaysGrayWhenGatewayDown(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	data := filepath.Join(shortDir(t), "data")
	if err := os.MkdirAll(filepath.Join(data, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(Options{DataDir: data, Now: now, Projects: []string{"a", "b"}, Unwired: []string{"b"}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Gateway != "down" {
		t.Fatalf("这个现场本意是网关不在跑，实际 %q", rep.Gateway)
	}
	for _, f := range rep.Bots {
		want := StateFail
		if f.Project == "b" {
			want = StateUnwired
		}
		if f.State != want {
			t.Fatalf("%s 期望 %s，实际 %s（%s）", f.Project, want, f.State, f.Why)
		}
	}
}

// 灰不挡绿：门的口径是「**已接凭据的**都绿了」，不是「所有 bot 都绿了」。
// 但灰必须在报告里数得出来 —— 否则「3/3 绿」会把「没接凭据」糊进绿里。
func TestAllGreenIgnoresUnwiredButTallyShowsIt(t *testing.T) {
	rep := Report{Gateway: "up", Bots: []Finding{{"a", StateOK, ""}, {"b", StateUnwired, ""}}}
	if !rep.AllGreen() {
		t.Fatal("灰档不该挡绿")
	}
	if ok, warn, fail, unwired := rep.Tally(); ok != 1 || warn != 0 || fail != 0 || unwired != 1 {
		t.Fatalf("计数应 1 绿 / 0 黄 / 0 红 / 1 灰，实际 %d/%d/%d/%d", ok, warn, fail, unwired)
	}
}

// 但**空集不许判绿**：一个绿的都没有时，「已接凭据的都绿了」是空真 ——
// 什么都不验证就报绿，是最纯的那种假绿（2026-10-09 真机实测逼出：三个 bot 全声明 unwired 时
// 第一版 `AllGreen()` 照旧返回 true）。同「没消息 = 没事」是反模式。
func TestAllGreenRefusesVacuousGreen(t *testing.T) {
	for _, rep := range []Report{
		{Gateway: "up", Bots: []Finding{}},
		{Gateway: "up", Bots: []Finding{{"a", StateUnwired, ""}}},
		{Gateway: "up", Bots: []Finding{{"a", StateUnwired, ""}, {"b", StateUnwired, ""}}},
	} {
		if rep.AllGreen() {
			t.Fatalf("一个绿的都没有却判绿：%+v", rep)
		}
	}
}

package notify

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anc/internal/probe"
)

// 判据 1：没喊过 + 红 → 新告警，并落账。
func TestDecideAlertOnFirstRed(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	rep := probe.Report{Gateway: "up", Bots: []probe.Finding{
		{Project: "demo-alice", State: probe.StateFail, Why: "起不来"},
	}}
	p := Decide(Cursor{Schema: Schema, Keys: map[string]Mark{}}, rep, DefaultCooldown, now)

	if len(p.Actions) != 1 {
		t.Fatalf("想要 1 条动作，拿到 %d：%+v", len(p.Actions), p.Actions)
	}
	a := p.Actions[0]
	if a.Key != "demo-alice" || a.Kind != KindAlert {
		t.Errorf("想要 demo-alice/alert，拿到 %s/%s", a.Key, a.Kind)
	}
	if len(p.Silent) != 0 {
		t.Errorf("不该有被压下的：%+v", p.Silent)
	}
	if m, ok := p.Next.Keys["demo-alice"]; !ok || !m.At.Equal(now) {
		t.Errorf("账没落对：%+v", p.Next.Keys)
	}
}

// 判据 2：红着但还在冷却期内 → 一条都不发，但要能在 Silent 里说出来。
func TestDecideSilentWithinCooldown(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	cur := Cursor{Schema: Schema, Keys: map[string]Mark{
		"demo-alice": {Level: string(probe.StateFail), At: now.Add(-10 * time.Minute)},
	}}
	rep := probe.Report{Gateway: "up", Bots: []probe.Finding{
		{Project: "demo-alice", State: probe.StateFail, Why: "还是起不来"},
	}}
	p := Decide(cur, rep, DefaultCooldown, now)

	if len(p.Actions) != 0 {
		t.Fatalf("冷却期内不该发：%+v", p.Actions)
	}
	if len(p.Silent) != 1 || p.Silent[0].Key != "demo-alice" {
		t.Fatalf("该说清「压下了一条」，拿到 %+v", p.Silent)
	}
	if !p.Next.Keys["demo-alice"].At.Equal(now.Add(-10 * time.Minute)) {
		t.Errorf("冷却期内不该刷新时间戳：%+v", p.Next.Keys["demo-alice"])
	}
}

// 判据 2 的另一半：红过冷却期 → 提醒一次（否则「没消息 = 没事」）。
func TestDecideReminderAfterCooldown(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	cur := Cursor{Schema: Schema, Keys: map[string]Mark{
		"demo-alice": {Level: string(probe.StateFail), At: now.Add(-31 * time.Minute)},
	}}
	rep := probe.Report{Gateway: "up", Bots: []probe.Finding{
		{Project: "demo-alice", State: probe.StateFail, Why: "还是起不来"},
	}}
	p := Decide(cur, rep, DefaultCooldown, now)

	if len(p.Actions) != 1 || p.Actions[0].Kind != KindReminder {
		t.Fatalf("想要 1 条 reminder，拿到 %+v", p.Actions)
	}
	if !p.Next.Keys["demo-alice"].At.Equal(now) {
		t.Errorf("提醒后该刷新时间戳：%+v", p.Next.Keys["demo-alice"])
	}
}

// 红转绿 → 一条「已恢复」，并把账清掉。
func TestDecideRecoveryClearsDebt(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	cur := Cursor{Schema: Schema, Keys: map[string]Mark{
		"demo-alice": {Level: string(probe.StateFail), At: now.Add(-2 * time.Hour)},
	}}
	rep := probe.Report{Gateway: "up", Bots: []probe.Finding{
		{Project: "demo-alice", State: probe.StateOK, Why: "回得了话"},
	}}
	p := Decide(cur, rep, DefaultCooldown, now)

	if len(p.Actions) != 1 || p.Actions[0].Kind != KindRecovery {
		t.Fatalf("想要 1 条 recovery，拿到 %+v", p.Actions)
	}
	if _, still := p.Next.Keys["demo-alice"]; still {
		t.Errorf("恢复后账要清掉：%+v", p.Next.Keys)
	}
}

// 没喊过的账转绿 → 不欠谁一声，别拿「已恢复」去吵人。
func TestDecideNoRecoveryForNeverAlerted(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	rep := probe.Report{Gateway: "up", Bots: []probe.Finding{
		{Project: "demo-alice", State: probe.StateOK, Why: "回得了话"},
	}}
	p := Decide(Cursor{Schema: Schema, Keys: map[string]Mark{}}, rep, DefaultCooldown, now)

	if len(p.Actions) != 0 || len(p.Silent) != 0 {
		t.Fatalf("没喊过的绿不该出声：%+v / %+v", p.Actions, p.Silent)
	}
	if len(p.Next.Keys) != 0 {
		t.Errorf("不该长出账：%+v", p.Next.Keys)
	}
}

// 只看红：黄档（运行中 / 没观测到）不许推给人工。
func TestDecideIgnoresWarn(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	rep := probe.Report{Gateway: "up", Bots: []probe.Finding{
		{Project: "demo-alice", State: probe.StateWarn, Why: "最后一轮正在处理中"},
	}}
	p := Decide(Cursor{Schema: Schema, Keys: map[string]Mark{}}, rep, DefaultCooldown, now)

	if len(p.Actions) != 0 || len(p.Silent) != 0 {
		t.Fatalf("黄档不该出声：%+v / %+v", p.Actions, p.Silent)
	}
}

// 网关挂了只喊网关一条：底下每个 bot 判红是同一根因，逐个喊就是刷屏，
// 而且此时不去评估 bot（它们的账原样留着，等网关恢复再说）。
func TestDecideGatewayDownOnlyOneAction(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Hour)
	cur := Cursor{Schema: Schema, Keys: map[string]Mark{
		"demo-alice": {Level: string(probe.StateFail), At: old},
	}}
	rep := probe.Report{Gateway: "down", GatewayWhy: "socket 拨不通", Bots: []probe.Finding{
		{Project: "demo-alice", State: probe.StateFail, Why: "gateway 没在跑"},
		{Project: "demo-bob", State: probe.StateFail, Why: "gateway 没在跑"},
	}}
	p := Decide(cur, rep, DefaultCooldown, now)

	if len(p.Actions) != 1 {
		t.Fatalf("网关挂只该喊 1 条，拿到 %d：%+v", len(p.Actions), p.Actions)
	}
	if p.Actions[0].Key != KeyGateway || p.Actions[0].Kind != KindAlert {
		t.Errorf("想要 gateway/alert，拿到 %+v", p.Actions[0])
	}
	if _, ok := p.Next.Keys["demo-bob"]; ok {
		t.Errorf("网关挂着不该给 bot 记账：%+v", p.Next.Keys)
	}
	if !p.Next.Keys["demo-alice"].At.Equal(old) {
		t.Errorf("网关挂着时 bot 的账该原样留着：%+v", p.Next.Keys["demo-alice"])
	}
}

// 网关恢复了 → 补一条「已恢复」并把网关的账清掉。
func TestDecideGatewayRecovery(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	cur := Cursor{Schema: Schema, Keys: map[string]Mark{
		KeyGateway: {Level: string(probe.StateFail), At: now.Add(-time.Hour)},
	}}
	rep := probe.Report{Gateway: "up", Bots: []probe.Finding{
		{Project: "demo-alice", State: probe.StateOK, Why: "回得了话"},
	}}
	p := Decide(cur, rep, DefaultCooldown, now)

	if len(p.Actions) != 1 || p.Actions[0].Key != KeyGateway || p.Actions[0].Kind != KindRecovery {
		t.Fatalf("想要 gateway/recovery，拿到 %+v", p.Actions)
	}
	if len(p.Next.Keys) != 0 {
		t.Errorf("恢复后账要清掉：%+v", p.Next.Keys)
	}
}

// 动作顺序按项目名排：diff 稳定，人看着也不跳。
func TestDecideOrderIsStable(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	rep := probe.Report{Gateway: "up", Bots: []probe.Finding{
		{Project: "demo-carol", State: probe.StateFail},
		{Project: "demo-alice", State: probe.StateFail},
		{Project: "demo-bob", State: probe.StateFail},
	}}
	p := Decide(Cursor{Schema: Schema, Keys: map[string]Mark{}}, rep, DefaultCooldown, now)

	got := []string{p.Actions[0].Key, p.Actions[1].Key, p.Actions[2].Key}
	want := []string{"demo-alice", "demo-bob", "demo-carol"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("顺序不对：拿到 %v，想要 %v", got, want)
		}
	}
}

// cooldown <= 0 时退回出厂值，而不是变成「每次都喊」。
func TestDecideZeroCooldownFallsBack(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	cur := Cursor{Schema: Schema, Keys: map[string]Mark{
		"demo-alice": {Level: string(probe.StateFail), At: now.Add(-1 * time.Minute)},
	}}
	rep := probe.Report{Gateway: "up", Bots: []probe.Finding{
		{Project: "demo-alice", State: probe.StateFail},
	}}
	if p := Decide(cur, rep, 0, now); len(p.Actions) != 0 {
		t.Fatalf("0 冷却该退回出厂 30m，却发了：%+v", p.Actions)
	}
}

// 游标不存在 = 空游标（第一次跑本来就没有）。
func TestLoadCursorMissingIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "notify.json")
	c, err := LoadCursor(path)
	if err != nil {
		t.Fatalf("不存在不该报错：%v", err)
	}
	if c.Schema != Schema || len(c.Keys) != 0 {
		t.Errorf("想要空游标，拿到 %+v", c)
	}
}

// 读得动却解析不了 → 报错。静默当空会把「喊过了」当成「没喊过」，反复吵人。
func TestLoadCursorBadJSONErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.json")
	if err := os.WriteFile(path, []byte("{这不是 JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCursor(path); err == nil {
		t.Fatal("坏游标必须报错，不许静默当空")
	}
}

// Save → LoadCursor 往返，且写出来的目录权限/结构能读回。
func TestCursorSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "notify.json")
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	in := Cursor{Schema: Schema, Keys: map[string]Mark{
		KeyGateway:   {Level: string(probe.StateFail), At: at, Why: "socket 拨不通"},
		"demo-alice": {Level: string(probe.StateFail), At: at},
	}}
	if err := in.Save(path); err != nil {
		t.Fatalf("存盘失败：%v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("临时文件该被 rename 掉，不该留在盘上")
	}
	out, err := LoadCursor(path)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if len(out.Keys) != 2 || out.Keys["demo-alice"].Why != "" || !out.Keys[KeyGateway].At.Equal(at) {
		t.Errorf("往返对不上：%+v", out.Keys)
	}
}

// CursorPath 落在 <data>/state/notify.json：跟它描述的那份现场待在一起。
func TestCursorPath(t *testing.T) {
	got := filepath.Clean(CursorPath(`/tmp/anc-data`))
	if !strings.HasSuffix(got, filepath.Join("state", "notify.json")) {
		t.Errorf("落点不对：%s", got)
	}
}

// Push 真走 cc-connect 的 unix socket，且请求体就是它要的 POST /send。
func TestPushSendsOverUnixSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("这个平台起不了 unix socket：%v", err)
	}
	defer ln.Close()

	type got struct {
		method, path, ctype string
		req                 SendRequest
	}
	ch := make(chan got, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sr SendRequest
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &sr)
		ch <- got{r.Method, r.URL.Path, r.Header.Get("Content-Type"), sr}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	if err := Push(sock, "demo-alice", "🔴 demo-alice 回不了话"); err != nil {
		t.Fatalf("推送失败：%v", err)
	}
	select {
	case g := <-ch:
		if g.method != "POST" || g.path != "/send" {
			t.Errorf("想要 POST /send，拿到 %s %s", g.method, g.path)
		}
		if !strings.HasPrefix(g.ctype, "application/json") {
			t.Errorf("Content-Type 不对：%s", g.ctype)
		}
		if g.req.Project != "demo-alice" || g.req.Message == "" {
			t.Errorf("请求体不对：%+v", g.req)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("服务端没收到请求")
	}
}

// 非 200 要说清是 gateway 拒了，不能当成功。
func TestPushNonOKIsError(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("这个平台起不了 unix socket：%v", err)
	}
	defer ln.Close()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("unknown project"))
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	err = Push(sock, "demo-alice", "x")
	if err == nil {
		t.Fatal("非 200 必须报错")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "unknown project") {
		t.Errorf("错误信息该带上状态码和网关的回话：%v", err)
	}
}

// socket 不在（gateway 没跑）→ 报错，且点明多半是 gateway 的事。
func TestPushMissingSocketIsError(t *testing.T) {
	dir := t.TempDir()
	err := Push(filepath.Join(dir, "nope.sock"), "demo-alice", "x")
	if err == nil {
		t.Fatal("socket 不在必须报错")
	}
	if !strings.Contains(err.Error(), "gateway") {
		t.Errorf("错误信息该点明 gateway：%v", err)
	}
}

// 正文要让人一眼看出：谁的、什么毛病、恢复与否。
func TestText(t *testing.T) {
	who := []string{"张三"}
	alert := Text(Action{Key: "demo-alice", Kind: KindAlert, Why: "起不来"}, who, DefaultCooldown)
	for _, want := range []string{"demo-alice", "起不来", "张三"} {
		if !strings.Contains(alert, want) {
			t.Errorf("告警正文缺 %q：\n%s", want, alert)
		}
	}
	rec := Text(Action{Key: "demo-alice", Kind: KindRecovery, Why: "回得了话"}, who, DefaultCooldown)
	if strings.Contains(rec, "该处理") {
		t.Errorf("恢复通知不该再说「该处理」：\n%s", rec)
	}
	if !strings.Contains(rec, "已恢复") {
		t.Errorf("恢复通知该说清已恢复：\n%s", rec)
	}
}

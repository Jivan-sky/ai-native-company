package board

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/org"
	"anc/internal/render"
)

// dataflowFixture 造一份「真相源 + 执行面」：把 domains 组织拷进临时目录，
// 旁边补一份 gateway/config.toml。cfgBody 为空时干脆不写这份文件（测「读不到执行面」）。
func dataflowFixture(t *testing.T, cfgBody string) (vault, root string) {
	t.Helper()
	root = t.TempDir()
	vault = filepath.Join(root, "vault")
	if err := os.CopyFS(vault, os.DirFS(fixturePath(t, "domains"))); err != nil {
		t.Fatal(err)
	}
	if cfgBody == "" {
		return vault, root
	}
	cfg := filepath.Join(root, "gateway", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	return vault, root
}

// 渲染器认的那种 project 块：判据是 append_system_prompt 的 ”' 多行 literal。
func renderedConfig(relayTimeout int) string {
	return "# anc:generated v=0.1.0 inputs=deadbeef at=2026-10-07T17:58:32Z\n" +
		"# 本文件由 `anc render` 全量生成；手改视为事故，重跑即覆盖。\n\n" +
		"[relay]\ntimeout_secs = " + itoa(relayTimeout) + "\nvisibility = \"summary\"\n\n" +
		"[[projects]]\nname = \"alice\"\n\n[projects.agent]\ntype = \"claudecode\"\n\n" +
		"[projects.agent.options]\nappend_system_prompt = '''\n## 1 身份\n\n你是 Alice 的助理。\n'''\n"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func dataflowOf(t *testing.T, s *Server) (DataflowView, string) {
	t.Helper()
	rec := get(t, s.Handler(), "/api/dataflow")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200：%s", rec.Code, rec.Body.String())
	}
	var v DataflowView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("不是合法 JSON：%v", err)
	}
	if v.Schema != DataflowSchema {
		t.Fatalf("schema=%q，期望 %s（前端靠它认这份报告）", v.Schema, DataflowSchema)
	}
	return v, rec.Body.String()
}

// 策略面：逐 bot 出「手里有什么」，入站只出**人数**，不出标识符。
func TestDataflowPolicyFace(t *testing.T) {
	vault, _ := dataflowFixture(t, renderedConfig(0))
	v, _ := dataflowOf(t, &Server{Vault: vault})
	if !v.Wired {
		t.Fatalf("真相源读得动却报 wired=false：%s", v.Error)
	}
	if len(v.Bots) == 0 {
		t.Fatal("domains 夹具里有启用成员，策略面不该是空的")
	}
	for _, b := range v.Bots {
		if b.Project == "" || b.Role == "" {
			t.Errorf("每条都该说清是哪个 bot、什么角色：%+v", b)
		}
		if b.Inbound > 1 {
			t.Errorf("入站「本人那一条」最多算 1，实际 %d（%s）", b.Inbound, b.Project)
		}
	}
}

// **硬约束**：数据流这一页也不能变成凭据面 —— 夹具里的飞书标识符一个都不许露面。
func TestDataflowLeaksNoCredentials(t *testing.T) {
	vault, _ := dataflowFixture(t, renderedConfig(0))
	_, body := dataflowOf(t, &Server{Vault: vault})
	for _, bad := range []string{"ou_demo_", "cli_demo_", "app_id", "open_id", "app_secret"} {
		if strings.Contains(body, bad) {
			t.Fatalf("数据流视图里出现了 %q —— 看板是观测面，不是凭据面：\n%s", bad, body)
		}
	}
	// 也不许带出本机布局：只出相对的东西。
	if strings.Contains(body, vault) {
		t.Fatalf("响应里带出了 vault 绝对路径：\n%s", body)
	}
}

// 通道关着（v1 口径 0）：声明有、值为 0、没有结构性缺口。
func TestDataflowRelayClosed(t *testing.T) {
	vault, _ := dataflowFixture(t, renderedConfig(0))
	v, _ := dataflowOf(t, &Server{Vault: vault})
	if !v.Relay.Declared || v.Relay.TimeoutSecs != 0 {
		t.Fatalf("relay 应当是「声明了且为 0」，实际 %+v", v.Relay)
	}
	if len(v.Gaps) != 0 {
		t.Fatalf("关闭状态的执行面不该报结构性缺口，实际 %v", v.Gaps)
	}
	if !v.Exec.ConfigPresent || !v.Exec.HasFingerprint || v.Exec.Inputs != "deadbeef" {
		t.Fatalf("执行面身份没读出来：%+v", v.Exec)
	}
	if len(v.Exec.Projects) != 1 || v.Exec.Projects[0] != "alice" {
		t.Fatalf("执行面 project 名单 = %v，期望 [alice]", v.Exec.Projects)
	}
}

// 通道被打开（手改产物）：既要进 gaps，也要在 note 里说人话 ——
// 这条是 GitHub #33 那条负向断言在看板这一侧的投影。
func TestDataflowCatchesRelayOpened(t *testing.T) {
	vault, _ := dataflowFixture(t, renderedConfig(120))
	v, _ := dataflowOf(t, &Server{Vault: vault})
	if len(v.Gaps) == 0 {
		t.Fatal("timeout_secs=120 是上游默认（通道开着），必须报结构性缺口")
	}
	if !strings.Contains(v.Relay.Note, "开着") {
		t.Fatalf("通道开着要在 note 里说明白，实际：%q", v.Relay.Note)
	}
}

// 没有 [relay] 段：等于静默沿用上游默认，也算「开着」。
func TestDataflowRelayMissingSection(t *testing.T) {
	cfg := strings.Replace(renderedConfig(0), "[relay]\ntimeout_secs = 0\nvisibility = \"summary\"\n\n", "", 1)
	vault, _ := dataflowFixture(t, cfg)
	v, _ := dataflowOf(t, &Server{Vault: vault})
	if v.Relay.Declared {
		t.Fatal("没有 [relay] 段却报「声明了」")
	}
	if len(v.Gaps) == 0 {
		t.Fatal("没写这一段 = 静默放行，必须报缺口")
	}
}

// 读不到执行面：照回 200，策略面照出，并把「答不了什么」说清楚。
func TestDataflowWithoutExecFace(t *testing.T) {
	vault, _ := dataflowFixture(t, "")
	v, _ := dataflowOf(t, &Server{Vault: vault})
	if v.Exec.ConfigPresent {
		t.Fatal("没有 config.toml 却报 config_present=true")
	}
	if v.Exec.Note == "" {
		t.Fatal("读不到执行面要如实说，不许留空")
	}
	if !v.Wired || len(v.Bots) == 0 {
		t.Fatal("执行面缺席不该连带把真相源那半边也吞掉")
	}
}

// 真相源读不动：wired=false + 明说读不了，不猜一份策略出来。
func TestDataflowBadVault(t *testing.T) {
	v, _ := dataflowOf(t, &Server{Vault: t.TempDir()})
	if v.Wired {
		t.Fatal("空目录读不出组织却报 wired=true")
	}
	if v.Error == "" {
		t.Fatal("读不动要说明原因")
	}
}

func TestDataflowIsReadOnly(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/dataflow", nil)
	rec := httptest.NewRecorder()
	(&Server{Vault: fixturePath(t, "domains")}).Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 %d，期望 405", rec.Code)
	}
}

// 同一个 bot 在两页上必须叫同一个名字：运行态页读的是 config 里的 project 名，
// 策略面报的也必须是它。各叫各的现场最坑人 —— 看板显示 alice、日志里是 demo-alice。
//
// 这条用**真渲染器**产物当夹具（不是手写一份 config 凑数）：钉的是「看板跟渲染规则
// 同源」这件事，规则改了而看板没跟上，这里就红。
func TestDataflowProjectNameMatchesConfig(t *testing.T) {
	vault, root := dataflowFixture(t, "")
	o, err := org.Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := render.Build(o, render.Options{
		Host:    org.Host{VaultRoot: vault, HomesRoot: filepath.Join(root, "homes"), DataDir: filepath.Join(root, "data")},
		Version: "test", Now: fixedNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "gateway", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(plan.Text), 0o600); err != nil {
		t.Fatal(err)
	}
	v, _ := dataflowOf(t, &Server{Vault: vault})
	if len(v.Bots) != len(plan.Projects) {
		t.Fatalf("启用成员 %d 个、执行面 project %d 个，本该一一对应：bots=%+v projects=%v",
			len(v.Bots), len(plan.Projects), v.Bots, plan.Projects)
	}
	inConfig := map[string]bool{}
	for _, n := range plan.Projects {
		inConfig[n] = true
	}
	for _, b := range v.Bots {
		if !inConfig[b.Project] {
			t.Fatalf("策略面报的 project=%q 不在执行面 %v 里 —— 同一个 bot 两页两个名", b.Project, plan.Projects)
		}
	}
}

// 空数组不是 null：前端直接 .length / .map，null 会把整页打崩。
func TestDataflowEmptyArraysAreNotNull(t *testing.T) {
	vault, _ := dataflowFixture(t, renderedConfig(0))
	_, body := dataflowOf(t, &Server{Vault: vault})
	for _, want := range []string{`"gaps": []`, `"bots": [`} {
		if !strings.Contains(body, want) {
			t.Fatalf("响应里该出现 %s：\n%s", want, body)
		}
	}
	if strings.Contains(body, `"gaps": null`) {
		t.Fatalf("gaps 是 null —— 前端会炸：\n%s", body)
	}
}

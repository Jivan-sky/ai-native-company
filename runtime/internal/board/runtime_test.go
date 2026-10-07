package board

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runtimeFixture 造一份「真相源 + 运行态现场」：把 one 组织拷进临时目录（admins: alice），
// 旁边补 gateway/config.toml 与 data/{run,sessions}。
func runtimeFixture(t *testing.T, withSocket bool) (vault, data string) {
	t.Helper()
	root := t.TempDir()
	vault = filepath.Join(root, "vault")
	if err := os.CopyFS(vault, os.DirFS(fixturePath(t, "one"))); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "gateway", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("\n[[projects]]\nname = \"a\"\n"), 0o600); err != nil {
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
		ln, err := net.Listen("unix", filepath.Join(data, "run", "api.sock"))
		if err != nil {
			t.Skipf("这个平台起不了 unix socket：%v", err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_ = c.Close()
			}
		}()
	}
	writeRuntimeSession(t, data, "a_00000001.json", time.Now())
	return vault, data
}

func writeRuntimeSession(t *testing.T, data, name string, now time.Time) {
	t.Helper()
	one := map[string]any{
		"sessions": map[string]any{"s1": map[string]any{
			"id": "s1", "name": "default", "agent_session_id": "sess-1",
			"history": []map[string]string{
				{"role": "user", "content": "…", "timestamp": now.Add(-2 * time.Minute).Format(time.RFC3339Nano)},
				{"role": "assistant", "content": "…", "timestamp": now.Add(-time.Minute).Format(time.RFC3339Nano)},
			},
		}},
	}
	b, err := json.Marshal(one)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "sessions", name), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runtimeOf(t *testing.T, s *Server) (RuntimeView, string) {
	t.Helper()
	rec := get(t, s.Handler(), "/api/runtime")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200：%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type=%q，期望 application/json", ct)
	}
	var v RuntimeView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("不是合法 JSON：%v", err)
	}
	if v.Schema != "anc.runtime/v1" {
		t.Fatalf("schema=%q，期望 anc.runtime/v1（前端靠它认这份报告）", v.Schema)
	}
	if !v.Wired && v.Bots == nil {
		t.Fatal("bots 应当是空数组而不是 null —— 前端要能直接 map")
	}
	return v, rec.Body.String()
}

// 没给 data 目录：如实回 wired=false，而不是编一份绿灯。
func TestRuntimeNotWired(t *testing.T) {
	v, _ := runtimeOf(t, &Server{Vault: fixturePath(t, "one")})
	if v.Wired {
		t.Fatal("没给 DataDir 却报 wired=true")
	}
	if v.Error == "" {
		t.Fatal("没接上要写清楚怎么接")
	}
}

// 接上了：gateway 判活 + 每个 bot 的三档判定 + 「出事交给谁」。
func TestRuntimeWired(t *testing.T) {
	vault, data := runtimeFixture(t, true)
	v, body := runtimeOf(t, &Server{Vault: vault, DataDir: data})
	if !v.Wired {
		t.Fatalf("接上了却报 wired=false：%s", v.Error)
	}
	if v.Gateway != "up" {
		t.Fatalf("gateway=%q（%s），期望 up", v.Gateway, v.GatewayWhy)
	}
	if len(v.Bots) != 1 || v.Bots[0].State != "ok" {
		t.Fatalf("刚回过话的 bot 应当报绿，实际 %+v", v.Bots)
	}
	if len(v.Handlers) != 1 || !strings.Contains(v.Handlers[0], "Alice") {
		t.Fatalf("该交给谁应当来自 company.admins，实际 %v（%s）", v.Handlers, v.HandlerWhy)
	}
	// 观测面不泄漏本机布局：vault / data 的绝对路径一个都不许出现在响应里。
	if strings.Contains(body, vault) || strings.Contains(body, data) {
		t.Fatalf("响应里带出了本机绝对路径：%s", body)
	}
}

// gateway 没在跑时也照回 200 —— 它不是「请求错了」，它就是来报这件事的。
func TestRuntimeGatewayDownIsStill200(t *testing.T) {
	vault, data := runtimeFixture(t, false)
	v, _ := runtimeOf(t, &Server{Vault: vault, DataDir: data})
	if v.Gateway != "down" {
		t.Fatalf("gateway=%q，期望 down", v.Gateway)
	}
	if len(v.Bots) != 1 || v.Bots[0].State != "fail" {
		t.Fatalf("网关不在跑时 bot 必然回不了话，应当报红，实际 %+v", v.Bots)
	}
}

// 只读口径：运行态端点也不接受写方法。
func TestRuntimeIsReadOnly(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/runtime", nil)
	rec := httptest.NewRecorder()
	(&Server{Vault: fixturePath(t, "one")}).Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 %d，期望 405", rec.Code)
	}
}

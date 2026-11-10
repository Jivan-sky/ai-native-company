package board

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anc/internal/agent"
	"anc/internal/authz"
)

// 假模型：第一轮要工具，第二轮给最终文本。和 agent 包的用例同一套路子。
type fakeChat struct{ calls int }

func (f *fakeChat) Stream(ctx context.Context, msgs []agent.Message, tools []agent.ToolSpec, onDelta func(agent.Delta) error) error {
	f.calls++
	if f.calls == 1 {
		if err := onDelta(agent.Delta{Text: "我先查"}); err != nil {
			return err
		}
		if err := onDelta(agent.Delta{ToolIndex: 0, ToolID: "c1", ToolName: "read_context", ToolArgs: `{"who":"alice"}`}); err != nil {
			return err
		}
		return onDelta(agent.Delta{Usage: &agent.Usage{In: 10, Out: 2}})
	}
	if err := onDelta(agent.Delta{Text: "好了"}); err != nil {
		return err
	}
	return onDelta(agent.Delta{Usage: &agent.Usage{In: 20, Out: 5}})
}

// newGated 造一个「配了端点 + 配了闸」的服务，并签一张票。
func newGated(t *testing.T) (*Server, string, ed25519.PrivateKey) {
	t.Helper()
	dir := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tok, err := authz.Sign(priv, "k1", authz.Claims{
		Sub: "invoice-sweep", Aud: "chat", Scope: []string{"qwen-max-prod"},
		BudgetUSD: 0.05, IAT: now.Unix(), Exp: now.Add(10 * time.Minute).Unix(), JTI: "j-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Vault:      t.TempDir(),
		Chat:       &ChatConfig{BaseURL: "http://x", Model: "m"},
		ChatClient: &fakeChat{},
		Gate: &GateConfig{
			PubKey:  pub,
			Revoked: authz.Revocations{Path: filepath.Join(dir, "revoked.json")},
			Budgets: authz.Budgets{Path: filepath.Join(dir, "budgets.json"), Reserve: 0.02},
			ModelID: "qwen-max-prod",
		},
	}
	return s, tok, priv
}

func postChat(t *testing.T, s *Server, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestChatStreamsSSE(t *testing.T) {
	s, tok, _ := newGated(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/api/chat", strings.NewReader(`{"prompt":"alice 有什么"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("HTTP %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type=%q", ct)
	}
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	body := sb.String()
	for _, want := range []string{"event: delta", "event: tool", "event: done", `"incomplete":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE 缺 %q：\n%s", want, body)
		}
	}
}

// 这个口在设计上必须**永远不读密钥值**：给一份带哨兵的 secrets，响应里出现即失败。
func TestModelsEndpointNeverLeaksValues(t *testing.T) {
	parent := t.TempDir()
	vault := filepath.Join(parent, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := `{"schema":"anc.models/v1","entries":[{"id":"q","provider":"p","base_url":"https://x","model":"m",
	  "secret":"K_PROD","limits":{"rpm":1}}]}`
	if err := os.WriteFile(filepath.Join(parent, "registry.json"), []byte(reg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secrets.env"), []byte("K_PROD=sk-SENTINEL-9f2e\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{Vault: vault}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/models", nil))
	body := rec.Body.String()
	if strings.Contains(body, "sk-SENTINEL") {
		t.Fatalf("/api/models 泄漏了密钥原文：%s", body)
	}
	var out struct {
		Wired   bool `json:"wired"`
		Entries []struct {
			Configured bool `json:"configured"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("不是 JSON：%v", err)
	}
	if !out.Wired || len(out.Entries) != 1 || !out.Entries[0].Configured {
		t.Fatalf("应当报「配了」：%s", body)
	}
}

func TestChatNotConfiguredIs501(t *testing.T) {
	s := &Server{Vault: t.TempDir()}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"prompt":"x"}`)))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("没配端点应当 501，实际 %d", rec.Code)
	}
}

// 闸的三条主线：无票 401、越权 403、预算不够 429。
func TestChatGate(t *testing.T) {
	s, tok, priv := newGated(t)

	if rec := postChat(t, s, "", `{"prompt":"x"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无票应当 401，实际 %d", rec.Code)
	}
	if rec := postChat(t, s, "garbage", `{"prompt":"x"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("假票应当 401，实际 %d", rec.Code)
	}
	// 越权：换一个不在 scope 里的注册名
	if rec := postChat(t, s, tok, `{"prompt":"x","model_id":"qwen-turbo-dev"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("越权应当 403，实际 %d", rec.Code)
	}
	// 被撤销
	_ = s.Gate.Revoked.Revoke("j-1", time.Now().Add(time.Hour))
	if rec := postChat(t, s, tok, `{"prompt":"x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("已撤销应当 403，实际 %d", rec.Code)
	}
	_ = priv // 签名端在这一侧不用
}

// 预算：预留 0.02、票面 0.05 → 第 3 次就该 429（前两次各占 0.02）。
func TestChatBudgetExhausts(t *testing.T) {
	s, tok, _ := newGated(t)
	for i := 0; i < 2; i++ {
		if rec := postChat(t, s, tok, `{"prompt":"x"}`); rec.Code != 200 {
			t.Fatalf("第 %d 次应当放行，实际 %d：%s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := postChat(t, s, tok, `{"prompt":"x"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("预算用尽应当 429，实际 %d：%s", rec.Code, rec.Body.String())
	}
}

// 配了端点却没配闸：503，绝不放行。
func TestChatWithoutGateRefuses(t *testing.T) {
	s := &Server{Vault: t.TempDir(), Chat: &ChatConfig{BaseURL: "http://x", Model: "m"}, ChatClient: &fakeChat{}}
	if rec := postChat(t, s, "whatever", `{"prompt":"x"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("没有闸应当 503，实际 %d", rec.Code)
	}
}

// 限流按注册名（厂商配额），不按票：多签几张票也不能绕过。
func TestChatRateLimited(t *testing.T) {
	s, tok, _ := newGated(t)
	s.Gate.RPM = map[string]int{"qwen-max-prod": 2}
	s.Gate.Limits = authz.RateLimits{Path: filepath.Join(t.TempDir(), "rl.json")}

	for i := 0; i < 2; i++ {
		if rec := postChat(t, s, tok, `{"prompt":"x"}`); rec.Code != 200 {
			t.Fatalf("第 %d 次应当放行，实际 %d", i+1, rec.Code)
		}
	}
	rec := postChat(t, s, tok, `{"prompt":"x"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("第 3 次应当被限流，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("被限流要给 Retry-After")
	}
}

// 应用清单：只出身份与状态，不出包内内容。
func TestAppsEndpoint(t *testing.T) {
	vault := t.TempDir()
	s := &Server{Vault: vault}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/apps", nil))
	if rec.Code != 200 {
		t.Fatalf("HTTP %d", rec.Code)
	}
	var out struct {
		Wired bool  `json:"wired"`
		Apps  []any `json:"apps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("不是 JSON：%v", err)
	}
	if !out.Wired || len(out.Apps) != 0 {
		t.Fatalf("空目录应当 wired=true、0 个应用：%s", rec.Body.String())
	}
}

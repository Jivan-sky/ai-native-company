package board

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/agent"
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

func newChatServer(t *testing.T) *Server {
	t.Helper()
	vault := t.TempDir()
	s := &Server{Vault: vault, Chat: &ChatConfig{BaseURL: "http://x", Model: "m"}, ChatClient: &fakeChat{}}
	return s
}

func TestChatStreamsSSE(t *testing.T) {
	s := newChatServer(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/chat", "application/json", strings.NewReader(`{"prompt":"alice 有什么"}`))
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

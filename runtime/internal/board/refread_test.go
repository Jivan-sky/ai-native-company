package board

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func vaultWithFile(t *testing.T, rel, content string) string {
	t.Helper()
	v := t.TempDir()
	full := filepath.Join(v, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return v
}

// agent 要能真的打开 refs：读得到、行范围可用。
func TestRefReadsFileAndLineRange(t *testing.T) {
	v := vaultWithFile(t, "domains/trade/contract.md", "line1\nline2\nline3\nline4\n")
	s := &Server{Vault: v}
	for _, c := range []struct {
		q    string
		want string
		to   int
	}{{"?path=domains/trade/contract.md#2-3", "line2\nline3", 3}} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/ref"+c.q, nil))
		var out RefView
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out.Content != c.want || out.To != c.to {
			t.Fatalf("%s：拿到 %q（%d-%d），期望 %q", c.q, out.Content, out.From, out.To, c.want)
		}
		if out.Total != 4 {
			t.Fatalf("总行数该是 4，拿到 %d", out.Total)
		}
	}
}

// 逃出 vault 的路径必须被拒 —— 这是读口，不是文件浏览器。
func TestRefRejectsTraversal(t *testing.T) {
	v := vaultWithFile(t, "ok.md", "x")
	s := &Server{Vault: v}
	for _, p := range []string{"../../etc/passwd", "/etc/passwd"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/ref?path="+p, nil))
		var out RefView
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out.Content != "" {
			t.Fatalf("越界路径 %q 不该读出内容，拿到 %q", p, out.Content)
		}
	}
}

// 读不到要给一句人话，不是空响应。
func TestRefMissingFileExplains(t *testing.T) {
	v := vaultWithFile(t, "ok.md", "x")
	s := &Server{Vault: v}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/ref?path=nope.md", nil))
	var out RefView
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error == "" {
		t.Fatalf("缺文件要说明原因，拿到 %+v", out)
	}
}

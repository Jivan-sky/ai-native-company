package board

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// 没有定义就不上屏；给了定义就按定义算。
func TestMetricsEndpointUsesVaultDefinition(t *testing.T) {
	v := t.TempDir()
	s := &Server{Vault: v}

	// ① 没有 metrics.md：一个数字都没有，且说明原因
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/metrics", nil))
	var first map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &first)
	if vals, _ := first["values"].([]any); len(vals) != 0 {
		t.Fatalf("没有定义时不该有数字：%v", first)
	}

	// ② 给一张表：按它算
	if err := os.WriteFile(filepath.Join(v, "metrics.md"),
		[]byte("| id | 名称 | kind | 条件 | 窗口(天) | 负责人 |\n|---|---|---|---|---|---|\n| d | 完成 | count | status in (done) | 7 | 记录 |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(v, "timeline"), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"id":"a","at":"2026-11-20T10:00:00Z","status":"done","title":"x"}` + "\n"
	if err := os.WriteFile(filepath.Join(v, "timeline", "2026-11.alice.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, httptest.NewRequest("GET", "/api/metrics", nil))
	var out struct {
		Values []struct {
			ID    string  `json:"id"`
			Value float64 `json:"value"`
		} `json:"values"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Values) != 1 || out.Values[0].ID != "d" || out.Values[0].Value != 1 {
		t.Fatalf("该按定义算出 1：%+v", out.Values)
	}
}

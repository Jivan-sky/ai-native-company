package board

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeTimeline(t *testing.T, vault string, lines ...string) {
	t.Helper()
	dir := filepath.Join(vault, "timeline")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-11.alice.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 先例只取决策行，且按域过滤；新的排前面。
func TestPrecedentsFiltersDecisionsByDomain(t *testing.T) {
	v := t.TempDir()
	writeTimeline(t, v,
		`{"id":"d1","case":"p1","at":"2026-11-10T10:00:00+08:00","kind":"decision","by":"alice","domain":"trade","title":"报价 —— 同意"}`,
		`{"id":"d2","case":"p2","at":"2026-11-12T10:00:00+08:00","kind":"decision","by":"bob","domain":"trade","title":"合并记录 —— 驳回"}`,
		`{"id":"d3","case":"p3","at":"2026-11-13T10:00:00+08:00","kind":"decision","by":"bob","domain":"delivery","title":"巡检 —— 同意"}`,
		`{"id":"x1","case":"p4","at":"2026-11-14T10:00:00+08:00","kind":"execution","domain":"trade","title":"不是决策"}`,
	)
	s := &Server{Vault: v}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/precedents?domain=trade", nil))
	if rec.Code != 200 {
		t.Fatalf("状态 %d", rec.Code)
	}
	var out PrecedentView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 2 || len(out.Entries) != 2 {
		t.Fatalf("trade 域该有 2 条决策，拿到 %d：%+v", out.Total, out.Entries)
	}
	if out.Entries[0].ID != "d2" {
		t.Fatalf("新的该排前面，拿到 %s", out.Entries[0].ID)
	}
	if out.Entries[0].Verdict != "驳回" {
		t.Fatalf("结论词该从标题取到「驳回」，拿到 %q", out.Entries[0].Verdict)
	}
}

// 查不到就明说没查到，不拿相近的凑数。
func TestPrecedentsSaysWhenNothingFound(t *testing.T) {
	v := t.TempDir()
	writeTimeline(t, v, `{"id":"d1","at":"2026-11-10T10:00:00+08:00","kind":"decision","domain":"trade","title":"x —— 同意"}`)
	s := &Server{Vault: v}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/precedents?domain=delivery", nil))
	var out PrecedentView
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Total != 0 || len(out.Missing) == 0 {
		t.Fatalf("没查到要说明，拿到 %+v", out)
	}
}

// 关键词只在标题/详情的原文里找，不做同义扩展。
func TestPrecedentsKeywordMatchesRawText(t *testing.T) {
	v := t.TempDir()
	writeTimeline(t, v,
		`{"id":"d1","at":"2026-11-10T10:00:00+08:00","kind":"decision","domain":"trade","title":"计费口径 —— 同意","detail":"改为按月"}`,
		`{"id":"d2","at":"2026-11-11T10:00:00+08:00","kind":"decision","domain":"trade","title":"报价 —— 同意"}`,
	)
	s := &Server{Vault: v}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/precedents?domain=trade&q=口径", nil))
	var out PrecedentView
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Total != 1 || out.Entries[0].ID != "d1" {
		t.Fatalf("关键词该只命中标题/详情原文，拿到 %+v", out.Entries)
	}
}

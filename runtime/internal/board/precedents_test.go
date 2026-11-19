package board

import (
	"anc/internal/approvals"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// 「拖住了」是事实，不是催办：只报放了多久、有没有超阈值，不报该催谁。
func TestPendingViewsReportsAgeAndStale(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	list := []approvals.Pending{
		{ID: "fresh", Enqueued: "2026-11-20T09:00:00Z"},
		{ID: "old", Enqueued: "2026-11-15T12:00:00Z"}, // 5 天
		{ID: "bad", Enqueued: "不是时间"},
	}
	views := pendingViews(list, now, 3, nil, nil)
	if views[0].Stale || views[0].AgeHours != 3 {
		t.Fatalf("3 小时前的不该算拖住：%+v", views[0])
	}
	if !views[1].Stale || views[1].AgeHours != 120 {
		t.Fatalf("5 天前该算拖住且 age=120h：%+v", views[1])
	}
	if views[2].Stale || views[2].AgeHours != 0 {
		t.Fatalf("时间解析不出来时留 0 且不算拖住，不拿「可能很久」吓人：%+v", views[2])
	}
}

// 待批条目自带同域先例：一次请求拿到"这条 + 先例"，不同域的不混进来。
func TestPendingViewsEmbedSameDomainPrecedents(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	byDomain := map[string][]Precedent{
		"trade":    {{ID: "d1", Domain: "trade", Title: "报价 —— 同意"}},
		"delivery": {{ID: "d2", Domain: "delivery", Title: "巡检 —— 驳回"}},
	}
	list := []approvals.Pending{
		{ID: "a", Domain: "trade", Enqueued: "2026-11-20T09:00:00Z"},
		{ID: "b", Domain: "delivery", Enqueued: "2026-11-20T09:00:00Z"},
		{ID: "c", Domain: "", Enqueued: "2026-11-20T09:00:00Z"},
	}
	views := pendingViews(list, now, 3, byDomain, nil)
	if len(views[0].Precedents) != 1 || views[0].Precedents[0].ID != "d1" {
		t.Fatalf("trade 该拿到 d1：%+v", views[0].Precedents)
	}
	if len(views[1].Precedents) != 1 || views[1].Precedents[0].ID != "d2" {
		t.Fatalf("delivery 该拿到 d2：%+v", views[1].Precedents)
	}
	if len(views[2].Precedents) != 0 {
		t.Fatalf("没有域的条目不该硬塞先例：%+v", views[2].Precedents)
	}
}

// 截止时间是事实：还有多久、是否已逾期；没给就不猜。
func TestPendingViewsReportsDueAndOverdue(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	list := []approvals.Pending{
		{ID: "later", Enqueued: "2026-11-20T09:00:00Z", DueAt: "2026-11-21T12:00:00Z"}, // 还剩 24h
		{ID: "late", Enqueued: "2026-11-20T09:00:00Z", DueAt: "2026-11-19T12:00:00Z"},  // 逾期 24h
		{ID: "none", Enqueued: "2026-11-20T09:00:00Z"},
	}
	views := pendingViews(list, now, 3, nil, nil)
	if views[0].HoursLeft != 24 || views[0].Overdue {
		t.Fatalf("还剩 24 小时：%+v", views[0])
	}
	if views[1].HoursLeft != -24 || !views[1].Overdue {
		t.Fatalf("逾期 24 小时：%+v", views[1])
	}
	if views[2].DueAt != "" || views[2].HoursLeft != 0 || views[2].Overdue {
		t.Fatalf("没给截止就不该编：%+v", views[2])
	}
}

// 依据打不开要在读的时候暴露出来：agent 才知道"这条的一半依据是空的"。
func TestPendingViewsReportsUnreadableRefs(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	check := func(refs []string) (int, []string) {
		var miss []string
		ok := 0
		for _, r := range refs {
			if r == "gone.md" {
				miss = append(miss, r)
				continue
			}
			ok++
		}
		return ok, miss
	}
	list := []approvals.Pending{
		{ID: "a", Refs: []string{"ok.md", "gone.md"}, Enqueued: "2026-11-20T09:00:00Z", Title: "t", Body: "b", Domain: "trade"},
	}
	views := pendingViews(list, now, 3, nil, check)
	if views[0].RefsChecked != 1 || len(views[0].RefsMissing) != 1 {
		t.Fatalf("该是 1 条可读 1 条缺失：%+v", views[0])
	}
	joined := ""
	for _, m := range views[0].Brief.Missing {
		joined += m
	}
	if !strings.Contains(joined, "打不开") {
		t.Fatalf("brief.missing 要指出依据打不开，拿到 %v", views[0].Brief.Missing)
	}
}

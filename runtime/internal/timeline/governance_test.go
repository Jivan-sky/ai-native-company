package timeline

import "testing"

// 更正只追加：原文不动，扫描时标出"谁更正了它"。
func TestScanGovernanceMarksCorrectedAndRetired(t *testing.T) {
	es := []Entry{
		{ID: "e1", At: "2026-11-19T09:00:00+08:00", Title: "原记录"},
		{ID: "e2", At: "2026-11-19T10:00:00+08:00", Title: "更正 e1", Corrects: "e1"},
		{ID: "e3", At: "2026-11-19T11:00:00+08:00", Title: "废止某经验", Retires: "exp-1"},
	}
	g := ScanGovernance(es)
	if g.Corrected["e1"] != "e2" {
		t.Fatalf("e1 该被 e2 更正，拿到 %q", g.Corrected["e1"])
	}
	if g.Retired["exp-1"] != "e3" {
		t.Fatalf("exp-1 该被 e3 废止，拿到 %q", g.Retired["exp-1"])
	}
	if len(es) != 3 || es[0].Title != "原记录" {
		t.Fatal("扫描不能改写原记录 —— 更正只追加")
	}
}

// 同一条被更正两次：后到的赢，且结果可重放（At 相同按 id 字典序）。
func TestScanGovernanceLastCorrectionWinsDeterministically(t *testing.T) {
	es := []Entry{
		{ID: "b", At: "2026-11-19T10:00:00+08:00", Corrects: "x"},
		{ID: "a", At: "2026-11-19T10:00:00+08:00", Corrects: "x"},
	}
	g := ScanGovernance(es)
	if g.Corrected["x"] != "b" {
		t.Fatalf("同一时刻该按 id 字典序取后一个（b），拿到 %q", g.Corrected["x"])
	}
}

// 回填规则：老数据没有 derived_from 时，读侧按 prev 推断（不改写历史）。
func TestUpstreamsFallsBackToPrev(t *testing.T) {
	withDF := Entry{ID: "a", DerivedFrom: []string{"x"}, Prev: "y"}
	if got := withDF.Upstreams(); len(got) != 1 || got[0] != "x" {
		t.Fatalf("有 derived_from 时该用它，拿到 %v", got)
	}
	onlyPrev := Entry{ID: "b", Prev: "p1"}
	if got := onlyPrev.Upstreams(); len(got) != 1 || got[0] != "p1" {
		t.Fatalf("没有 derived_from 但有 prev 时该推断成 [p1]，拿到 %v", got)
	}
	neither := Entry{ID: "c"}
	if got := neither.Upstreams(); len(got) != 0 {
		t.Fatalf("两者都没有时该是空，拿到 %v", got)
	}
}

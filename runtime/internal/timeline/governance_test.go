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

package board

import (
	"testing"

	"anc/internal/timeline"
)

// 序列是「每日一条」的效率读数：按条目算，按天分组，少的就少报。
// 这一条测试盯的是三件事：按原文日期分组（不转时区）、排序、other 不混进 done/failed。
func TestDaySeries(t *testing.T) {
	in := []timeline.Entry{
		{At: "2026-11-10T09:31:30+08:00", Status: "done"},
		{At: "2026-11-10T10:00:00+08:00", Status: "done"},
		{At: "2026-11-10T11:00:00+08:00", Status: "failed"},
		{At: "2026-11-10T12:00:00+08:00", Status: "ok"}, // other：不进达标率的分子分母
		{At: "2026-11-11T09:00:00+08:00", Status: "done"},
		{At: "2026-11-12T09:00:00+08:00", Status: "blocked"}, // other
		{At: "", Status: "done"},                             // 没时间的条目直接跳过，不猜
	}
	got := daySeries(in)
	if len(got) != 3 {
		t.Fatalf("要三天，得到 %d：%+v", len(got), got)
	}
	if got[0].Day != "2026-11-10" || got[0].Done != 2 || got[0].Failed != 1 || got[0].Other != 1 {
		t.Errorf("11-10 不对：%+v", got[0])
	}
	if got[1].Day != "2026-11-11" || got[1].Done != 1 {
		t.Errorf("11-11 不对：%+v", got[1])
	}
	if got[2].Day != "2026-11-12" || got[2].Other != 1 || got[2].Done != 0 {
		t.Errorf("11-12 不对：%+v", got[2])
	}
	// 升序：曲线从左到右就是时间从左到右
	if got[0].Day > got[1].Day || got[1].Day > got[2].Day {
		t.Error("序列要按天升序")
	}
}

// 按人筛：大小写与两侧空格不该影响结果；筛不中就是空（不是全部）。
func TestFilterBy(t *testing.T) {
	in := []timeline.Entry{
		{At: "2026-11-10T09:00:00+08:00", By: "alice", Status: "done"},
		{At: "2026-11-10T10:00:00+08:00", By: "Alice ", Status: "failed"},
		{At: "2026-11-10T11:00:00+08:00", By: "bob", Status: "done"},
		{At: "2026-11-10T12:00:00+08:00", By: "", Status: "done"},
	}
	if got := filterBy(in, "alice"); len(got) != 2 {
		t.Fatalf("alice 要有 2 条（含 Alice ），得到 %d", len(got))
	}
	if got := filterBy(in, " Alice "); len(got) != 2 {
		t.Errorf("两侧空格不该影响结果，得到 %d", len(got))
	}
	if got := filterBy(in, "nobody"); len(got) != 0 {
		t.Errorf("搜不到就该是空，得到 %d", len(got))
	}
}

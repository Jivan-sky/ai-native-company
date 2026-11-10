package job

import (
	"testing"
	"time"
)

func TestDue(t *testing.T) {
	sh := func(ev, at string) Spec {
		return Spec{Name: "x", Schedule: Schedule{Every: ev, At: at, TZ: "Asia/Shanghai"}}
	}
	// 从没跑过 → 立刻跑
	if ok, _ := Due(sh("24h", "09:00"), nil, time.Now()); !ok {
		t.Fatal("新作业应当立刻跑")
	}

	loc, _ := time.LoadLocation("Asia/Shanghai")
	lastOK := &State{LastOK: true, LastAt: time.Date(2026, 11, 10, 9, 0, 0, 0, loc)}

	// 同一分钟：不该跑
	if ok, _ := Due(sh("24h", "09:00"), lastOK, time.Date(2026, 11, 10, 9, 30, 0, 0, loc)); ok {
		t.Fatal("刚跑过不该再跑")
	}
	// 第二天 09:01：该跑
	if ok, next := Due(sh("24h", "09:00"), lastOK, time.Date(2026, 11, 11, 9, 1, 0, 0, loc)); !ok {
		t.Fatalf("第二天这个点该跑（next=%v）", next)
	}
	// 停机两天后 tick：仍该跑（补当天），且 next 是当天 09:00
	ok, next := Due(sh("24h", "09:00"), lastOK, time.Date(2026, 11, 12, 10, 0, 0, 0, loc))
	if !ok || next.Day() != 12 {
		t.Fatalf("停机后应当补跑：ok=%v next=%v", ok, next)
	}

	// 失败（LastOK=false）不该让作业停摆：仍然算「该跑」
	failed := &State{LastOK: false, LastAt: time.Date(2026, 11, 10, 9, 0, 0, 0, loc)}
	if ok, _ := Due(sh("24h", "09:00"), failed, time.Date(2026, 11, 10, 9, 5, 0, 0, loc)); !ok {
		t.Fatal("上一次失败应当允许立刻重试（否则一次偶发失败就永久停摆）")
	}
}

func TestDueEveryOnly(t *testing.T) {
	loc := time.UTC
	last := &State{LastOK: true, LastAt: time.Date(2026, 11, 10, 0, 0, 0, 0, loc)}
	if ok, _ := Due(Spec{Schedule: Schedule{Every: "1h"}}, last, time.Date(2026, 11, 10, 0, 30, 0, 0, loc)); ok {
		t.Fatal("没到间隔不该跑")
	}
	if ok, _ := Due(Spec{Schedule: Schedule{Every: "1h"}}, last, time.Date(2026, 11, 10, 1, 1, 0, 0, loc)); !ok {
		t.Fatal("到间隔该跑")
	}
}

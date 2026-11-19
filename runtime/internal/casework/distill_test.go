package casework

import (
	"strings"
	"testing"
)

// 门槛一：有**可复用**的突破 → 够格成为经验候选。
func TestDistillTakesReusableBreakthrough(t *testing.T) {
	v := t.TempDir()
	if _, err := AddBlocker(v, Blocker{ID: "b1", Title: "对账口径不一致", Kind: "口径", Domain: "trade", Case: "task-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := AddBreakthrough(v, Breakthrough{ID: "bt1", BlockerID: "b1", Method: "以附件三为准逐项核", Reusable: true, Scope: "财务/对账"}); err != nil {
		t.Fatal(err)
	}
	made, skipped, err := DistillCase(v, "task-1", DistillOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(made) != 1 {
		t.Fatalf("该产出 1 条候选，拿到 %d（跳过：%v）", len(made), skipped)
	}
	c := made[0]
	if c.Experience.Claim != "以附件三为准逐项核" {
		t.Fatalf("结论该用突破的方法，拿到 %q", c.Experience.Claim)
	}
	if len(c.Experience.From) != 2 {
		t.Fatalf("证据链该含 blocker 与 breakthrough，拿到 %v", c.Experience.From)
	}
	if strings.Contains(c.Experience.By, "alice") {
		t.Fatal("自动候选不该冒用人名署名")
	}
}

// 门槛二：没有突破，但同类（域+类别）踩够次数 → 也够格。
func TestDistillTakesRecurringWithoutBreakthrough(t *testing.T) {
	v := t.TempDir()
	for _, id := range []string{"b1", "b2"} {
		if _, err := AddBlocker(v, Blocker{ID: id, Title: "又卡在口径", Kind: "口径", Domain: "trade", Case: "task-1"}); err != nil {
			t.Fatal(err)
		}
	}
	made, _, err := DistillCase(v, "task-1", DistillOpts{MinRecur: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(made) != 2 {
		t.Fatalf("两条都该够格（同类 2 次），拿到 %d", len(made))
	}
}

// 幂等：重跑结账不该产出重复候选。
func TestDistillIsIdempotent(t *testing.T) {
	v := t.TempDir()
	if _, err := AddBlocker(v, Blocker{ID: "b1", Title: "卡住", Kind: "工具", Domain: "trade", Case: "c1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := AddBreakthrough(v, Breakthrough{ID: "bt1", BlockerID: "b1", Method: "重启进程", Reusable: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DistillCase(v, "c1", DistillOpts{}); err != nil {
		t.Fatal(err)
	}
	made, skipped, err := DistillCase(v, "c1", DistillOpts{})
	if err != nil {
		t.Fatalf("重跑不该报错：%v", err)
	}
	if len(made) != 0 {
		t.Fatalf("重跑不该再产出，拿到 %d", len(made))
	}
	if !strings.Contains(skipped["b1"], "幂等") {
		t.Fatalf("跳过理由该说清幂等，拿到 %q", skipped["b1"])
	}
}

// 门槛不够的必须被跳过，并说明为什么 —— 不许悄悄不提炼。
func TestDistillSkipsWithReason(t *testing.T) {
	v := t.TempDir()
	if _, err := AddBlocker(v, Blocker{ID: "b1", Title: "一次性小问题", Kind: "人", Domain: "trade", Case: "c1"}); err != nil {
		t.Fatal(err)
	}
	made, skipped, err := DistillCase(v, "c1", DistillOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(made) != 0 {
		t.Fatalf("不够格的不该提炼，拿到 %d", len(made))
	}
	if !strings.Contains(skipped["b1"], "没踩够") {
		t.Fatalf("跳过要给理由，拿到 %q", skipped["b1"])
	}
}

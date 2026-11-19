package casework

import "testing"

// 「这个坑踩了几次」是这两类对象存在的第一理由 —— 先钉住它。
func TestRecurringCountsSameKindAndDomain(t *testing.T) {
	v := t.TempDir()
	for _, id := range []string{"b1", "b2"} {
		if _, err := AddBlocker(v, Blocker{ID: id, Title: "口径不一致 " + id, Kind: "口径", Domain: "trade"}); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}
	if _, err := AddBlocker(v, Blocker{ID: "b3", Title: "权限不足", Kind: "权限", Domain: "trade"}); err != nil {
		t.Fatal(err)
	}
	// 解掉一个：组内 solved 应变成 1，但 count 仍是 2
	if _, err := Resolve(v, "b1", "alice"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	groups, err := Recurring(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("该有 2 组（口径 2 条、权限 1 条），拿到 %d：%+v", len(groups), groups)
	}
	if groups[0].Kind != "口径" || groups[0].Count != 2 || groups[0].Solved != 1 {
		t.Fatalf("排序或计数不对：%+v", groups[0])
	}
}

// 突破必须挂在真实卡点上；没有卡点的"突破"无法验证。
func TestBreakthroughRequiresExistingBlocker(t *testing.T) {
	v := t.TempDir()
	if _, err := AddBreakthrough(v, Breakthrough{BlockerID: "nope", Method: "换个口径"}); err == nil {
		t.Fatal("指向不存在的卡点该被拒")
	}
	if _, err := AddBlocker(v, Blocker{ID: "b1", Title: "卡住", Kind: "口径"}); err != nil {
		t.Fatal(err)
	}
	bt, err := AddBreakthrough(v, Breakthrough{ID: "bt1", BlockerID: "b1", Method: "改用附件三", Cost: "2 小时", Reusable: true})
	if err != nil {
		t.Fatalf("add breakthrough: %v", err)
	}
	if bt.At == "" {
		t.Fatal("突破要记时间点")
	}
	all, _ := ListBreakthroughs(v)
	if len(all) != 1 || all[0].BlockerID != "b1" || !all[0].Reusable {
		t.Fatalf("读回来的突破不对：%+v", all)
	}
}

// 卡点被解开是「状态变了」，不是删记录 —— 删掉就答不出「踩过几次」。
func TestResolveKeepsRecordAndRefusesTwice(t *testing.T) {
	v := t.TempDir()
	if _, err := AddBlocker(v, Blocker{ID: "b1", Title: "卡住", Kind: "工具"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(v, "b1", "alice"); err != nil {
		t.Fatal(err)
	}
	all, _ := ListBlockers(v)
	if len(all) != 1 || all[0].Status != BlockerResolved || all[0].ResolvedBy != "alice" {
		t.Fatalf("解开后记录该留着并带 resolved_by：%+v", all)
	}
	if _, err := Resolve(v, "b1", "bob"); err == nil {
		t.Fatal("同一条不该被解两次 —— 解不开的是新卡点")
	}
}

// kind 必填：没有类别就无法归组，也就答不出「踩了几次」。
func TestBlockerRequiresKind(t *testing.T) {
	v := t.TempDir()
	if _, err := AddBlocker(v, Blocker{Title: "没说类别"}); err == nil {
		t.Fatal("没有 kind 的卡点该被拒")
	}
}

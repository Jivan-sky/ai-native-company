package exp

import (
	"os"
	"testing"
)

// 回流是**拉取 + 征询**：目录只列已部署的，不替任何 agent 做决定。
func TestCatalogListsOnlyDeployed(t *testing.T) {
	v := t.TempDir()
	deployed(t, v) // e1 已部署
	if _, err := Add(v, Experience{ID: "e9", Claim: "还没确认", From: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	items, err := Catalog(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "e1" {
		t.Fatalf("只该列出已部署的，拿到 %+v", items)
	}
	if items[0].Digest == "" || items[0].ApprovedBy != "alice" {
		t.Fatalf("目录项要带指纹与批准人：%+v", items[0])
	}
}

// 没看过 → 全是新的；标记看过之后 → 不再重复报（否则人学会忽略它）。
func TestNewSinceTracksPerAgentSeenState(t *testing.T) {
	v := t.TempDir()
	deployed(t, v)
	fresh, err := NewSince(v, "devbot")
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 {
		t.Fatalf("第一次该报 1 条新的，拿到 %d", len(fresh))
	}
	if err := MarkSeen(v, "devbot", fresh); err != nil {
		t.Fatal(err)
	}
	again, _ := NewSince(v, "devbot")
	if len(again) != 0 {
		t.Fatalf("标记看过之后不该再报，拿到 %d", len(again))
	}
	// 换个人：还是新的（按人记状态，不是全局）
	forOther, _ := NewSince(v, "opsbot")
	if len(forOther) != 1 {
		t.Fatalf("另一个主体该看到它，拿到 %d", len(forOther))
	}
}

// 账本说部署了、盘上没有 → 必须能查出来（假状态不许沉默）。
func TestMissingSkillFilesIsDetectable(t *testing.T) {
	v := t.TempDir()
	deployed(t, v)
	_ = removeFileOnly(t, v, "e1")
	miss, err := MissingSkillFiles(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(miss) != 1 || miss[0] != "e1" {
		t.Fatalf("该报出缺失的 e1，拿到 %v", miss)
	}
}

func removeFileOnly(t *testing.T, v, id string) error {
	t.Helper()
	return os.Remove(skillPath(v, id))
}

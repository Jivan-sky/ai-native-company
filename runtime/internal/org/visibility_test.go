package org

import "testing"

// 判据 2 的第一步：可见性只留一份实现。
// 这里同时钉住三条口径 —— 空域恒真、All 恒真、否则按白名单。
func TestVisibilityAllows(t *testing.T) {
	v := Visibility{Domains: []string{"trade", "delivery"}}
	cases := []struct {
		slug string
		want bool
	}{
		{"", true},           // 公司级资料
		{"trade", true},      // 白名单内
		{"logistics", false}, // 白名单外
	}
	for _, c := range cases {
		if got := v.Allows(c.slug); got != c.want {
			t.Errorf("Allows(%q) = %v，期望 %v", c.slug, got, c.want)
		}
	}
	if !(Visibility{All: true}).Allows("logistics") {
		t.Error("All=true 时任何域都该可见")
	}
}

// Viewer.CanSeeDomain 必须与 Visibility 走同一条实现（转发的存在性证明）。
func TestViewerCanSeeDomainMatchesVisibility(t *testing.T) {
	v := Viewer{Domains: []string{"trade"}}
	if v.CanSeeDomain("trade") != v.Visibility().Allows("trade") {
		t.Fatal("CanSeeDomain 与 Visibility.Allows 不一致 —— 报告了同一条口径的两份实现")
	}
	if v.CanSeeDomain("logistics") {
		t.Fatal("不在白名单里的域不该可见")
	}
}

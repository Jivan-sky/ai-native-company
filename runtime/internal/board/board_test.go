package board

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anc/internal/org"
)

var fixedNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func boardOrg(t *testing.T, name string) *org.Org {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "orgs", name))
	if err != nil {
		t.Fatal(err)
	}
	o, err := org.Load(abs)
	if err != nil {
		t.Fatalf("加载 %s: %v", name, err)
	}
	return o
}

// 纯函数：同一份真相源 + 同一个时刻 → 逐字节相同。前端据此可以按字节 diff / 缓存。
func TestOfIsDeterministic(t *testing.T) {
	o := boardOrg(t, "domains")
	a, err := Of(o, fixedNow).JSON()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Of(o, fixedNow).JSON()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("两次投影不一致：\n%s\n---\n%s", a, b)
	}
	if !strings.Contains(a, `"schema": "`+Schema+`"`) {
		t.Fatalf("投影里没有 schema 版本号：\n%s", a)
	}
}

// **这条是硬约束**：看板是观测面，不是凭据面。飞书标识符一个都不许漏进投影。
func TestViewLeaksNoCredentials(t *testing.T) {
	s, err := Of(boardOrg(t, "domains"), fixedNow).JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"ou_demo_alice", "cli_demo_alice", "ou_demo_bob", "cli_demo_dev",
		"open_id", "app_id", "extra_allow_from", "allow_chat",
	} {
		if strings.Contains(s, bad) {
			t.Fatalf("投影里出现了 %q —— 看板不该拿到凭据面字段：\n%s", bad, s)
		}
	}
}

// who 存的是岗位，投影里派生成人名 —— 与 persona 用的是同一处口径（org.WhoLabel）。
func TestViewDerivesWhoLabel(t *testing.T) {
	v := Of(boardOrg(t, "domains"), fixedNow)
	if len(v.Domains) != 2 {
		t.Fatalf("期望 2 个域，实际 %d", len(v.Domains))
	}
	if v.Domains[0].Who != "manager" || v.Domains[0].WhoLabel != "经理（Alice Wang）" {
		t.Fatalf("who / who_label 不对：%+v", v.Domains[0])
	}
}

// 成员的 model 是三级回退之后的生效值，不是原始字段（看板要能直接显示「他跑哪个模型」）。
func TestMemberModelIsResolved(t *testing.T) {
	v := Of(boardOrg(t, "domains"), fixedNow)
	for _, m := range v.Members {
		if m.Model == "" {
			t.Fatalf("成员 %s 的 model 没回退到 company.defaults：%+v", m.Name, m)
		}
	}
}

// nil 切片一律归一成 []：JSON 里 null 会让前端多一层判空。
func TestEmptySlicesAreArraysNotNull(t *testing.T) {
	s, err := Of(boardOrg(t, "one"), fixedNow).JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, "null") {
		t.Fatalf("投影里出现了 null：\n%s", s)
	}
	// one 没有 domains.md，也没有任何成员划域 —— 两处都该是空数组。
	if !strings.Contains(s, `"domains": []`) {
		t.Fatalf("空域表没渲染成 []：\n%s", s)
	}
}

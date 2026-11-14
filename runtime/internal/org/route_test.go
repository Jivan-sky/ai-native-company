package org

import (
	"math/rand"
	"testing"
)

// 用 demo-public 那份真 vault：7 个业务 agent，分在 6 块域上。
func routeOrg(t *testing.T) *Org {
	t.Helper()
	o, err := Load("../../testdata/demo-public/vault")
	if err != nil {
		t.Fatalf("真相源加载失败：%v", err)
	}
	return o
}

// 域对得上的进候选，对不上的**根本不进** —— 不是排最后。
// 这是与 new-api 最根本的差别：它降级只是换个上游，我们降级是让不该碰这块数据的人来碰。
func TestRouteExcludesOtherDomains(t *testing.T) {
	r := RouteTask(routeOrg(t), "delivery-ops", "")
	if len(r.Tiers) == 0 {
		t.Fatal("该有候选")
	}
	got := map[string]bool{}
	for _, tier := range r.Tiers {
		for _, a := range tier.Agents {
			got[a.Slug] = true
		}
	}
	if !got["shopinitbot"] || !got["workorderbot"] {
		t.Fatalf("delivery-ops 的两个 agent 该进候选：%v", got)
	}
	if got["receptionbot"] {
		t.Error("挂在 cs-ops 的 agent 不该进 delivery-ops 的候选")
	}
	excluded := false
	for _, x := range r.Excluded {
		if x.Slug == "receptionbot" && x.Why != "" {
			excluded = true
		}
	}
	if !excluded {
		t.Fatalf("排除要带原因：%+v", r.Excluded)
	}
}

// 岗位也对得上的排第一档。
func TestRouteRoleMatchIsTopTier(t *testing.T) {
	r := RouteTask(routeOrg(t), "delivery-ops", "delivery-owner")
	if len(r.Tiers) == 0 {
		t.Fatal("该有候选")
	}
	if r.Tiers[0].Tier != 0 {
		t.Fatalf("岗位对得上的该在第 1 档：%+v", r.Tiers[0])
	}
	for _, a := range r.Tiers[0].Agents {
		if a.Role != "delivery-owner" {
			t.Errorf("第 1 档里混进了岗位不对的：%+v", a)
		}
	}
}

// 岗位要求写了一个没人代的：候选为空，且**不跨档降级**。
func TestRouteNoFallbackWhenRoleMissing(t *testing.T) {
	r := RouteTask(routeOrg(t), "delivery-ops", "no-such-role")
	if len(r.Tiers) != 0 {
		t.Fatalf("岗位没人代就不该有候选，拿到 %d 档", len(r.Tiers))
	}
	if r.Human() != "没有候选" {
		t.Errorf("没有候选要说清楚：%s", r.Human())
	}
	// 但每个被排除的都要说清为什么
	if len(r.Excluded) == 0 {
		t.Error("排除了谁也要写出来")
	}
}

// 档内均匀：同档两个 agent，多次抽签都该被抽到（不退化成一个）。
func TestRoutePickIsUniformWithinTier(t *testing.T) {
	r := RouteTask(routeOrg(t), "delivery-ops", "delivery-owner")
	if len(r.Tiers) == 0 || len(r.Tiers[0].Agents) < 2 {
		t.Skip("这一档只有一个 agent，测不出均匀性")
	}
	rnd := rand.New(rand.NewSource(42))
	seen := map[string]int{}
	for i := 0; i < 200; i++ {
		a, why := r.Pick(rnd)
		if a == nil {
			t.Fatalf("该选得出人：%s", why)
		}
		seen[a.Slug]++
	}
	if len(seen) < 2 {
		t.Fatalf("同档内该都能被选到（否则就是退化成了固定偏好）：%v", seen)
	}
}

// 选人只在**最高一档**里发生，不会滑到下一档。
func TestRoutePickNeverFallsBack(t *testing.T) {
	r := RouteTask(routeOrg(t), "delivery-ops", "")
	if len(r.Tiers) < 1 {
		t.Skip("没有候选")
	}
	top := map[string]bool{}
	for _, a := range r.Tiers[0].Agents {
		top[a.Slug] = true
	}
	rnd := rand.New(rand.NewSource(7))
	for i := 0; i < 50; i++ {
		a, _ := r.Pick(rnd)
		if a == nil || !top[a.Slug] {
			t.Fatalf("选出来的人不在第一档：%+v", a)
		}
	}
}

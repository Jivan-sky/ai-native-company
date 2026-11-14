package org

import (
	"fmt"
	"math/rand"
	"strings"
)

// Route 是「这件活该派给谁」的候选集与分档。
//
// 形状借自 new-api 的选渠道（分档阶梯 + 档内加权随机），但**语义不同**，三处刻意的差别：
//
//  1. **不自动降档到没资格的人**。档位只表达「更合适」，不表达「可以越过职责」——
//     域不匹配的 agent 根本不进候选，不是排在最后。
//  2. **不重试**。选错人不是"再选一次"，是要显式交接（`anc task handoff`）并留痕。
//     随机只发生在**同一档内**，跨档不会因为"这个人没空"就自动滑到下一档。
//  3. **理由要能说出来**：每个候选带一句「为什么在这一档」，不看代码也能核对。
//
// 现阶段没有权重数据，档内先均匀（借 new-api 那个 `+10` 保底的思路：
// 全 0 时不该退化成"永远选第一个"）。
type Route struct {
	Domain string      `json:"domain"`
	Role   string      `json:"role,omitempty"`
	Tiers  []RouteTier `json:"tiers"`
	// Excluded 是没进候选的 agent 与原因 —— 少了几个人必须能解释。
	Excluded []RouteExcluded `json:"excluded"`
}

// RouteTier 是一档：同一档里的 agent 同样合适。
type RouteTier struct {
	Tier   int          `json:"tier"`
	Why    string       `json:"why"`
	Agents []RouteAgent `json:"agents"`
}

// RouteAgent 是一个候选。Order 是稳定序号，Pick 用它，不按名字重排。
type RouteAgent struct {
	Order int    `json:"order"`
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Role  string `json:"role,omitempty"`
	Why   string `json:"why"`
}

// RouteExcluded 是没进候选的 agent。
type RouteExcluded struct {
	Slug string `json:"slug"`
	Why  string `json:"why"`
}

// TierWhy 是四档的固定说法。写成常量是因为它要出现在看板与 CLI 里，
// 两处各写一遍迟早不一致。
var routeTierWhy = []string{
	"域与岗位都对得上",
	"域对得上",
	"泛用 agent（没挂域）且岗位对得上",
	"泛用 agent（没挂域）",
}

// RouteTask 给出这件活的候选集。纯函数：不写盘、不选人、不打日志。
//
// `domain` 是这件活落在哪块业务（空 = 公司级，谁都可能接）。
// `role` 是这条活要求的岗位（空 = 不限）。
func RouteTask(o *Org, domain, role string) Route {
	domain = strings.TrimSpace(domain)
	role = strings.TrimSpace(role)
	r := Route{Domain: domain, Role: role, Tiers: []RouteTier{}, Excluded: []RouteExcluded{}}
	if o == nil {
		return r
	}
	tiers := make([][]RouteAgent, len(routeTierWhy))
	order := 0
	for _, a := range o.Agents {
		aRole := strings.TrimSpace(a.Role)
		aDomain := strings.TrimSpace(a.Domain)
		roleOK := role == "" || aRole == role
		domainOK := domain == "" || aDomain == domain
		generic := aDomain == ""

		switch {
		case aDomain != "" && !domainOK:
			r.Excluded = append(r.Excluded, RouteExcluded{Slug: a.Slug,
				Why: "挂在 " + aDomain + "，不是这件活所在的 " + domain})
			continue
		case role != "" && !roleOK:
			r.Excluded = append(r.Excluded, RouteExcluded{Slug: a.Slug,
				Why: "代的是岗位 " + firstNonEmptyOrg(aRole, "（没写）") + "，这件活要 " + role})
			continue
		}
		tier := 3
		switch {
		case !generic && roleOK && role != "":
			tier = 0
		case !generic:
			tier = 1
		case roleOK && role != "":
			tier = 2
		}
		order++
		tiers[tier] = append(tiers[tier], RouteAgent{
			Order: order, Slug: a.Slug, Name: firstNonEmptyOrg(a.Name, a.Slug),
			Role: aRole, Why: routeTierWhy[tier],
		})
	}
	for i, list := range tiers {
		if len(list) == 0 {
			continue
		}
		r.Tiers = append(r.Tiers, RouteTier{Tier: i, Why: routeTierWhy[i], Agents: list})
	}
	return r
}

// Pick 在**最高一档里**加权随机选一个。档内权重现在都是 0 → 均匀（+10 保底）。
//
// 不跨档：这一档没人接，返回 nil 与原因，而不是悄悄滑到下一档 ——
// 跨档意味着「降级使用一个不太合适的人」，那该由人决定并留痕。
func (r Route) Pick(rnd *rand.Rand) (*RouteAgent, string) {
	if len(r.Tiers) == 0 {
		return nil, "没有候选：没有哪个 agent 的域/岗位对得上这件活"
	}
	top := r.Tiers[0]
	if len(top.Agents) == 0 {
		return nil, "最高一档是空的"
	}
	// +10 保底：权重全 0 时仍然均匀，不退化成"永远第一个"。
	sum := 0
	for range top.Agents {
		sum += 10
	}
	if sum <= 0 || rnd == nil {
		a := top.Agents[0]
		return &a, ""
	}
	hit := rnd.Intn(sum)
	for _, a := range top.Agents {
		hit -= 10
		if hit < 0 {
			cp := a
			return &cp, ""
		}
	}
	a := top.Agents[len(top.Agents)-1]
	return &a, ""
}

// Human 把候选集写成一行人话（CLI 与看板共用同一句）。
func (r Route) Human() string {
	if len(r.Tiers) == 0 {
		return "没有候选"
	}
	var b strings.Builder
	for i, t := range r.Tiers {
		if i > 0 {
			b.WriteString("；")
		}
		names := make([]string, 0, len(t.Agents))
		for _, a := range t.Agents {
			names = append(names, a.Slug)
		}
		fmt.Fprintf(&b, "第 %d 档（%s）：%s", t.Tier+1, t.Why, strings.Join(names, " / "))
	}
	return b.String()
}

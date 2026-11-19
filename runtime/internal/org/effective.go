package org

// Effective —— 「这个人 / 这个 Agent 现在到底能碰什么」的单一视图（治理判据 2 的 S3）。
//
// 为什么必须有一个单一视图：五条轴分别回答不同问题，客户问一句"这个 bot 能碰什么"，
// 现在要翻 capabilities.md、grants/、domains.md、policies/ 四个地方，还可能翻漏。
// 视图不是新判定，是**把五条轴已有的结论合成一页** —— 所以它不许自己发明规则。
//
// 每条结论都带来源：哪份文件、哪一行/哪一条 grant。**答不出来的写"无法判定"，不写"可以"。**

import (
	"sort"
	"strings"
	"time"
)

// EffectiveView 是合成后的视图。
type EffectiveView struct {
	Who       string           `json:"who"`
	Kind      string           `json:"kind"` // member | agent
	Role      string           `json:"role"`
	Domains   []string         `json:"domains"`         // 可见域；空 + All=false = 看不见任何业务域
	AllDoms   bool             `json:"all_domains"`     // 跨域可见（admin 或 access.cross_domain=visible）
	Caps      []Cap            `json:"caps"`            // 岗位能力（来自 capabilities.md）
	Delegated []EffectiveGrant `json:"delegated"`       // 委派给他的授权，带判定结论
	Notes     []string         `json:"notes,omitempty"` // 说明与缺口
}

// EffectiveGrant 是一条授权的判定结果（复用 Exercise，不重新判）。
type EffectiveGrant struct {
	Slug    string `json:"slug"`
	Action  string `json:"action"`
	Object  string `json:"object"`
	From    string `json:"from"`
	Verdict string `json:"verdict"` // ok | denied | unknown
	Why     string `json:"why,omitempty"`
}

// Effective 合成一个人 / agent 的权限视图。
//
// caps 是能力表（读不到就传空表 = 全拒，与线上同口径）；
// auth/authWired 与 Exercise 完全同源，不在这里另判。
func Effective(o *Org, caps CapTable, auth Authority, authWired bool, who string, at time.Time) (EffectiveView, error) {
	who = strings.TrimSpace(who)
	id, ok := o.Identity(who)
	if !ok {
		return EffectiveView{}, errNoIdentity(who)
	}
	v := EffectiveView{Who: id.Name, Kind: id.Kind, Role: id.Role}

	// 可见性轴：与看板 / 检索同一个实现（Visibility）。
	view := Visibility{Domains: id.Domains}
	if id.Kind == KindMember {
		if mv, found := o.ResolveMemberViewer(id.Ref); found {
			view = mv.Visibility()
		}
	}
	v.AllDoms, v.Domains = view.All, view.Domains
	if !v.AllDoms && len(v.Domains) == 0 {
		v.Notes = append(v.Notes, "看不见任何业务域 —— 只有公司级资料（域为空的那些）")
	}

	// 岗位轴：直接取能力表里这个角色那几行（不做二次推导）。
	for _, c := range caps[strings.ToLower(v.Role)] {
		v.Caps = append(v.Caps, c)
	}
	if len(v.Caps) == 0 {
		v.Notes = append(v.Notes, "能力表里没有岗位 "+v.Role+" 的行 —— 按能力表判定时一律拒绝")
	}

	// 委派轴：筛出 to 匹配这条身份的授权，逐条用 Exercise 判（同一个判定器，不另写）。
	for _, g := range o.Grants {
		if !grantToIdentity(g, id) {
			continue
		}
		verdict := o.Exercise(g, auth, authWired, at)
		v.Delegated = append(v.Delegated, EffectiveGrant{
			Slug: g.Slug, Action: g.Action, Object: g.Object, From: g.From,
			Verdict: verdictWord(verdict), Why: verdict.Reason,
		})
	}
	sort.SliceStable(v.Delegated, func(i, j int) bool { return v.Delegated[i].Slug < v.Delegated[j].Slug })
	if len(v.Delegated) == 0 {
		v.Notes = append(v.Notes, "没有任何委派给他的授权 —— 跨域能力为零")
	}
	if !authWired {
		v.Notes = append(v.Notes, "权威源没接上：每条委派都判「无法判定」，不是「可以」")
	}
	return v, nil
}

// verdictWord 把三态压成一个词：ok / denied / unknown。
// **未知不等于拒绝**，也不等于放行 —— 词必须能分开，否则视图会骗人。
func verdictWord(v Verdict) string {
	switch {
	case v.Unknown:
		return "unknown"
	case v.OK:
		return "ok"
	default:
		return "denied"
	}
}

// grantToIdentity 判一条授权是不是给他的。
// 只认三种写法（与 grants 的 from/to 解析同一个口径）：member:<名> / 裸名字 / agent slug。
func grantToIdentity(g Grant, id Identity) bool {
	to := strings.TrimSpace(g.To)
	if to == "" {
		return false
	}
	if strings.HasPrefix(to, "member:") {
		return strings.EqualFold(strings.TrimPrefix(to, "member:"), id.Ref) ||
			strings.EqualFold(strings.TrimPrefix(to, "member:"), id.Name)
	}
	return strings.EqualFold(to, id.Ref) || strings.EqualFold(to, id.Name)
}

// errNoIdentity 保持与 Identity 同一条口径：解不出 = 这个主体不存在。
func errNoIdentity(who string) error { return &identityError{who: who} }

type identityError struct{ who string }

func (e *identityError) Error() string {
	return "解不出「" + e.who + "」是谁 —— 身份不许自报：必须能在 members/ 或 agents.md 里找到"
}

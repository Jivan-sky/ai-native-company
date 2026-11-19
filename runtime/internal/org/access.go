package org

import "strings"

// 跨域可见性（company.md 的 access.cross_domain）。
//
// 2026-10-19 拍板：**默认可见**。但这只是出厂值，不是硬编码 ——
// 客户想收紧就在 company.md 写 `access.cross_domain: hidden`。
// 之所以做成配置而不是常量：同一条口径在「一家人人互相信任的小公司」和
// 「一个有竞业部门的公司」里答案不同，代码不该替客户选。
const (
	CrossDomainModeVisible = "visible"
	CrossDomainModeHidden  = "hidden"
)

// AccessRules 是 company.md 里与「谁能看见什么」有关的公司级口径。
// 它不随人变，所以放在 Company 下面，而不是塞进每个成员。
type AccessRules struct {
	// CrossDomain 空串合法 —— 它是「没写」，不是「写错」。
	CrossDomain string
}

// ValidCrossDomain 判一个取值认不认识。
func ValidCrossDomain(s string) bool {
	switch s {
	case "", CrossDomainModeVisible, CrossDomainModeHidden:
		return true
	}
	return false
}

// CrossDomainVisible 判这家公司是否允许成员看到自己所属域之外的东西。
func (a AccessRules) CrossDomainVisible() bool {
	return a.CrossDomain != CrossDomainModeHidden
}

// EffectiveCrossDomain 返回生效值：没写就是 visible。
// 给人看的地方（CLI / 看板）用它，免得各自再判一次「空串算哪档」。
func (a AccessRules) EffectiveCrossDomain() string {
	if a.CrossDomain == "" {
		return CrossDomainModeVisible
	}
	return a.CrossDomain
}

// Viewer 是「这个 open_id 在这家公司里是谁、能看见哪些域」的判定结果。
//
// 它只回答「看」。写操作（派任务 / 审批 / 改配置）由调用方按 Admin 与域归属
// 另行判定 —— 那几档口径还没定全（R-12 / #62），不在这里塞一排布尔占位。
type Viewer struct {
	Member  Member
	Admin   bool
	Domains []string // 显式所属域（members/*/persona.md 的 domains）
	// AllDomains = 所有域都可见：管理员，或客户把 access.cross_domain 设成 visible。
	AllDomains bool
}

// ResolveViewer 按飞书 open_id 解访问者。
//
// 白名单制（2026-10-19 拍板）：对不上 members/*/feishu.open_id 一律拒绝，
// 没有「游客」这一档。disabled 的成员同样拒绝 —— 那是离职停用的第一道闸，
// 真正的断链要等 HR 事件（R / D5）。
//
// 管理员有两种写法，任一成立即可：company.admins 里有这个成员，或该成员写 admin: true。
// 两处都认，是为了让「随时把人提成最高权限」既能在公司层集中做，也能在人身上单独做。
func (o *Org) ResolveViewer(openID string) (Viewer, bool) {
	m, ok := o.memberByOpenID(openID)
	if !ok || m.Disabled {
		return Viewer{}, false
	}
	return o.viewerOf(m), true
}

// ResolveMemberViewer 按**成员编号**解访问者（CLI / MCP 的 who 入口用）。
//
// 与 ResolveViewer 共用同一个判定函数：入口不同（编号 / open_id），答案必须一样 ——
// 「命令行说他是管理员、检索说不是」这种漂移，比没有权限判定更坏。
func (o *Org) ResolveMemberViewer(name string) (Viewer, bool) {
	m, ok := o.Member(name)
	if !ok || m.Disabled {
		return Viewer{}, false
	}
	return o.viewerOf(m), true
}

func (o *Org) viewerOf(m Member) Viewer {
	v := Viewer{
		Member:  m,
		Domains: append([]string(nil), m.Domains...),
		Admin:   m.Admin || o.isAdminName(m.Name),
	}
	v.AllDomains = v.Admin || o.Company.Access.CrossDomainVisible()
	return v
}

// memberByOpenID 是「open_id → 人」的唯一实现。
// ByOpenID（认人）与 ResolveViewer（判进门）都走这里 —— 两份实现迟早会漂，
// 而漂出来的差别会表现成「同一个人，两处认成两个身份」。
func (o *Org) memberByOpenID(openID string) (Member, bool) {
	if o == nil {
		return Member{}, false
	}
	id := strings.TrimSpace(openID)
	if id == "" {
		return Member{}, false
	}
	for _, m := range o.Members {
		if strings.TrimSpace(m.Feishu.OpenID) == id {
			return m, true
		}
	}
	return Member{}, false
}

func (o *Org) isAdminName(name string) bool {
	for _, a := range o.Company.Admins {
		if a == name {
			return true
		}
	}
	return false
}

// CanSeeDomain 判这个访问者能不能看见某个域。
//
// slug 为空 = 这份东西不属于任何域（公司级资料，比如 company.md 自己）：
// 登录进来的人都能看 —— 它本来就渲染在每个人的 persona 里，拒绝它只会让人打不开首页。
func (v Viewer) CanSeeDomain(slug string) bool {
	// 转给 Visibility.Allows —— 判据 2 的第一步：**同一条口径只留一份实现**。
	return v.Visibility().Allows(slug)
}

// Visibility 是「这次能看见哪些域」的**唯一实现**。
//
// 为什么要有这个类型：看板用 Viewer.CanSeeDomain、检索用 retrieval.Scope.Allows，
// 两处各写一遍同样的循环。这正是 SPEC §15.4 说的「判定点散落」——
// 两份实现迟早给出两个答案，而"命令行说能看、看板说不能看"比没有权限更坏。
//
// 口径（三处必须一致，任何一处改动都要同步测试）：
//   - 空 slug = 公司级资料（company.md 这类），登录的人都能看；
//   - All = 跨域可见（admin 或 access.cross_domain=visible）；
//   - 否则按 Domains 白名单。
type Visibility struct {
	All     bool
	Domains []string
}

// Allows 判一个域（或空域）在不在可见范围里。
func (v Visibility) Allows(slug string) bool {
	if slug == "" || v.All {
		return true
	}
	for _, d := range v.Domains {
		if d == slug {
			return true
		}
	}
	return false
}

// Visibility 把访问者转成统一形状。
func (v Viewer) Visibility() Visibility { return Visibility{All: v.AllDomains, Domains: v.Domains} }

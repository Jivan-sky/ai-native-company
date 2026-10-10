package org

import "strings"

// Identity 是接入面上的**主体** —— 成员 bot 与业务 agent 共用同一种形状。
//
// 为什么要合起来（2026-10-10）：两类主体接在同一张口上（`anc_read_context` / 信封），
// 在此之前 `who` 只认 members/ —— 业务 agent **出站被硬拒**（「我们公司没有这个人」）、
// 入站只标一条 warn，两边还不一样。同一件事两套解析，迟早对不上号（本仓库付过学费的那类）。
//
// 分辨一个主体要三样，缺一就「认不出这是谁」：
//   - Ref：**编号身份** —— 成员名 / 业务 agent 的 slug。不靠自报，来自真相源。
//   - Kind：哪一类 —— member / agent。失败代价不同（SPEC §2.3：成员 bot 答错人还能兜，
//     业务 bot 处理错单要赔钱），所以回话里要能分开。
//   - Domains：**归属** —— 挂在哪块业务（业务 agent 的 domain 列；成员在 persona 的 domains）。
//   - Inscription：**铭文** —— 干什么的 / 替谁干。业务 agent 取 agents.md 的 description；
//     成员 bot 这一格留空（它代人，来源是 members/<名>/persona.md，不在这张表上）。
type Identity struct {
	Kind        string
	Ref         string
	Name        string
	Role        string
	Domains     []string
	Inscription string
	Line        int
}

// Kind 的两个取值。写死成常量：回话里会当字段发给 harness，拼错就等于身份说不清。
const (
	KindMember = "member"
	KindAgent  = "agent"
)

// Identity 按**编号**取主体：先成员、后业务 agent。
//
// 顺序写死（同 Resolves 的做法）：同一份输入永远同一个答案，不是猜。
// 两类主体的编号撞车在真相源里已经是红线（agent.slug.collides_member），
// 所以这里不会出现「两个都命中」的合法情形。
//
// 判不出来 = **这个主体不存在**，不是「参数写错了」—— 调用方的拒话照这个口径写。
func (o *Org) Identity(ref string) (Identity, bool) {
	if o == nil {
		return Identity{}, false
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Identity{}, false
	}
	if m, ok := o.Member(ref); ok {
		return Identity{
			Kind: KindMember, Ref: m.Name, Name: m.DisplayName,
			Role: m.Role, Domains: m.Domains,
		}, true
	}
	if a, ok := o.Agent(ref); ok {
		var domains []string
		if a.Domain != "" {
			domains = []string{a.Domain}
		}
		return Identity{
			Kind: KindAgent, Ref: a.Slug, Name: a.Name,
			Role: a.Role, Domains: domains,
			Inscription: a.Description, Line: a.Line,
		}, true
	}
	return Identity{}, false
}

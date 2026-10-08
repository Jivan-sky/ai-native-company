package envelope

import (
	"strings"

	"anc/internal/org"
)

// Bind 把信封绑到真相源上：`who` / `on_behalf_of` / `scope` 指向的东西**到底存不存在**。
//
// 为什么和 Parse 分开：结构错（读不成信封）与指向错（指向了一个不存在的人 / 域）是两类事 ——
// 前者信封不成立，后者信封成立但**身份可疑**。混在一起报，人分不清「你格式写错了」
// 和「我们公司没有这个人」。
//
// 档次：整组默认 warn。这不是我随口定的 —— 域表 / 项目表立的就是这个先例
// （新能力先「看见」、不先「拦」）。想变红：company.md 的 policy 段写
// `envelope.who.unknown = fatal`。**门禁是数据，不是这里的 if。**
func Bind(o *org.Org, e Envelope, p *org.Policy) []org.Issue {
	if o == nil {
		return nil
	}
	if p == nil {
		p = org.DefaultPolicy()
	}
	const where = "信封"
	// 走 Report 而不是裸切片：Report.Add 会**丢掉 off 档**（关掉一条规则 = 一条发现都不出）
	// 并去掉逐字重复 —— 这两件事都不该在调用方各写一遍。
	rep := &org.Report{}

	// who：必须是真相源里的一个成员（含 bot）。身份不许自报 —— 这是 #44 的验收判据之一。
	if w := strings.TrimSpace(e.Who); w != "" {
		if _, ok := o.Member(w); !ok {
			rep.Add(p.Issue("envelope.who.unknown", where,
				"who=%q 不是任何成员 —— 身份不许自报，必须能在 members/ 里解出来", w))
		}
	}

	// on_behalf_of：bot 自身的权限是所代理对象权限的投影（SPEC §2.3），所以署名缺了，
	// 审计就答不出「谁授权的」。空与解不出分开报。
	ob := strings.TrimSpace(e.OnBehalfOf)
	switch {
	case ob == "":
		rep.Add(p.Issue("envelope.on_behalf_of.missing", where,
			"没写 on_behalf_of —— 审计要靠它答出「谁在什么时候授权谁做了什么」（SPEC §6）"))
	case !Resolves(o, ob):
		rep.Add(p.Issue("envelope.on_behalf_of.unknown", where,
			"on_behalf_of=%q 解不出人 / 岗位 / 业务域（写法：member:名字 | role:岗位 | domain:slug，或直接写名字）", ob))
	}

	// scope：域 / 项目都必须是表里的 slug。
	// **表为空就不报**：没有 domains.md / projects.md 的存量 vault 不该因为「发了封信」被提醒 ——
	// 门禁不许长在别人的文档上（同 LoadDomains / LoadProjects 的谦让）。
	if d := strings.TrimSpace(e.Scope.Domain); d != "" && len(o.Domains) > 0 && !hasDomain(o, d) {
		rep.Add(p.Issue("envelope.scope.domain.unknown", where,
			"scope.domain=%q 不在 domains.md 里 —— 这块业务不存在，授权无从落脚", d))
	}
	if pr := strings.TrimSpace(e.Scope.Project); pr != "" && len(o.Projects) > 0 && !hasProject(o, pr) {
		rep.Add(p.Issue("envelope.scope.project.unknown", where,
			"scope.project=%q 不在 projects.md 里 —— 多半是拼错，或这一项还没抽成表行", pr))
	}

	// kind：认不出的原样保留，只提醒（词表不锁死，见包注释 2）。
	switch k := strings.TrimSpace(e.Kind); {
	case k == "":
		rep.Add(p.Issue("envelope.kind.missing", where,
			"没写 kind —— 收件人得自己猜这是请示还是汇报"))
	case !KnownKind(k):
		rep.Add(p.Issue("envelope.kind.unknown", where,
			"kind=%q 不在已知词表（ask / report / notify / ingest / proposal）—— 照收，只是归类不了", k))
	}

	return rep.Issues
}

// Resolves 判 on_behalf_of 指不指向真东西。
//
// 支持 `member:x` / `role:x` / `domain:x` 前缀，也接受**裸名字** —— 裸名字按固定的
// 顺序解（成员 → 岗位 → 业务域），顺序写死是为了同一份输入永远同一个答案，不是猜。
//
// 导出（W2）：出站读出口（anc_read_context）复用它判 on_behalf_of。**身份解析只有这一套** ——
// 另开一套迟早会对不上号。
func Resolves(o *org.Org, s string) bool {
	kind, name := org.SplitRef(s)
	if name == "" {
		return false
	}
	switch kind {
	case "member":
		_, ok := o.Member(name)
		return ok
	case "role":
		_, ok := o.Roles[name]
		return ok
	case "domain":
		return hasDomain(o, name)
	case "":
		if _, ok := o.Member(name); ok {
			return true
		}
		if _, ok := o.Roles[name]; ok {
			return true
		}
		return hasDomain(o, name)
	default:
		// 认不出的前缀不是「错误」，是解不出 —— 走 same 一条 warn，文案里给了写法。
		return false
	}
}

func hasDomain(o *org.Org, slug string) bool {
	for _, d := range o.Domains {
		if d.Slug == slug {
			return true
		}
	}
	return false
}

func hasProject(o *org.Org, slug string) bool {
	for _, pr := range o.Projects {
		if pr.Slug == slug {
			return true
		}
	}
	return false
}

package envelope

import (
	"strings"

	"anc/internal/org"
)

// ---------- 授权执行层第一刀：跨域信封进不进得来 ----------
//
// 在此之前，授权表只有**落点与结构校验**（`internal/org/grants.go`）—— 口径齐了，
// 「行使」这一层一行代码都没有。这一刀把口径变成关卡，落点就在信封入口上：
// `anc envelope check` 与 `anc envelope serve` 走的是**同一支**（同源，不存在两套判法）。
//
// 判什么：这封信的（主体，客体）有没有一条 grant 覆盖。
//
//   - **只判跨域**。没写 `scope`、或写的是自己那一摊 → 不通 grant。
//     SPEC §6-12：「没有 grant 就是不通 —— 跨业务域、跨 bot、看板分区同理」；
//     反过来读就是：**自己域内本来就不需要 grant**。这条与出站读出口的跨域拒法同一个口径，
//     不新造第二套。
//   - **主体** = `on_behalf_of` 优先、退回 `who` —— SPEC §2.3：bot 不自有权限，
//     它的权是所代理对象权限的**投影**。两处都用**同一支** `o.Identity` 解析。
//   - **客体** = `scope.domain` 的 slug。
//   - **动作这一维还没定**（信封里没有 action 字段）：这一刀**只判客体**。
//     不在没有定义处的地方发明词表 —— 同 `grants.go` 对 `to` / `object` 的留白。
//
// 档次：出厂 `fatal`、**不锁死**。SPEC §6 不变量 1「默认拒」是硬口径，所以这一条与
// 结构校验那批「先看见不先拦」的先例**不同**：它出厂就是拦。要松开 —— 在 `company.md` 写
// `policy: grant.match.missing = warn|off`，存量客户照样能先看见。**门禁是数据，不是这里的 if。**
//
// 一条事实只报一处：主体 / 客体**解不出来**不在这里报 —— 那是 `Bind` 的
// `envelope.who.unknown` / `on_behalf_of.unknown` / `scope.domain.unknown` 的活。
// 这里只答「有没有授权」这一个问题。
//
// 不查 `from`：级联失效（人一走整条授权链当刻断）是 W4 后面那格的事，
// 这一刀只做「无匹配即拒」。
// ---------- 授权的「产生」这一半：现在没有 ----------
//
// 上面这一支只回答「这条 grant 覆不覆盖这次行使」—— 它**消费**授权表。
// 授权**怎么产生**（提案 → 人点头 → 落成 grants/ 一个文件）现在**没有落到代码**，口径先钉住：
//
//   - **出厂口径 = 一律要人点头。** 没有例外、没有阈值、没有 automatic 分支。
//   - **「权限大 / 小」这条分界线不许写进代码**：写成阈值 = 把**放权的分界**钉死在实现里，
//     比其它过拟合更危险（定宽了等于自动开门）。它将来只能是**数据**，且必须由
//     **拥有那块业务的人**写下（SPEC §6「谁给的，自己必须先有这个能力」）—— 否则
//     「自动批小权」会成为 ANC 自己长出来的权力来源。
//   - **预留入口（现在不实现，只记指针）**：域表加一列声明「这一档要不要人拍」＋ 规则表一条兜底
//     （出厂 off）。改的时候动的是**数据与规则表**，不是这里的 if。
//   - **自动下放要先满足灰度**：ANC 真正嵌进企业、跑够一段灰度之后才开。
//
// 为什么这次不做：期限那一格刚因为「替不存在的需求定语法」被拍掉 —— 同一条教训。
// 授权**产生**的真实用法还没有样本，先按「一律人点头」这条不会错的口径走。见 GitHub #62。
// requestingKinds 是「**请求**而不是行使」的 kind 表。
//
// 提案（`proposal`）的定义就是「agent 只能提、不能直接行使」（#34）—— 它**不能**靠 grant 才能提：
// 还没拿到权的人当然没有 grant，拿 grant 当提案的门就等于**提案永远提不出来**。
// 门禁拦的是「把数据拿出去 / 写进去」；提案里**没有任何业务数据**，只是一句请求。
// 所以这一支**不判授权、不报 `grant.match.missing`**，但它**照样是一次跨域请求** → 留痕照记。
//
// 这是一张**数据表**，不是第二个 `if`：将来若还有哪个 kind 也属「请求而非行使」，加在这里。
// `ask` **不在**此列 —— 它可能带着数据去问，算不算行使要单独判，不搭便车。
//
// 不给它配档位开关：这不是「门的松紧」，是**门的适用范围**（提案不是行使）。松紧才是数据。
var requestingKinds = map[string]bool{KindProposal: true}

type GateOutcome struct {
	Subject     string // 按谁的权限判的（on_behalf_of 优先，退回 who）；空 = 主体解不出
	OnBehalfOf  string // 原样带出，留痕要用
	Domain      string // scope.domain；空 = 这封信不通 grant
	CrossDomain bool   // true = 这次是跨域（有覆盖或被拒都算）
	Grant       string // 命中的那条 grant 的落点（vault 相对路径）；没命中为空
	Requesting  bool   // true = 这一封是**请求**（提案），不是行使：不判授权，但仍算一次跨域请求（要留痕）
}

// Gate 判一封信的授权。返回发现 + 这次的结论（结论给调用方留痕用 ——
// 「行使必留痕」要有痕可留，才谈得上留痕）。
//
// 返回的发现**没有**在这里过 Report 的 off 档过滤 —— 走 Report 是为了与 Bind 同一套
// 「off 档一条都不出、逐字重复只留一条」的行为。
func Gate(o *org.Org, e Envelope, p *org.Policy) ([]org.Issue, GateOutcome) {
	var out GateOutcome
	if o == nil {
		return nil, out
	}
	if p == nil {
		p = org.DefaultPolicy()
	}
	out.OnBehalfOf = strings.TrimSpace(e.OnBehalfOf)

	dom := strings.TrimSpace(e.Scope.Domain)
	if dom == "" {
		// 没写 scope = 说的是自己那一摊 = 域内，不通 grant。
		return nil, out
	}
	out.Domain = dom

	ref, idn, ok := gateSubject(o, e)
	if !ok {
		// 主体解不出：这封信连「按谁的权限」都答不了。归 Bind 报，这里不报第二遍。
		return nil, out
	}
	out.Subject = ref

	if ownsDomain(idn, dom) {
		return nil, out // 自己域内：不需要 grant
	}
	out.CrossDomain = true

	// 请求（提案）不是行使：不判授权 —— 但还是「一次跨域请求」，CrossDomain 留着好让调用方留痕。
	if requestingKinds[strings.ToLower(strings.TrimSpace(e.Kind))] {
		out.Requesting = true
		return nil, out
	}

	for _, g := range o.Grants {
		if grantCovers(g, ref, idn, dom) {
			out.Grant = g.Path
			return nil, out
		}
	}

	rep := &org.Report{}
	rep.Add(p.Issue("grant.match.missing", "信封",
		"跨域信封：scope.domain=%q 不在 %s 负责的域里，而 grants/ 里没有任何一条覆盖它 —— "+
			"**数据默认不通**（SPEC §6-12）。要通就写一条 grant（grants/ 下一个文件一条，"+
			"from / to / action / object，写法见 DESIGN.md §7.1.15），或按「找谁」接头走人。"+
			"（这一刀只判了客体：动作那一维还没定。）", dom, ref))
	return rep.Issues, out
}

// gateSubject 定「这一封信是按谁的权限在走」。
//
// `on_behalf_of` 优先（SPEC §2.3：bot 不自有权限），解得出就用它；没写或解不出，退回 `who` 自己。
// 两处走**同一支** `o.Identity`（成员 / 业务 agent 共用），不另开一套 —— 这正是 2026-10-10
// 修掉的那类「一边认一边不认」。
//
// 一处刻意窄：`on_behalf_of` 写成岗位（`role:manager`）或域（`domain:trade`）时，
// 这一刀**退回按 `who` 判**。理由是岗位 / 域级的授权匹配（`to` 的客体词表）还没有定义处，
// 在这里发明一套就是猜。窄的这一条写在明面上，不藏在行为里。
func gateSubject(o *org.Org, e Envelope) (string, org.Identity, bool) {
	if ob := strings.TrimSpace(e.OnBehalfOf); ob != "" {
		if kind, name := org.SplitRef(ob); kind == "" || kind == "member" {
			if idn, ok := o.Identity(name); ok {
				return idn.Ref, idn, true
			}
		}
	}
	if w := strings.TrimSpace(e.Who); w != "" {
		if idn, ok := o.Identity(w); ok {
			return idn.Ref, idn, true
		}
	}
	return "", org.Identity{}, false
}

// ownsDomain 判这一格域是不是主体的「自己那一摊」。成员 bot 的域来自 persona，
// 业务 agent 的域来自 agents.md 的 domain 列 —— Identity 已经把两者归一成 Domains（同一把尺子）。
func ownsDomain(idn org.Identity, dom string) bool {
	for _, d := range idn.Domains {
		if d == dom {
			return true
		}
	}
	return false
}

// grantCovers 判一条 grant 覆不覆盖这次行使（主体 + 客体）。
//
// **客体**：`object` 认两种写法 —— 裸 slug（`trade`）与带前缀（`domain:trade`），
// 走的是 `from` / `to` 同一支 `org.SplitRef`（不另开一套引用语法）。
// 别的形态（bot / 通道 / 看板分区）**不算命中**：它们的词表还没有定义处。
//
// **主体**：`to` 认三条 —— 与主体编号完全相等（业务 agent 的 slug 走这条）、
// `member:<名>`、`role:<岗位>`，外加裸名字按「先成员后岗位」的固定顺序解（同 `Resolves`）。
func grantCovers(g org.Grant, ref string, idn org.Identity, dom string) bool {
	if !objectIsDomain(g.Object, dom) {
		return false
	}
	return toCoversSubject(g.To, ref, idn)
}

func objectIsDomain(object, dom string) bool {
	object = strings.TrimSpace(object)
	if object == "" {
		return false
	}
	switch kind, name := org.SplitRef(object); kind {
	case "", "domain":
		return name == dom
	}
	return false
}

func toCoversSubject(to, ref string, idn org.Identity) bool {
	to = strings.TrimSpace(to)
	if to == "" {
		return false
	}
	if to == ref {
		return true
	}
	switch kind, name := org.SplitRef(to); kind {
	case "member":
		return name == ref
	case "role":
		return idn.Role != "" && name == idn.Role
	case "":
		// 裸名字：与 Resolves / hasActor 同一套固定顺序（先成员，后岗位），顺序写死不是猜。
		if name == ref {
			return true
		}
		return idn.Role != "" && name == idn.Role
	}
	return false
}

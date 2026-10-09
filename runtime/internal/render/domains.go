package render

import "anc/internal/org"

// DomainRow / DomainBrief / VisibleDomains —— 「谁看得见哪些域」的**唯一一把尺子**。
//
// 至少两个消费方共用它，不许各写一套（各算各的迟早对不上号 —— 2026-10-09 探针那次
// 「只改了一个消费方」的教训还热着）：
//
//   - persona 段 8：agent 开工时随上下文一起拿到（见 persona.go 的 domainSection）；
//   - 出站读出口：agent 主动要（W2 的 anc_read_context）—— 桥的另一半。
//
// 边界照抄 SPEC §6「数据默认不通」，一个字都没新造：
//
//   - **自己的域**：全行 —— 是什么 / 数据在哪 / 找谁（另附口径与原件来源）。这一域的事得干得了；
//   - **别人的域**：只有 name / what / who。别人的 data 与 terms **不给** ——
//     要跨域就按「找谁」接头，走人，不走近道。
//
// Line 是 domains.md 里的行号，只为定位（报错 / 证据指针），不是业务数据。
type DomainRow struct {
	Slug    string
	Name    string
	What    string
	Data    string
	Who     string
	Terms   string
	Sources string
	Line    int
}

// DomainBrief 是别域的可见面：只有名称 / 是什么 / 找谁。
type DomainBrief struct {
	Slug string
	Name string
	What string
	Who  string
	Line int
}

// VisibleDomains 按成员所属域把域表切成两半。
//
// Who 已经过 o.WhoLabel（岗位 → 「人名（岗位）」）—— 调用方拿到的是能直接给人看的那一份。
// o == nil 时返回空（同 LoadDomains 的谦让：没有域表不是错，是还没划域）。
func VisibleDomains(o *org.Org, m org.Member) (mine []DomainRow, others []DomainBrief) {
	return visibleDomains(o, m.Domains)
}

// visibleDomains 是上面那把尺子的本体：**喂它的是「这个主体看得见哪些域」**，不是「它是谁」——
// 成员 bot 代人、业务 agent 代岗位（SPEC §2.3），两类主体在这里没有区别。
func visibleDomains(o *org.Org, domains []string) (mine []DomainRow, others []DomainBrief) {
	if o == nil {
		return nil, nil
	}
	owned := make(map[string]bool, len(domains))
	for _, s := range domains {
		owned[s] = true
	}
	for _, d := range o.Domains {
		if owned[d.Slug] {
			mine = append(mine, DomainRow{
				Slug: d.Slug, Name: d.Name, What: d.What, Data: d.Data,
				Who: o.WhoLabel(d.Who), Terms: d.Terms, Sources: d.Sources, Line: d.Line,
			})
			continue
		}
		others = append(others, DomainBrief{
			Slug: d.Slug, Name: d.Name, What: d.What, Who: o.WhoLabel(d.Who), Line: d.Line,
		})
	}
	return mine, others
}

// RefuseDomainText 是「这块业务不在你的可见范围」这句话的唯一写法。
//
// 两件事一起给：**拒**（不给 data / terms）与**指路**（按「找谁」接头，SPEC §6）。
// 只拒不指路，agent 会转头去问人 —— 那正是 W2 要消掉的那一步。
func RefuseDomainText(slug, who string) string {
	if who == "" || who == "—" {
		return "「" + slug + "」不在你的可见范围（数据默认不通），而且域表里没写找谁 —— 先让人补 domains.md 的 who 列"
	}
	return "「" + slug + "」不在你的可见范围（数据默认不通）。要办这块业务，找 " + who + " —— 按「找谁」接头，不走近道"
}

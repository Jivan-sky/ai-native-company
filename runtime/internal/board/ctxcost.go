package board

import (
	"encoding/json"

	"anc/internal/ctxpack"
)

// 上下文装配的「账」：每一段占多少、依据是什么、被挡在哪。
//
// 为什么要有它：上下文治理最容易变成"看看它给了什么"。但真正要回答的是
// **为什么只拿到这些**、以及**这次装配有多贵**。没有这两样，
// 装配器想给多少给多少，最后一定演变成"把整个 vault 塞进去"。
//
// chars 用**序列化后的字符数**而不是 token：token 依赖具体分词器，
// 在这里算一个近似值反而会让人当真。字符数可核对、不撒谎；要 token 等计量接上。

// SectionCost 是一段的账。
type SectionCost struct {
	Name  string `json:"name"`  // 中文段名，直接上屏
	Key   string `json:"key"`   // 契约里的字段名，便于对账
	Chars int    `json:"chars"` // 这一段序列化后的字符数（0 = 这段是空的）
	Items int    `json:"items"` // 几条；不适用为 0
	Why   string `json:"why"`   // 依据哪条规则 / 为什么是空的
}

// CostReport 是整份上下文的账。
type CostReport struct {
	Sections []SectionCost `json:"sections"`
	Total    int           `json:"total"`
	Note     string        `json:"note"`
}

// costOf 把一份装配结果拆成段账。纯函数：只读 Out，不碰磁盘。
func costOf(o *ctxpack.Out) CostReport {
	rep := CostReport{
		Sections: []SectionCost{},
		Note: "字符数是**序列化后**的实际长度，不是 token 估算 —— " +
			"token 依赖具体分词器，这里算一个近似值只会让人当真。",
	}
	if o == nil {
		return rep
	}
	add := func(name, key string, v any, items int, why string) {
		n := 0
		if v != nil {
			if b, err := json.Marshal(v); err == nil {
				n = len(b)
			}
		}
		rep.Sections = append(rep.Sections, SectionCost{Name: name, Key: key, Chars: n, Items: items, Why: why})
		rep.Total += n
	}
	add("身份", "identity", o.Identity, 0, "从 members.md / agents.md 解出来的那一条；编号不由调用方自报")
	add("业务域", "domains", o.Domains, len(o.Domains), "只出你负责的域；别人的域只留名称与找谁（见下一段）")
	add("别域目录", "directory", o.Directory, len(o.Directory), "只有名称 / 是什么 / 找谁，不给正文 —— 跨域要 grant")
	add("技能", "skills", o.Skills, len(o.Skills), "岗位声明的技能；正文在不在逐条标")
	add("项目", "projects", o.Projects, len(o.Projects), "挂在你可见域上的项目")
	add("待办", "todo", o.Todo, len(o.Todo), "热层里 to == 你的那几条；没接热层时这段是空的（不是没人派给你）")
	add("我在做", "doing", o.Doing, len(o.Doing), "热层里 by == 你的那几条")
	if o.Grants != nil {
		add("授权", "grants", o.Grants, len(o.Grants.Rows),
			"只出与你相关的几条（from / to / on_behalf_of 指着你的），不是全表")
	} else {
		add("授权", "grants", nil, 0, "这一段没装配（或全表为空）")
	}
	add("答不了的", "gaps", o.Gaps, len(o.Gaps), "如实报缺：这一段本来就是「我们答不了什么」")
	if o.Refused != nil {
		add("拒绝理由", "refused", o.Refused, 0, "被拒时给理由，不给数据")
	}
	return rep
}

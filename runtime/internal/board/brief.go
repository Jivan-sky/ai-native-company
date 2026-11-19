package board

// Brief —— 一条待批/任务对**下游 agent** 的最低可判材料（判据 10'）。
//
// 为什么放在服务端而不是前端：这个产品的主消费者是 agent，不是眼睛。
// agent 读 /api/approvals 时应当**不需要推理**就能拿到五问的答案；
// 缺什么就显式写缺什么，让它（和它的人类）知道去补哪一块。
//
// 五问（来自治理判据 10'）：
//
//	我要做什么 / Agent 做什么 / 我不能做什么 / 卡住找谁 / 错了怎么办
//
// 硬纪律：**不许编**。没有数据就写进 Missing，不拿默认话术糊上去。

import "strings"

// BriefAnswer 是五问里的一问。
type BriefAnswer struct {
	// Value 是从真实数据里得到的答案；空 = 答不出。
	Value string `json:"value,omitempty"`
	// From 是这条答案的来源字段（便于 agent 回指，也便于人查它凭什么这么说）。
	From string `json:"from,omitempty"`
}

// Brief 是一条待批的"人机分工简报"。
type Brief struct {
	What       BriefAnswer `json:"what"`              // 我要做什么
	AgentDoes  BriefAnswer `json:"agent_does"`        // Agent 做什么
	MustNot    BriefAnswer `json:"must_not"`          // 我不能做什么
	EscalateTo BriefAnswer `json:"escalate_to"`       // 卡住找谁
	IfWrong    BriefAnswer `json:"if_wrong"`          // 错了怎么办
	Missing    []string    `json:"missing,omitempty"` // 哪几问答不出、为什么
}

// briefOf 从已有字段合成简报。只做**搬运与拼接**，不做推断。
func briefOf(p PendingView) Brief {
	b := Brief{
		What:    BriefAnswer{Value: firstNonEmptyStr(p.Title, ""), From: "title"},
		MustNot: BriefAnswer{Value: "不能替我签字；已签的不改，要改就重新提一次", From: "policy:approvals"},
		IfWrong: BriefAnswer{Value: "撤回并重新提一次；撤回会留痕，不抹历史", From: "policy:approvals"},
	}
	if p.Who != "" {
		b.AgentDoes = BriefAnswer{Value: p.Who + " 收集材料、核对、生成结论，交回给你决定", From: "who"}
	}
	if p.To != "" {
		b.EscalateTo = BriefAnswer{Value: "该 " + p.To + " 批；批不了就挂起并写明在等谁", From: "to"}
	}

	// 缺什么如实写 —— 这是"料够不够判"的体检，不是文案
	if strings.TrimSpace(p.Title) == "" {
		b.Missing = append(b.Missing, "没有标题：不知道要批什么")
	}
	if strings.TrimSpace(p.Body) == "" {
		b.Missing = append(b.Missing, "没有正文：看不到提案原文，无法判断依据")
	}
	if strings.TrimSpace(p.Domain) == "" && strings.TrimSpace(p.Project) == "" {
		b.Missing = append(b.Missing, "没有域/项目：不知道这件事落在哪块业务")
	}
	if strings.TrimSpace(p.To) == "" {
		b.Missing = append(b.Missing, "没有指定审批岗位：不知道该谁批")
	}
	return b
}

func firstNonEmptyStr(xs ...string) string {
	for _, x := range xs {
		if s := strings.TrimSpace(x); s != "" {
			return s
		}
	}
	return ""
}

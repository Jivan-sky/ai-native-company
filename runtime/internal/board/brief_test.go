package board

import "testing"

// 判据 10'：下游 agent 读一条待批，应当**不用推理**就能拿到五问；
// 缺什么必须显式写进 Missing —— 不许编。
func TestBriefAnswersFiveQuestionsFromRealFields(t *testing.T) {
	p := PendingView{ID: "p1", Who: "acme-devbot", To: "manager", Domain: "trade",
		Title: "把 Q3 对账单落到 domains/trade/ledger.md", Body: "正文"}
	b := briefOf(p)

	if b.What.Value == "" || b.What.From != "title" {
		t.Fatalf("我要做什么：该来自 title，拿到 %+v", b.What)
	}
	if b.AgentDoes.Value == "" || b.AgentDoes.From != "who" {
		t.Fatalf("Agent 做什么：该来自 who，拿到 %+v", b.AgentDoes)
	}
	if b.EscalateTo.Value == "" || b.EscalateTo.From != "to" {
		t.Fatalf("卡住找谁：该来自 to，拿到 %+v", b.EscalateTo)
	}
	if b.MustNot.Value == "" || b.IfWrong.Value == "" {
		t.Fatal("不能做什么 / 错了怎么办：来自 policy，不该为空")
	}
	if len(b.Missing) != 0 {
		t.Fatalf("字段齐全时不该报缺，拿到 %v", b.Missing)
	}
}

// 缺料要如实写缺，而不是拿默认话术糊过去。
func TestBriefReportsMissingFields(t *testing.T) {
	p := PendingView{ID: "p2"} // 只有一个 id，其余全空
	b := briefOf(p)
	if len(b.Missing) < 4 {
		t.Fatalf("标题/正文/域/审批岗位都缺，该报 4 条，拿到 %v", b.Missing)
	}
	if b.What.Value != "" {
		t.Fatalf("没有标题时不该编一个答案，拿到 %q", b.What.Value)
	}
}

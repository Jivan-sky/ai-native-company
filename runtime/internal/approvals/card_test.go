package approvals

import (
	"strings"
	"testing"
	"time"

	"anc/internal/card"
)

func doneCase(t *testing.T, sig Signal) (Pending, Decision) {
	t.Helper()
	p := Pending{
		ID: "prop-card-1", Who: "tradebot", To: "manager",
		Domain: "trade", Project: "import-q4",
		Title: "member:alice 申请 trade 的权", Body: "请开 trade 域的读权：import-q4 这批合同要对一下台账，只读，不写。",
		Enqueued: "2026-10-10T15:35:36Z", Status: StatusPending,
	}
	d, err := New(p.ID, "alice", sig, "", time.Date(2026, 10, 11, 0, 20, 12, 0, time.UTC))
	if err != nil {
		t.Fatalf("建一次点头：%v", err)
	}
	d.Via = "飞书卡片 · 点的人 alice（Alice · ou_demo_alice）"
	return p, d
}

// 收口的颜色跟着**落库那一行**写的状态词走 —— 不另编一套（颜色只此一处说了算）。
// 「同意」与「驳回」落库都是 done → 都绿；结论在 title / verdict 那两格上分得开。
func TestDoneFacts_颜色跟着落库那一行(t *testing.T) {
	for _, sig := range []Signal{SignalApprove, SignalReject, SignalHold} {
		p, d := doneCase(t, sig)
		f := DoneFacts(nil, p, d)
		if f["status"] != StatusDone {
			t.Errorf("%s：status = %q，want %q（落库那时写的就是这个）", sig, f["status"], StatusDone)
		}
		if f["tone"] != "green" {
			t.Errorf("%s：tone = %q，want green（done → 绿）", sig, f["tone"])
		}
		if f["verdict"] != d.Verdict() {
			t.Errorf("%s：verdict = %q，want %q", sig, f["verdict"], d.Verdict())
		}
		if f["at"] != "2026-10-11T00:20:12Z" {
			t.Errorf("%s：at = %q", sig, f["at"])
		}
		if f["by"] != "alice" {
			t.Errorf("%s：by = %q", sig, f["by"])
		}
	}
}

// 收口帧渲染出来：结论进标题、正文与「谁点的」都在、**没有按键**。
func TestDoneCard_结论进标题_没有按键(t *testing.T) {
	p, d := doneCase(t, SignalReject)
	c, notes := p.DoneCard(nil, d, card.DefaultDone())
	if len(notes) != 0 {
		t.Errorf("槽位给齐了还喊：%v", notes)
	}
	if !strings.Contains(c.Header.Title, "驳回") {
		t.Errorf("标题 = %q —— 结论要出现在标题上", c.Header.Title)
	}
	if !strings.Contains(c.Text(), "只读，不写") {
		t.Errorf("正文没带上原文：%s", c.Text())
	}
	for _, el := range c.Elements {
		if el.Kind == card.KindButtons {
			t.Fatal("收口帧渲染出来还有按键 —— 收口的意思就是不能再点")
		}
	}
}

// 帧要的槽位值没给，渲染要**喊出来**（不是静默空着）。
func TestDoneCard_槽位缺了要喊(t *testing.T) {
	p, d := doneCase(t, SignalApprove)
	_, notes := p.DoneCard(nil, d, card.Frame{
		Schema: card.Schema, Name: "坏的收口帧",
		Header: card.Header{Title: "{{verdict}} · {{没有这个键}}"},
	})
	if len(notes) == 0 {
		t.Fatal("帧要了一个没有的槽位，却一声不吭")
	}
}

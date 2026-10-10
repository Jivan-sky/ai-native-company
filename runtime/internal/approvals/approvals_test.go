package approvals

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anc/internal/envelope"
	"anc/internal/hot"
	"anc/internal/org"
)

// fakeQueue 是热层的假替身。
//
// 用例要测的是**顺序与痕迹**（先落库、再清热层、幂等），不是 Redis 本身 ——
// 「用真 Redis 跑一遍」留给沙箱里的实测，这里不起服务。
type fakeQueue struct {
	items   map[string]hot.State
	puts    []string
	drops   []string
	failPut error
}

func newFakeQueue() *fakeQueue { return &fakeQueue{items: map[string]hot.State{}} }

func (f *fakeQueue) Get(id string) (hot.State, bool, error) {
	s, ok := f.items[strings.TrimSpace(id)]
	return s, ok, nil
}

func (f *fakeQueue) Put(s hot.State) (hot.State, error) {
	if f.failPut != nil {
		return hot.State{}, f.failPut
	}
	f.items[strings.TrimSpace(s.ID)] = s
	f.puts = append(f.puts, s.ID)
	return s, nil
}

func (f *fakeQueue) Drop(id string) error {
	delete(f.items, strings.TrimSpace(id))
	f.drops = append(f.drops, id)
	return nil
}

func (f *fakeQueue) List() ([]hot.State, error) {
	out := make([]hot.State, 0, len(f.items))
	for _, s := range f.items {
		out = append(out, s)
	}
	return out, nil
}

func proposal(id string) envelope.Envelope {
	return envelope.Envelope{
		ID:         id,
		TS:         "2026-10-10T20:00:00+08:00",
		Who:        "bob-bot",
		OnBehalfOf: "member:bob",
		Scope:      envelope.Scope{Domain: "ops"},
		Kind:       envelope.KindProposal,
		Body:       "想把退货那摊接过来，需要看一下客服域的数据。",
	}
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("用例写错了时间：%v", err)
	}
	return v
}

// 点头只认三个字：别的一律不认，**不猜**。
func TestSignalOnlyThreeWords(t *testing.T) {
	for _, s := range []string{"approve", " Reject ", "HOLD"} {
		if _, ok := ParseSignal(s); !ok {
			t.Fatalf("%q 该被认出来", s)
		}
	}
	// 「yes」「同意」看着都像批准 —— 正因为像，才不能认：认了就是把门交给一个能被说服的东西。
	for _, s := range []string{"yes", "同意", "ok", "ask", ""} {
		if k, ok := ParseSignal(s); ok {
			t.Fatalf("%q 不该被认出来（认成了 %q）", s, k)
		}
	}
}

// 只收 proposal：别的 kind 的去处不是「等人点头」。
func TestEnqueueOnlyTakesProposal(t *testing.T) {
	when := at(t, "2026-10-10T20:01:00+08:00")

	if _, err := FromEnvelope(proposal("p-1"), when); err != nil {
		t.Fatalf("proposal 该收：%v", err)
	}
	ask := proposal("p-2")
	ask.Kind = envelope.KindAsk
	if _, err := FromEnvelope(ask, when); err == nil {
		t.Fatal("ask 不该进待批队列")
	}
	// 认不出的 kind（现场先发一句那种）也不进 —— 队列不是筐。
	weird := proposal("p-3")
	weird.Kind = "whatever"
	if _, err := FromEnvelope(weird, when); err == nil {
		t.Fatal("认不出的 kind 不该进待批队列")
	}
	// 没有 id 就点不了头：队列靠 id 指认。
	noID := proposal("")
	if _, err := FromEnvelope(noID, when); err == nil {
		t.Fatal("没有 id 的信封不该进队")
	}
}

// 队列里那一行必须**带「代谁」**：审批的人第一眼要知道是谁在要权。
func TestTitleCarriesOnBehalfOf(t *testing.T) {
	p, err := FromEnvelope(proposal("p-1"), at(t, "2026-10-10T20:01:00+08:00"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Title, "bob") || !strings.Contains(p.Title, "ops") {
		t.Fatalf("一句话里该有「代谁」和「哪块业务」：%q", p.Title)
	}
	if p.Status != StatusPending {
		t.Fatalf("刚入队该是 %s：%q", StatusPending, p.Status)
	}
}

// 热层进出要无损：读回来的那条必须跟写进去的一模一样。
func TestStateRoundTrip(t *testing.T) {
	p, err := FromEnvelope(proposal("p-1"), at(t, "2026-10-10T20:01:00+08:00"))
	if err != nil {
		t.Fatal(err)
	}
	p.To = "经理"
	back, err := FromState(p.State())
	if err != nil {
		t.Fatal(err)
	}
	if back != p {
		t.Fatalf("往返丢东西：\n写进去 %+v\n读回来 %+v", p, back)
	}
	// 队列 id 是前缀 + 提案 id；不属于这个队列的条目要认出来。
	if _, ok := ProposalID(p.State().ID); !ok {
		t.Fatalf("%q 该被认出是队列条目", p.State().ID)
	}
	if _, ok := ProposalID("t-1"); ok {
		t.Fatal("普通任务条目不该被当成待批")
	}
}

// 重复入队不冲掉「等了多久」—— 否则每提一次都像刚提的。
func TestEnqueueIsIdempotent(t *testing.T) {
	q := newFakeQueue()
	first, err := FromEnvelope(proposal("p-1"), at(t, "2026-10-10T20:01:00+08:00"))
	if err != nil {
		t.Fatal(err)
	}
	saved, created, err := Enqueue(q, first)
	if err != nil || !created {
		t.Fatalf("第一次该是新入队：created=%v err=%v", created, err)
	}
	second, err := FromEnvelope(proposal("p-1"), at(t, "2026-10-10T21:30:00+08:00"))
	if err != nil {
		t.Fatal(err)
	}
	again, created, err := Enqueue(q, second)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("第二次不该是新入队")
	}
	if again.Enqueued != saved.Enqueued {
		t.Fatalf("时间戳被冲掉了：%s -> %s", saved.Enqueued, again.Enqueued)
	}
	if len(q.puts) != 1 {
		t.Fatalf("只该写一次热层，写了 %d 次", len(q.puts))
	}
}

// 队列的边界是**前缀**，不是 status：进行中的任务躺在同一个热层里，不许串进来。
func TestListPendingSkipsOtherTasks(t *testing.T) {
	q := newFakeQueue()
	later, err := FromEnvelope(proposal("p-late"), at(t, "2026-10-10T21:00:00+08:00"))
	if err != nil {
		t.Fatal(err)
	}
	earlier, err := FromEnvelope(proposal("p-early"), at(t, "2026-10-10T19:00:00+08:00"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Enqueue(q, later); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Enqueue(q, earlier); err != nil {
		t.Fatal(err)
	}
	// 一条普通任务：同一个热层，不同前缀。
	if _, err := q.Put(hot.State{ID: "t-1", Status: "running", Title: "别的活"}); err != nil {
		t.Fatal(err)
	}

	list, err := ListPending(q)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("该只有 2 条待批，拿到 %d 条", len(list))
	}
	if list[0].ID != "p-early" || list[1].ID != "p-late" {
		t.Fatalf("该按进队时间从早到晚：%s / %s", list[0].ID, list[1].ID)
	}
}

// 点头：落库 + 审计两笔，然后清掉队列里那条。
func TestSettleWritesBothAndClearsQueue(t *testing.T) {
	vault := t.TempDir()
	q := newFakeQueue()
	p, err := FromEnvelope(proposal("p-1"), at(t, "2026-10-10T20:01:00+08:00"))
	if err != nil {
		t.Fatal(err)
	}
	p.To = "经理"
	if _, _, err := Enqueue(q, p); err != nil {
		t.Fatal(err)
	}
	d, err := New("p-1", "ou_bob", SignalApprove, "退货这摊归他", at(t, "2026-10-10T21:00:00+08:00"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Settle(q, vault, d)
	if err != nil {
		t.Fatal(err)
	}

	tl := readAll(t, r.Timeline)
	for _, want := range []string{`"kind":"decision"`, `"case":"p-1"`, `"status":"done"`, `"by":"ou_bob"`, "同意", `"to":"经理"`} {
		if !strings.Contains(tl, want) {
			t.Fatalf("落库那行里该有 %s：%s", want, tl)
		}
	}
	// 提案原文必须留在痕里 —— 事后要能回答「他到底要什么」。
	if !strings.Contains(tl, "退货") {
		t.Fatalf("落库那行该留着提案原文：%s", tl)
	}

	au := readAll(t, r.Audit)
	for _, want := range []string{`"result":"ok"`, `"action":"invoke"`, `"actor":"ou_bob"`, `"tool":"anc approvals"`, `"source":"manual"`, `"object":"ops"`} {
		if !strings.Contains(au, want) {
			t.Fatalf("审计那行里该有 %s：%s", want, au)
		}
	}

	if _, ok, _ := q.Get(QueueID("p-1")); ok {
		t.Fatal("结账之后队列里不该还有这条")
	}

	// 再点一次：不能静默成功，也不能再落一笔痕。
	before := readAll(t, r.Timeline)
	if _, err := Settle(q, vault, d); !errors.Is(err, ErrNotQueued) {
		t.Fatalf("重复点头该报 ErrNotQueued，拿到 %v", err)
	}
	if after := readAll(t, r.Timeline); after != before {
		t.Fatal("重复点头不该再落一笔痕")
	}
}

// 「先落库、再清热层」：库没落成，队列必须还在 —— 反序就会把一次点头弄丢。
func TestSettleKeepsQueueWhenTimelineFails(t *testing.T) {
	vault := t.TempDir()
	// 把 timeline 这个目录名占成一个文件 → 落库必然失败。
	if err := os.WriteFile(filepath.Join(vault, "timeline"), []byte("占位"), 0o644); err != nil {
		t.Fatal(err)
	}
	q := newFakeQueue()
	p, err := FromEnvelope(proposal("p-1"), at(t, "2026-10-10T20:01:00+08:00"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Enqueue(q, p); err != nil {
		t.Fatal(err)
	}
	d, err := New("p-1", "ou_bob", SignalReject, "", at(t, "2026-10-10T21:00:00+08:00"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Settle(q, vault, d); err == nil {
		t.Fatal("落库失败就该报错，不能假装成功")
	}
	if _, ok, _ := q.Get(QueueID("p-1")); !ok {
		t.Fatal("落库失败时热层不该被清掉")
	}
	if len(q.drops) != 0 {
		t.Fatal("落库失败时不该动热层")
	}
	if _, err := os.Stat(filepath.Join(vault, "audit")); !os.IsNotExist(err) {
		t.Fatal("落库失败时不该写审计（否则会留下一笔没有结论的行使）")
	}
}

// 匿名点头等于没点头。
func TestDecideNeedsWho(t *testing.T) {
	for _, c := range []struct {
		name string
		by   string
		sig  Signal
	}{
		{"没写谁", "", SignalApprove},
		{"认不出的信号", "ou_bob", Signal("同意")},
	} {
		if _, err := New("p-1", c.by, c.sig, "", time.Now()); err == nil {
			t.Fatalf("%s：该被拦下来", c.name)
		}
	}
	if _, err := New("", "ou_bob", SignalApprove, "", time.Now()); err == nil {
		t.Fatal("没写提案 id：该被拦下来")
	}
}

// 「该谁批」从域表解：批的人是**那个岗**，不是人名；解不出要说得出来为什么。
func TestApproverComesFromDomainTable(t *testing.T) {
	o := &org.Org{Domains: []org.Domain{{Slug: "ops", Who: "经理"}}}
	if who, note := Approver(o, "ops"); who != "经理" || note != "" {
		t.Fatalf("ops 该解出经理：%q / %q", who, note)
	}
	if who, note := Approver(o, "sales"); who != "" || !strings.Contains(note, "域表里没有") {
		t.Fatalf("没有的域该说清：%q / %q", who, note)
	}
	if _, note := Approver(o, ""); !strings.Contains(note, "scope.domain") {
		t.Fatalf("没写客体该说清：%q", note)
	}
	if _, note := Approver(nil, "ops"); !strings.Contains(note, "org") {
		t.Fatalf("没加载到真相源该说清：%q", note)
	}
	empty := &org.Org{Domains: []org.Domain{{Slug: "ops"}}}
	if who, note := Approver(empty, "ops"); who != "" || !strings.Contains(note, "who") {
		t.Fatalf("who 空该说清：%q / %q", who, note)
	}
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到 %s：%v", path, err)
	}
	return string(b)
}

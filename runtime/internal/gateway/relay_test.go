package gateway

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 样本一处收着：`runtime/testdata/card/`（与 internal/card 的用例同一批文件）。
func sample(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "card", name))
	if err != nil {
		t.Fatalf("样本读不到 %s：%v", name, err)
	}
	return raw
}

// evt 造一条扁平形状的真事件（长连接那份的形状），只换 event_id 与提议号。
// 按键那一格是**一串 JSON 文本**（真事件就长这样），所以先 Marshal 再当字符串塞进去。
func evt(eventID, proposal string) []byte {
	inner, _ := json.Marshal(map[string]string{"anc_id": proposal, "anc_signal": "approve"})
	outer, _ := json.Marshal(map[string]any{
		"type": "card.action.trigger", "event_id": eventID,
		"operator_id": "ou_demo_alice", "message_id": "om_demo_card_1",
		"chat_id": "oc_demo_chat_1", "action_tag": "button",
		"action_value": string(inner),
	})
	return outer
}

// fakeSink 记账：落痕几次、收口几次、各自返回什么。
type fakeSink struct {
	settleCalls int
	patchCalls  int
	settleErr   error
	patchErr    error
	lastPatch   *Settle
}

func (f *fakeSink) Settle(raw []byte) (*Settle, error) {
	f.settleCalls++
	if f.settleErr != nil {
		return nil, f.settleErr
	}
	return &Settle{
		Proposal: "prop-card-1", Signal: "approve", By: "alice",
		Via: "飞书卡片 · 点的人 alice", At: "2026-10-11T00:48:12+08:00",
		ChatID: "oc_demo_chat_1", MessageID: "om_demo_card_1",
		DoneCard: json.RawMessage(`{"config":{},"header":{"template":"green"}}`),
		Timeline: "timeline/2026-10.alice.jsonl", Audit: "audit/2026-10.alice.jsonl",
	}, nil
}

func (f *fakeSink) Patch(s *Settle) error {
	f.patchCalls++
	f.lastPatch = s
	return f.patchErr
}

func newRelay(t *testing.T, cfg Config, sink Sink) *Relay {
	t.Helper()
	r, err := New(cfg, sink)
	if err != nil {
		t.Fatalf("造 relay 都不成：%v", err)
	}
	return r
}

// 判据 1：钥匙先认 event_id —— 真事件带它，就得用它。
func TestEventKeyPrefersEventID(t *testing.T) {
	if got := EventKey(sample(t, "click-flat.json")); got != "e:e041e2cb2228b7f85b3da12b14d7e70a" {
		t.Errorf("扁平形状的钥匙取错了：%s", got)
	}
	// 嵌套形状把 event_id 放在 header 里，也要认。
	if got := EventKey(sample(t, "click-nested.json")); got != "e:evt-card-1" {
		t.Errorf("嵌套形状的钥匙取错了：%s", got)
	}
}

// 判据 2：没带 event_id 就退到指纹 —— 同一条消息、同一个人、同一个按键 = 同一件事，
// 外面那层 noise（时间戳之类）换个值不许改钥匙。
func TestEventKeyFingerprintWithoutID(t *testing.T) {
	a := []byte(`{"operator_id":"ou_demo_alice","message_id":"om_x","action_value":"{\"anc_id\":\"p1\",\"anc_signal\":\"approve\"}"}`)
	b := []byte(`{"timestamp":"1791650891553585","operator_id":"ou_demo_alice","message_id":"om_x","action_value":"{\"anc_id\":\"p1\",\"anc_signal\":\"approve\"}"}`)
	ka, kb := EventKey(a), EventKey(b)
	if !strings.HasPrefix(ka, "f:") || ka != kb {
		t.Errorf("指纹不稳：%s / %s", ka, kb)
	}
	// 换个人点同一条 = 另一件事（不然 Bob 的点头会被 Alice 的那次吃掉）。
	c := []byte(`{"operator_id":"ou_demo_bob","message_id":"om_x","action_value":"{\"anc_id\":\"p1\",\"anc_signal\":\"approve\"}"}`)
	if EventKey(c) == ka {
		t.Error("不同的人点同一条，钥匙不该一样")
	}
}

// 判据 3：认得出的一次按键 → 落痕 + 收口，两样都发生，且落痕吃的是**原样的**事件体。
func TestHandleSettlesAndPatches(t *testing.T) {
	fs := &fakeSink{}
	raw := sample(t, "click-flat.json")
	o := newRelay(t, Config{}, fs).Handle(raw)
	if o.Kind != KindSettled {
		t.Fatalf("想要 settled，拿到 %s（%s）", o.Kind, o.Detail)
	}
	if !o.Patched || o.PatchErr != "" {
		t.Errorf("收口没成：patched=%v err=%s", o.Patched, o.PatchErr)
	}
	if fs.settleCalls != 1 || fs.patchCalls != 1 {
		t.Errorf("该落一次痕收一次口：settle=%d patch=%d", fs.settleCalls, fs.patchCalls)
	}
	if fs.lastPatch == nil || fs.lastPatch.Timeline == "" {
		t.Error("收口那一侧没拿到落痕的成品")
	}
}

// 判据 4：同一件事递两次 → 第二次只答 duplicate，落痕那一侧一步都不动。
func TestHandleDeduplicates(t *testing.T) {
	fs := &fakeSink{}
	r := newRelay(t, Config{}, fs)
	raw := sample(t, "click-flat.json")
	if o := r.Handle(raw); o.Kind != KindSettled {
		t.Fatalf("第一次该 settled，拿到 %s", o.Kind)
	}
	o := r.Handle(raw)
	if o.Kind != KindDuplicate {
		t.Fatalf("第二次该 duplicate，拿到 %s", o.Kind)
	}
	if fs.settleCalls != 1 {
		t.Errorf("重复那一次不许再落痕：settle=%d", fs.settleCalls)
	}
}

// 判据 5：不是一次按键的（别的卡片 / 认不出形状）→ ignored，不进落痕那一侧。
func TestHandleIgnoresNonClick(t *testing.T) {
	for _, name := range []string{"click-not-json.json", "click-no-action.json", "click-plain-string.json"} {
		fs := &fakeSink{}
		o := newRelay(t, Config{}, fs).Handle(sample(t, name))
		if o.Kind != KindIgnored {
			t.Errorf("%s 该 ignored，拿到 %s（%s）", name, o.Kind, o.Detail)
		}
		if fs.settleCalls != 0 {
			t.Errorf("%s 不该碰落痕那一侧", name)
		}
	}
}

// 判据 6：是一次按键、但没带齐那两个键 → rejected，同样不落痕（缺一个也不猜）。
func TestHandleRejectsMissingKeys(t *testing.T) {
	fs := &fakeSink{}
	o := newRelay(t, Config{}, fs).Handle(sample(t, "click-signal-only.json"))
	if o.Kind != KindRejected {
		t.Fatalf("该 rejected，拿到 %s", o.Kind)
	}
	if fs.settleCalls != 0 {
		t.Error("缺键的那条不该进落痕那一侧")
	}
}

// 判据 7：落痕那一侧说「判据说不」→ rejected；说别的 → error。两种都不许混。
func TestHandleSplitsRejectFromError(t *testing.T) {
	fs := &fakeSink{settleErr: Reject{Reason: "认不出的信号 \"maybe\""}}
	if o := newRelay(t, Config{}, fs).Handle(sample(t, "click-flat.json")); o.Kind != KindRejected {
		t.Errorf("Reject 该走 rejected，拿到 %s", o.Kind)
	}
	fs2 := &fakeSink{settleErr: errors.New("热层没连上")}
	if o := newRelay(t, Config{}, fs2).Handle(sample(t, "click-flat.json")); o.Kind != KindError {
		t.Errorf("故障该走 error，拿到 %s", o.Kind)
	}
	if !IsReject(Reject{Reason: "x"}) || IsReject(errors.New("x")) {
		t.Error("IsReject 分不清 Reject 与普通错误")
	}
}

// 判据 8：收口失败**不回退**已经落的痕 —— 账不能因为面子丢了。
func TestPatchFailureKeepsSettle(t *testing.T) {
	fs := &fakeSink{patchErr: errors.New("平台回 400")}
	o := newRelay(t, Config{}, fs).Handle(sample(t, "click-flat.json"))
	if o.Kind != KindSettled {
		t.Fatalf("落痕成了就该是 settled，拿到 %s", o.Kind)
	}
	if o.Patched || !strings.Contains(o.PatchErr, "400") {
		t.Errorf("收口失败要如实记下：patched=%v err=%q", o.Patched, o.PatchErr)
	}
	if !strings.Contains(o.Line(time.Now()), "收口没成") {
		t.Errorf("那一行话没把收口那件事说出来：%s", o.Line(time.Now()))
	}
}

// 判据 9：NoPatch（调用方自己就是应答方）→ 只落痕，不碰平台。
func TestPatchOffSkipsPlatform(t *testing.T) {
	fs := &fakeSink{}
	o := newRelay(t, Config{NoPatch: true}, fs).Handle(sample(t, "click-flat.json"))
	if o.Kind != KindSettled || o.Patched || o.PatchErr != "" {
		t.Errorf("关掉收口就该只 settled：%+v", o)
	}
	if fs.patchCalls != 0 {
		t.Error("关掉了还去碰平台")
	}
}

// 判据 10：幂等表装不下时吐最老的 —— 别让它变成一条只会涨的内存。
func TestDedupeCapacityEvictsOldest(t *testing.T) {
	fs := &fakeSink{}
	r := newRelay(t, Config{Capacity: 2}, fs)
	for i := 1; i <= 3; i++ {
		if o := r.Handle(evt("e"+strconv.Itoa(i), "p"+strconv.Itoa(i))); o.Kind != KindSettled {
			t.Fatalf("第 %d 条该 settled，拿到 %s", i, o.Kind)
		}
	}
	if o := r.Handle(evt("e1", "p1")); o.Kind != KindSettled {
		t.Errorf("e1 早被挤出去了，该重新 settled，拿到 %s", o.Kind)
	}
	if o := r.Handle(evt("e3", "p3")); o.Kind != KindDuplicate {
		t.Errorf("e3 还在表里，该 duplicate，拿到 %s", o.Kind)
	}
}

// 判据 11：过了保鲜期，同一件事重新算一件事（长跑进程的内存要有边）。
func TestDedupeTTLExpires(t *testing.T) {
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	fs := &fakeSink{}
	r := newRelay(t, Config{TTL: time.Minute, Now: func() time.Time { return now }}, fs)
	if o := r.Handle(evt("e1", "p1")); o.Kind != KindSettled {
		t.Fatalf("第一次该 settled，拿到 %s", o.Kind)
	}
	if o := r.Handle(evt("e1", "p1")); o.Kind != KindDuplicate {
		t.Fatalf("保鲜期内该 duplicate，拿到 %s", o.Kind)
	}
	now = now.Add(2 * time.Minute)
	if o := r.Handle(evt("e1", "p1")); o.Kind != KindSettled {
		t.Errorf("过期后该重新 settled，拿到 %s", o.Kind)
	}
}

// 判据 12：没 sink 就造不出来 —— 没有落痕那一半，收口无从谈起。
func TestNewNeedsSink(t *testing.T) {
	if _, err := New(Config{}, nil); err == nil {
		t.Error("没 sink 也放过去了")
	}
}

// 判据 13：那一行话里得看得出「什么结局、哪一件事」。
func TestLineCarriesKindAndKey(t *testing.T) {
	fs := &fakeSink{}
	o := newRelay(t, Config{}, fs).Handle(sample(t, "click-flat.json"))
	line := o.Line(time.Date(2026, 10, 11, 0, 48, 12, 0, time.UTC))
	for _, want := range []string{KindSettled, "e:e041e2cb2228b7f85b3da12b14d7e70a", "prop-card-1", "alice"} {
		if !strings.Contains(line, want) {
			t.Errorf("那一行话里缺 %q：%s", want, line)
		}
	}
}

package envelope

import (
	"reflect"
	"testing"
)

func sample() Envelope {
	return Envelope{
		ID:         "e-1",
		TS:         "2026-10-08T02:41:00+08:00",
		Who:        "alice",
		OnBehalfOf: "member:alice",
		Scope:      Scope{Domain: "trade", Project: "trade-q3"},
		Kind:       KindAsk,
		Body:       "帮我看下这单能不能签",
		Refs:       []string{"10-knowledge/x.md"},
		Needs:      []string{"human-approval"},
	}
}

func TestParseRejectsIncomplete(t *testing.T) {
	cases := []struct{ name, raw string }{
		{"不是 JSON", `not json`},
		{"缺 id", `{"ts":"2026-10-08T02:41:00+08:00","who":"alice"}`},
		{"缺 ts", `{"id":"e","who":"alice"}`},
		{"ts 不是 RFC3339", `{"id":"e","ts":"2026/10/08 02:41","who":"alice"}`},
		{"缺 who", `{"id":"e","ts":"2026-10-08T02:41:00+08:00"}`},
	}
	for _, c := range cases {
		if _, err := Parse([]byte(c.raw)); err == nil {
			t.Errorf("%s：应当拒收，实际收了", c.name)
		}
	}
}

// 只写三个必需项的**最小信封**必须收得下 —— 现场先发一句，不该被字段缺失挡住。
func TestParseAcceptsMinimal(t *testing.T) {
	e, err := Parse([]byte(`{"id":"e","ts":"2026-10-08T02:41:00+08:00","who":"alice"}`))
	if err != nil {
		t.Fatalf("最小信封应当收下：%v", err)
	}
	if e.Who != "alice" || e.Kind != "" || len(e.Refs) != 0 {
		t.Fatalf("解析结果不对：%+v", e)
	}
}

func TestAtParsesRFC3339(t *testing.T) {
	if _, ok := sample().At(); !ok {
		t.Fatal("ts 应当解析得出来")
	}
	if _, ok := (Envelope{TS: "  "}).At(); ok {
		t.Fatal("空 ts 不该解析得出来")
	}
}

func TestKnownKind(t *testing.T) {
	for _, k := range []string{"ask", "report", "notify", "ingest", "proposal", "  ASK "} {
		if !KnownKind(k) {
			t.Errorf("%q 应当认得", k)
		}
	}
	for _, k := range []string{"", "escalate", "human-approval"} {
		if KnownKind(k) {
			t.Errorf("%q 不该认得（词表不锁死：认不出照收，只是归类不了）", k)
		}
	}
}

func TestBytesRoundTrip(t *testing.T) {
	in := sample()
	b, err := in.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	out, err := Parse(b)
	if err != nil {
		t.Fatalf("自己写出去的读不回来：%v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("往返不一致：\n  in  %+v\n  out %+v", in, out)
	}
}

// 判据：只靠信封就能答出这六件事。顺序即「人怎么读它」。
func TestBriefAnswersSix(t *testing.T) {
	got := sample().Brief()
	want := []Line{
		{"谁", "alice"},
		{"代谁", "member:alice"},
		{"哪块业务", "trade / trade-q3"},
		{"要什么", "ask"},
		{"证据在哪", "10-knowledge/x.md"},
		{"要不要人拍", "human-approval"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("六问六答不符：\n  实际 %+v\n  期望 %+v", got, want)
	}
}

// 空字段**不假装知道**：给一个「（…）」的占位，让人一眼看出这条没填。
func TestBriefMarksEmpties(t *testing.T) {
	e := Envelope{ID: "e", TS: "2026-10-08T02:41:00+08:00", Who: "alice"}
	got := e.Brief()
	want := []string{"（空）", "（没写）", "（没带）", "（没提）"}
	// 索引 1/3/4/5 = 代谁 / 要什么 / 证据在哪 / 要不要人拍
	for i, w := range want {
		idx := []int{1, 3, 4, 5}[i]
		if got[idx].A != w {
			t.Errorf("%s 空值应显示 %q，实际 %q", got[idx].Q, w, got[idx].A)
		}
	}
}

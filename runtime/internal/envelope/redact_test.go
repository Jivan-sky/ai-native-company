package envelope

import (
	"os"
	"strings"
	"testing"
)

// 这一组吃的是**跨流水脱敏**那一刀（议题 #64）：信封日志与 audit 吃同一张表。
const fakeSecret64 = "sk-ant-api03-Qq1Ww2Ee3Rr4Tt5Yy"

const markOpen64 = "«已脱敏:"

// ── 1 纯函数：body / refs / needs / findings.msg 过表，标识与词表不动 ──
func TestScrubTouchesOnlyFreeText(t *testing.T) {
	r := Record{
		Envelope: Envelope{
			ID: "e1", TS: "2026-10-10T12:00:00+08:00", Who: "alice",
			OnBehalfOf: "member:alice", Kind: KindReport,
			Body:  "正文里带了口令：" + fakeSecret64,
			Refs:  []string{"https://x.test/a?token=" + fakeSecret64, "docs/plain.md"},
			Needs: []string{"要不要人拍：" + fakeSecret64},
		},
		At: "2026-10-10T12:00:00+08:00",
		Findings: []Finding{
			{Rule: "x", Level: "warn", Msg: "值看起来像密钥：" + fakeSecret64},
		},
	}
	if hits := scrub(&r); len(hits) == 0 {
		t.Fatal("该命中并报出规则名")
	}
	for _, got := range []string{
		r.Envelope.Body, strings.Join(r.Envelope.Refs, " "),
		strings.Join(r.Envelope.Needs, " "), r.Findings[0].Msg,
	} {
		if strings.Contains(got, fakeSecret64) {
			t.Errorf("明文还留着：%q", got)
		}
		if !strings.Contains(got, markOpen64) {
			t.Errorf("该留下占位符，得到 %q", got)
		}
	}
	if r.Envelope.Refs[1] != "docs/plain.md" {
		t.Errorf("没命中的 ref 不该被动：%q", r.Envelope.Refs[1])
	}
	if r.Envelope.ID != "e1" || r.Envelope.Who != "alice" ||
		r.Envelope.OnBehalfOf != "member:alice" || r.Envelope.Kind != KindReport ||
		r.At != "2026-10-10T12:00:00+08:00" || r.Findings[0].Rule != "x" {
		t.Errorf("标识/词表格子被动了：%+v", r)
	}
}

// ── 2 端到端 · 写口：Append 落盘之后，磁盘上不许有那个明文 ──
func TestAppendWritesRedactedToDisk(t *testing.T) {
	data := t.TempDir()
	rec := Record{
		Envelope: Envelope{
			ID: "e1", TS: "2026-10-10T12:00:00+08:00", Who: "alice",
			OnBehalfOf: "member:alice", Kind: KindReport,
			Body: "落盘前先把口令念了一遍：" + fakeSecret64,
		},
		At:       "2026-10-10T12:00:00+08:00",
		Findings: []Finding{{Rule: "x", Level: "warn", Msg: "引了原文：" + fakeSecret64}},
	}
	path, err := Append(data, rec)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), fakeSecret64) {
		t.Fatalf("磁盘上还躺着明文：%s", raw)
	}
	if !strings.Contains(string(raw), markOpen64) {
		t.Fatalf("该留下占位符（让人看出这里有过东西），得到 %s", raw)
	}
}

// 占位符得是「哪一类」那个名字，不是空白 —— 静默抹平与假绿是同一类错误。
func TestAppendPlaceholderNamesTheRule(t *testing.T) {
	data := t.TempDir()
	rec := Record{
		Envelope: Envelope{
			ID: "e1", TS: "2026-10-10T12:00:00+08:00", Who: "alice",
			OnBehalfOf: "member:alice", Kind: KindReport, Body: fakeSecret64,
		},
		At: "2026-10-10T12:00:00+08:00",
	}
	path, err := Append(data, rec)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "sk-"+markOpen64+"sk»") {
		t.Fatalf("形态档拙出来该是 %q，得到 %s", "sk-"+markOpen64+"sk»", raw)
	}
}

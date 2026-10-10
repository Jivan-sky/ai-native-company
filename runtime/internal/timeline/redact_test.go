package timeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这一组吃的是**跨流水脱敏**那一刀（议题 #64）：timeline 与 audit 吃同一张表、同一个占位符。
// 断言只锁三件是契约的事：**明文没了 / 占位符写出了哪一类 / 该留的格子一个没动**。

// fakeSecret64 是形态档一眼认得出的假密钥（`sk-` 前缀 + 足够长的串）。
const fakeSecret64 = "sk-ant-api03-Qq1Ww2Ee3Rr4Tt5Yy"

const markOpen64 = "«已脱敏:"

// ── 1 纯函数：只碰 title / detail / refs，其余格子一个字都不动 ──
func TestScrubTouchesOnlyFreeText(t *testing.T) {
	e := Entry{
		ID: "t1", Case: "case-slug", At: "2026-10-10T12:00:00+08:00",
		Kind: "note", Status: "running", By: "alice", Role: "manager",
		To: "bob", Domain: "trade", Project: "import-q4",
		Title:  "把口令文件念了一遍：" + fakeSecret64,
		Detail: "原文里有 ANTHROPIC_AUTH_TOKEN=" + fakeSecret64,
		Refs:   []string{"https://x.test/a?token=" + fakeSecret64, "docs/plain.md"},
	}
	hits := scrub(&e)
	if len(hits) == 0 {
		t.Fatal("该命中并报出规则名 —— 占位符要写出「哪一类」")
	}
	for _, got := range []string{e.Title, e.Detail, strings.Join(e.Refs, " ")} {
		if strings.Contains(got, fakeSecret64) {
			t.Errorf("自由文本里的明文还留着：%q", got)
		}
		if !strings.Contains(got, markOpen64) {
			t.Errorf("该留下占位符，得到 %q", got)
		}
	}
	if e.Refs[1] != "docs/plain.md" {
		t.Errorf("没命中的 ref 不该被动：%q", e.Refs[1])
	}
	// 标识与词表：按 case 折叠、按人按域筛全靠它们，抹了就看不出「谁在推进哪一格」。
	if e.ID != "t1" || e.Case != "case-slug" || e.By != "alice" || e.Role != "manager" ||
		e.To != "bob" || e.Domain != "trade" || e.Project != "import-q4" ||
		e.Kind != "note" || e.Status != "running" {
		t.Errorf("标识/词表格子被动了：%+v", e)
	}
}

// ── 2 不误伤：用量口径的词、已经是引用（指针）的，一个都不许动 ──
func TestScrubLeavesUsageCountersAndPointersAlone(t *testing.T) {
	for _, in := range []string{
		"input_tokens=1234 output_tokens=5678",
		"token=${ANTHROPIC_AUTH_TOKEN}",
	} {
		e := Entry{ID: "t", At: "2026-10-10T12:00:00+08:00", By: "alice", Title: in}
		if hits := scrub(&e); e.Title != in || len(hits) != 0 {
			t.Errorf("%q 不该被抹，得到 %q（命中 %v）", in, e.Title, hits)
		}
	}
}

// ── 3 端到端 · 写口：Append 落盘之后，磁盘上不许有那个明文，且不算「老明文」 ──
func TestAppendWritesRedactedToDisk(t *testing.T) {
	vault := t.TempDir()
	path, err := Append(vault, Entry{
		ID: "t1", At: "2026-10-10T12:00:00+08:00", By: "alice",
		Title: "写之前先把口令念了一遍：" + fakeSecret64,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shard: %v", err)
	}
	if strings.Contains(string(raw), fakeSecret64) {
		t.Fatalf("磁盘上还躺着明文：%s", raw)
	}
	if !strings.Contains(string(raw), markOpen64) {
		t.Fatalf("该留下占位符（让人看出这里有过东西），得到 %s", raw)
	}
	doc, err := Load(vault)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if doc.Len() != 1 {
		t.Fatalf("该读回 1 条，得到 %d", doc.Len())
	}
	if strings.Contains(doc.Entries[0].Title, fakeSecret64) {
		t.Fatalf("读回来还带着明文：%q", doc.Entries[0].Title)
	}
	if len(doc.Tainted) != 0 {
		t.Fatalf("刚落盘的行是抹过再写的，不该算「落盘时就是明文」：%v", doc.Tainted)
	}
}

// ── 4 端到端 · 读口：老明文读出来是干净的、位置指得出来、**文件本身一个字节不动** ──
func TestLoadRedactsLegacyPlaintextAndReportsIt(t *testing.T) {
	vault := t.TempDir()
	dir := filepath.Join(vault, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"id":"t9","at":"2026-10-01T09:00:00+08:00","by":"alice","title":"老行：` + fakeSecret64 + `"}`
	line := append([]byte(legacy), "\n"...)
	p := filepath.Join(dir, "2026-10.alice.jsonl")
	if err := os.WriteFile(p, line, 0o644); err != nil {
		t.Fatal(err)
	}

	doc, err := Load(vault)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if doc.Len() != 1 {
		t.Fatalf("该读回 1 条，得到 %d", doc.Len())
	}
	if strings.Contains(doc.Entries[0].Title, fakeSecret64) {
		t.Fatalf("读回来还带着明文：%q", doc.Entries[0].Title)
	}
	if len(doc.Tainted) != 1 || doc.Tainted[0] != "2026-10.alice.jsonl:1" {
		t.Fatalf("该如实报出那一行是脏的，得到 %v", doc.Tainted)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(line) {
		t.Fatal("读口是兜底，不是清理 —— 文件一个字节都不该被改（append-only）")
	}
}

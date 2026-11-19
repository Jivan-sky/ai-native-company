package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func addOne(t *testing.T, v, id string) string {
	t.Helper()
	p, err := Append(v, Record{ID: id, At: "2026-11-19T10:00:00+08:00", Actor: "alice",
		Role: "manager", Action: ActionRead, Result: ResultOK})
	if err != nil {
		t.Fatalf("append %s: %v", id, err)
	}
	return p
}

// 正常链路：三行连得上，verify 全通过。
func TestChainAppendAndVerify(t *testing.T) {
	v := t.TempDir()
	for _, id := range []string{"a", "b", "c"} {
		addOne(t, v, id)
	}
	rep, err := Verify(v)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Lines != 3 || rep.Chained != 3 || len(rep.Issues) != 0 {
		t.Fatalf("该是 3 行全通过，拿到 %+v", rep)
	}
}

// 改中间一行 → 必须被检出（判据 3 的核心：有写权也改不了历史而不留痕）。
func TestChainDetectsTamperedLine(t *testing.T) {
	v := t.TempDir()
	p := addOne(t, v, "a")
	addOne(t, v, "b")
	b, _ := os.ReadFile(p)
	tampered := strings.Replace(string(b), `"result":"ok"`, `"result":"failed"`, 1)
	if tampered == string(b) {
		t.Fatal("测试没改到内容")
	}
	if err := os.WriteFile(p, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Issues) == 0 {
		t.Fatal("被改过的行必须报出来")
	}
	if !strings.Contains(rep.Issues[0].Why, "hash") && !strings.Contains(rep.Issues[0].Why, "prev_hash") {
		t.Fatalf("理由要说清是哈希对不上，拿到 %q", rep.Issues[0].Why)
	}
}

// 删掉中间一行 → 下一行的 prev_hash 对不上，也要报。
func TestChainDetectsDeletedLine(t *testing.T) {
	v := t.TempDir()
	p := addOne(t, v, "a")
	addOne(t, v, "b")
	addOne(t, v, "c")
	b, _ := os.ReadFile(p)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 {
		t.Fatalf("该有 3 行，拿到 %d", len(lines))
	}
	// 删掉中间那行
	if err := os.WriteFile(p, []byte(lines[0]+"\n"+lines[2]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Verify(v)
	if len(rep.Issues) == 0 {
		t.Fatal("删行必须被检出（prev_hash 断链）")
	}
}

// 老账（没有 hash）算 legacy，不当作伪造。
func TestChainTreatsLegacyAsNotForged(t *testing.T) {
	v := t.TempDir()
	dir := filepath.Join(v, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"id":"old","at":"2026-10-01T10:00:00+08:00","actor":"alice","role":"manager","action":"read","result":"ok"}`
	if err := os.WriteFile(filepath.Join(dir, "2026-10.alice.jsonl"), []byte(legacy+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Verify(v)
	if rep.Legacy != 1 || len(rep.Issues) != 0 {
		t.Fatalf("老账该算 legacy 且不报错，拿到 %+v", rep)
	}
}

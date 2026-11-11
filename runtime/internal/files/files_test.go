package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkroot(t *testing.T) Source {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "10-knowledge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "10-knowledge", "a.md"), []byte("# hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.md"), []byte(strings.Repeat("x", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	return Source{Schema: Schema, Name: "kb", Type: "file", Root: dir, MaxBytes: 10, MaxEntries: 100}
}

func TestListAndRead(t *testing.T) {
	s := mkroot(t)
	ents, trunc, err := List(s, "")
	if err != nil {
		t.Fatal(err)
	}
	if trunc || len(ents) != 2 { // 10-knowledge/ 与 big.md
		t.Fatalf("列目录不对：%+v", ents)
	}
	b, truncated, err := Read(s, "10-knowledge/a.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "# hello" || truncated {
		t.Fatalf("读文件不对：%q %v", b, truncated)
	}
}

func TestReadTruncates(t *testing.T) {
	s := mkroot(t)
	b, truncated, err := Read(s, "big.md")
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(b) != 10 {
		t.Fatalf("应当截断到 10 字节：len=%d truncated=%v", len(b), truncated)
	}
}

// 路径穿越：../、绝对路径、编码过的 ..，全部拒绝。
func TestTraversalRejected(t *testing.T) {
	s := mkroot(t)
	for _, p := range []string{"../etc/passwd", "/etc/passwd", "10-knowledge/../../etc/passwd", "..%2f..%2fetc/passwd"} {
		if _, _, err := Read(s, p); err == nil {
			t.Errorf("应当拒绝 %q", p)
		}
	}
}

// 符号链接逃逸：根内放一个指向根外的软链，读它必须被拒。
func TestSymlinkEscapeRejected(t *testing.T) {
	s := mkroot(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.Root, "escape")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("这个平台不支持软链：", err)
	}
	if _, _, err := Read(s, "escape"); err == nil {
		t.Fatal("软链逃逸必须被拒")
	}
	// 列目录时软链要标出来
	ents, _, _ := List(s, "")
	found := false
	for _, e := range ents {
		if e.Name == "escape" && e.Link {
			found = true
		}
	}
	if !found {
		t.Fatal("软链应当被标记")
	}
}

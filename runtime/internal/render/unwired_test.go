package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/org"
)

// 声明 unwired 的成员的 project 名要算得出来；**停用的**不算 ——
// 停用的成员本来就不进 config，多报一个只是噪声。
func TestUnwiredProjects(t *testing.T) {
	root := t.TempDir()
	src, err := filepath.Abs(filepath.Join("..", "..", "testdata", "orgs", "one"))
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(root, "vault")
	if err := os.CopyFS(vault, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	// devbot 声明未接凭据；alice 也声明，但同时停用 —— 停用的那个不许出现。
	if err := declareUnwired(filepath.Join(vault, "members", "devbot", "persona.md")); err != nil {
		t.Fatal(err)
	}
	alice := filepath.Join(vault, "members", "alice", "persona.md")
	if err := declareUnwired(alice); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(alice)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(b), "disabled: false", "disabled: true", 1)
	if patched == string(b) {
		t.Fatal("fixture 的 alice persona 里没找到 disabled 锚点")
	}
	if err := os.WriteFile(alice, []byte(patched), 0o600); err != nil {
		t.Fatal(err)
	}

	o, err := org.Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	got := UnwiredProjects(o)
	if len(got) != 1 || got[0] != "demo-devbot" {
		t.Fatalf("应当只有启用中的 devbot 落灰，实际 %v", got)
	}
}

func declareUnwired(persona string) error {
	b, err := os.ReadFile(persona)
	if err != nil {
		return err
	}
	patched := strings.Replace(string(b), "disabled: false", "disabled: false\nunwired: true", 1)
	if patched == string(b) {
		return os.ErrNotExist
	}
	return os.WriteFile(persona, []byte(patched), 0o600)
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceRenderDryRunDoesNotWrite(t *testing.T) {
	out := filepath.Join(t.TempDir(), "unit.xml")
	code := quiet(t, func() int {
		return cmdService([]string{"render", "--org", fixturePath(t, "one"), "--bot", "alice",
			"--goos", "windows", "--out", out})
	})
	if code != 0 {
		t.Fatalf("退出码 %d", code)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("默认是 dry-run，不该落盘")
	}
}

func TestServiceRenderApplyThenRefusesForeign(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "unit.xml")
	argv := []string{"render", "--org", fixturePath(t, "one"), "--bot", "alice",
		"--goos", "windows", "--account", `M\alice`, "--out", out, "--apply"}
	if code := quiet(t, func() int { return cmdService(argv) }); code != 0 {
		t.Fatalf("落盘退出码 %d", code)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "anc:generated") {
		t.Fatal("落盘的单元没有指纹")
	}
	// 再跑一次：内容一致就该说「已是同一份」，而不是无脑重写。
	if code := quiet(t, func() int { return cmdService(argv) }); code != 0 {
		t.Fatalf("重跑退出码 %d", code)
	}

	foreign := filepath.Join(dir, "foreign.xml")
	if err := os.WriteFile(foreign, []byte("<xml>别人的</xml>"), 0o600); err != nil {
		t.Fatal(err)
	}
	code := quiet(t, func() int {
		return cmdService([]string{"render", "--org", fixturePath(t, "one"), "--bot", "alice",
			"--goos", "windows", "--out", foreign, "--apply"})
	})
	if code != 1 {
		t.Fatalf("他源文件应当拒绝覆盖，退出码 %d", code)
	}
	if fb, _ := os.ReadFile(foreign); string(fb) != "<xml>别人的</xml>" {
		t.Fatal("拒绝时不该动对方的文件")
	}
}

func TestServiceRejectsDisabledOrUnknownBot(t *testing.T) {
	cases := []struct {
		name string
		bot  string
		org  string
	}{
		{"没有这个成员", "nobody", "one"},
		{"成员已停用", "frank", "disabled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code := quiet(t, func() int {
				return cmdService([]string{"render", "--org", fixturePath(t, c.org), "--bot", c.bot})
			})
			if code != 1 {
				t.Fatalf("退出码 %d，期望 1", code)
			}
		})
	}
}

func TestServiceUninstallIsDryRunByDefault(t *testing.T) {
	code := quiet(t, func() int {
		return cmdService([]string{"uninstall", "--org", fixturePath(t, "one"), "--bot", "alice"})
	})
	if code != 0 {
		t.Fatalf("退出码 %d —— 不加 --apply 时只该打印", code)
	}
}

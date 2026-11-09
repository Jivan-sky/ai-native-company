package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSecretName(t *testing.T) {
	yes := []string{"secrets.env", ".env", ".env.local", "server.pem", "tls.KEY", "a.p12", "id_rsa", "id_ed25519.pub"}
	no := []string{"notes.md", "domains.md", "CLAUDE.md", "secrets.md", "env.md"}
	for _, n := range yes {
		if !secretName(n) {
			t.Errorf("%q 应当被当成秘密文件", n)
		}
	}
	for _, n := range no {
		if secretName(n) {
			t.Errorf("%q 不该被当成秘密文件（多报会让人对这条失去信任）", n)
		}
	}
}

// 真相源扫秘密文件时**必须跳过 .git** —— 否则每个仓库都会因为 .git 里的东西整条报错。
func TestSecretFilesInSkipsGitDir(t *testing.T) {
	v := t.TempDir()
	mustWrite(t, filepath.Join(v, "secrets.env"), "X=1\n")
	mustWrite(t, filepath.Join(v, "id_rsa"), "k\n")
	mustWrite(t, filepath.Join(v, ".git", "secrets.env"), "不该被报\n")
	mustWrite(t, filepath.Join(v, "notes.md"), "# 正常文件\n")

	got, err := secretFilesIn(v)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"id_rsa", "secrets.env"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("扫出来的是 %v，期望 %v（要排序、要跳过 .git、不许报 .md）", got, want)
	}
}

// 越权模式：devbot 允许 bypass，其余角色一个都不许。判据看的是**文件**，不是 org 的解析结果。
func TestBypassRoles(t *testing.T) {
	v := t.TempDir()
	mustWrite(t, filepath.Join(v, "roles", "manager", "persona.md"), "---\nrole: manager\nmode: bypassPermissions\n---\n")
	mustWrite(t, filepath.Join(v, "roles", "ops", "persona.md"), "---\nrole: ops\nmode: dontAsk\n---\n")
	mustWrite(t, filepath.Join(v, "roles", "devbot", "persona.md"), "---\nrole: devbot\nmode: bypassPermissions\n---\n")

	got, err := bypassRoles(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "roles/manager" {
		t.Fatalf("只该报 manager，实际 %v（devbot 是允许的；ops 是 dontAsk）", got)
	}
}

// 权限：不存在 = 跳过（不是失败），存在但开着 = 失败，且给出能照抄的 chmod。
func TestPermItem(t *testing.T) {
	dir := t.TempDir()

	missing := permItem("x", filepath.Join(dir, "nope"), 0o600)
	if !missing.OK || !missing.Skip {
		t.Fatalf("不存在的东西该是「跳过」，不是「通过」也不是「失败」：%+v", missing)
	}

	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := permItem("f", p, 0o600); !got.OK || got.Skip {
		t.Fatalf("0600 该通过：%+v", got)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	bad := permItem("f", p, 0o600)
	if bad.OK || bad.Skip {
		t.Fatalf("0644 该失败：%+v", bad)
	}
	if !strings.Contains(bad.Fix, "chmod 600") {
		t.Fatalf("FIX 要能照抄：%q", bad.Fix)
	}
}

// 端到端：真相源里躺着一个 secrets.env → 退出码 1 且点名；删掉之后不再有需要处理的项。
func TestAuditConfigCLI(t *testing.T) {
	root := t.TempDir()
	vault := filepath.Join(root, "acme")
	home := filepath.Join(root, "home")
	mustWrite(t, filepath.Join(vault, "roles", "manager", "persona.md"), "---\nrole: manager\nmode: dontAsk\n---\n")
	mustWrite(t, filepath.Join(vault, "secrets.env"), "FEISHU_APP_SECRET=abc\n")

	out, code := capture(t, func() int {
		return cmdAuditConfig([]string{vault, "--home", home})
	})
	if code != 1 {
		t.Fatalf("有秘密文件在真相源里，应当退出码 1，得到 %d：\n%s", code, out)
	}
	if !strings.Contains(out, "真相源里的秘密文件") || !strings.Contains(out, "secrets.env") {
		t.Fatalf("要指名道姓说清楚是哪个文件：\n%s", out)
	}

	if err := os.Remove(filepath.Join(vault, "secrets.env")); err != nil {
		t.Fatal(err)
	}
	out, code = capture(t, func() int {
		return cmdAuditConfig([]string{vault, "--home", home})
	})
	if code != 0 {
		t.Fatalf("清干净之后该退出码 0，得到 %d：\n%s", code, out)
	}
	if !strings.Contains(out, "没有需要处理的项") {
		t.Fatalf("结论要说清「没有需要处理的项」：\n%s", out)
	}
}

// --json 是给机器读的：字段必须齐，且 Skip 与 OK 分开（混在一起读的人会以为「还没装」=「装得好」）。
func TestAuditConfigJSON(t *testing.T) {
	root := t.TempDir()
	vault := filepath.Join(root, "acme")
	home := filepath.Join(root, "home")
	mustWrite(t, filepath.Join(vault, "notes.md"), "# 空\n")

	out, code := capture(t, func() int {
		return cmdAuditConfig([]string{vault, "--home", home, "--json"})
	})
	if code != 0 {
		t.Fatalf("干净目录该通过，得到 %d：\n%s", code, out)
	}
	if !strings.Contains(out, `"items"`) || !strings.Contains(out, `"skip"`) {
		t.Fatalf("JSON 结构不对：\n%s", out)
	}
}

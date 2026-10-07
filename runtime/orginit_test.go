package main

import (
	"os"
	"path/filepath"
	"testing"

	"anc/internal/org"
)

// 生成的骨架必须**自带能过校验、且能渲染**的性质 ——
// 否则「模板生成的东西跑不起来」这种坑要等驻场那天才发现。
func TestOrgInitProducesUsableVault(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	if code := quiet(t, func() int {
		return cmdOrgInit([]string{dir, "--client", "某车队", "--id", "demo", "--members", "alice,bob"})
	}); code != 0 {
		t.Fatalf("生成退出码 %d", code)
	}

	for _, p := range []string{
		"company/company.md",
		"domains.md",
		"roles/manager/persona.md",
		"roles/devbot/persona.md",
		"members/alice/persona.md",
		"members/bob/persona.md",
		"members/devbot/persona.md",
		"10-knowledge/CLAUDE.md",
		"20-ops/CLAUDE.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Fatalf("缺文件 %s：%v", p, err)
		}
	}

	o, err := org.Load(dir)
	if err != nil {
		t.Fatalf("生成的骨架没通过校验：%v", err)
	}
	if len(o.Enabled()) != 3 {
		t.Fatalf("启用成员 %d 个，期望 3 个（alice / bob / devbot）", len(o.Enabled()))
	}
	// 占位符是故意的：allowed_tools 没填就该有告警顶着，不能静默当「已经配好了」。
	if len(o.Warnings) == 0 {
		t.Fatal("allowed_tools 空着却没告警 —— 骨架会被当成成品")
	}

	cfg := filepath.Join(t.TempDir(), "gateway", "config.toml")
	if code := quiet(t, func() int {
		return cmdRender([]string{dir, "--config", cfg, "--apply"})
	}); code != 0 {
		t.Fatalf("骨架应当能直接渲染，退出码 %d", code)
	}
}

func TestOrgInitRefusesBadInput(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"id 不是 ascii", []string{"--client", "某车队", "--id", "Demo Co"}, 2},
		{"id 大写", []string{"--client", "某车队", "--id", "Demo"}, 2},
		{"缺 id", []string{"--client", "某车队"}, 2},
		{"devbot 不该手写", []string{"--client", "某车队", "--id", "demo", "--members", "alice,devbot"}, 2},
		{"admin 不在名单里", []string{"--client", "某车队", "--id", "demo", "--admin", "carol"}, 2},
		{"日期格式不对", []string{"--client", "某车队", "--id", "demo", "--date", "2026/10/06"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{filepath.Join(t.TempDir(), "v")}, c.args...)
			if code := quiet(t, func() int { return cmdOrgInit(args) }); code != c.want {
				t.Fatalf("退出码 %d，期望 %d", code, c.want)
			}
		})
	}
}

func TestOrgInitProtectsNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "已有文件.txt"), []byte("别踩我"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{dir, "--client", "某车队", "--id", "demo"}
	if code := quiet(t, func() int { return cmdOrgInit(base) }); code != 1 {
		t.Fatalf("非空目录应当拒绝，退出码 %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "company", "company.md")); !os.IsNotExist(err) {
		t.Fatal("拒绝时不该写入任何东西")
	}
	if code := quiet(t, func() int { return cmdOrgInit(append(base, "--force")) }); code != 0 {
		t.Fatal("--force 后应当生成")
	}
}

func TestSplitListAndDisplayName(t *testing.T) {
	if got := splitList(" a, b ,,c "); len(got) != 3 || got[2] != "c" {
		t.Fatalf("切分结果不对：%v", got)
	}
	if got := displayName("alice"); got != "Alice" {
		t.Fatalf("displayName(alice) = %q", got)
	}
	if got := displayName("张伟"); got != "张伟" {
		t.Fatalf("中文名不该被动：%q", got)
	}
}

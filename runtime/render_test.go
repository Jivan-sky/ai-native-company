package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"anc/internal/org"
	renderpkg "anc/internal/render"
)

var update = flag.Bool("update", false, "重写结构快照")

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "orgs", name))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func copyFixture(t *testing.T, name string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	if err := os.CopyFS(dst, os.DirFS(fixturePath(t, name))); err != nil {
		t.Fatal(err)
	}
	return dst
}

// quiet 把子命令的 stdout 吞掉，失败信息才看得清。
func quiet(t *testing.T, f func() int) int {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	old := os.Stdout
	os.Stdout = devnull
	defer func() { os.Stdout = old }()
	return f()
}

func writePersona(t *testing.T, vault, dir, body string) {
	t.Helper()
	p := filepath.Join(vault, "members", dir)
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "persona.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 目标配置不存在时，--check 必须报「需要 apply」并以 1 退出，
// 不能因为「没得比」就回一个假的 OK。
func TestCheckMissingTargetExitsOne(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gateway", "config.toml")
	code := quiet(t, func() int {
		return cmdRender([]string{fixturePath(t, "one"), "--config", cfg, "--check"})
	})
	if code != 1 {
		t.Fatalf("退出码 %d，期望 1", code)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Fatal("--check 不该写盘")
	}
}

func TestDryRunDoesNotWrite(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gateway", "config.toml")
	if code := quiet(t, func() int {
		return cmdRender([]string{fixturePath(t, "one"), "--config", cfg})
	}); code != 0 {
		t.Fatalf("退出码 %d，期望 0", code)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Fatal("默认是 dry-run，不该落盘")
	}
}

func TestApplyThenCheckIsClean(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gateway", "config.toml")
	v := fixturePath(t, "one")
	if code := quiet(t, func() int { return cmdRender([]string{v, "--config", cfg, "--apply"}) }); code != 0 {
		t.Fatalf("落盘退出码 %d，期望 0", code)
	}
	if code := quiet(t, func() int { return cmdRender([]string{v, "--config", cfg, "--check"}) }); code != 0 {
		t.Fatalf("刚写完就报漂移（退出码 %d），说明漂移检测自相矛盾", code)
	}
}

// 手写/他源的配置不许被静默覆盖：没有 anc 指纹就是别人的文件。
func TestApplyRefusesForeignConfig(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gateway", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	original := "# 人手写的配置，不许被踩\n[[projects]]\nname = \"手工\"\n"
	if err := os.WriteFile(cfg, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	v := fixturePath(t, "one")
	if code := quiet(t, func() int { return cmdRender([]string{v, "--config", cfg, "--apply"}) }); code != 1 {
		t.Fatalf("应当拒绝覆盖，退出码 %d", code)
	}
	if b, _ := os.ReadFile(cfg); string(b) != original {
		t.Fatal("拒绝时也不许改动对方的文件")
	}
	// 接管他源配置时，project 集合必然全变，所以两道门是独立的两件事：
	// --adopt 认「这份文件归我」，--allow-scale 认「bot 集合要变」。都得显式过。
	if code := quiet(t, func() int { return cmdRender([]string{v, "--config", cfg, "--apply", "--adopt"}) }); code != 1 {
		t.Fatalf("只 --adopt 不该顺带放行 project 集合变化，退出码 %d", code)
	}
	if code := quiet(t, func() int {
		return cmdRender([]string{v, "--config", cfg, "--apply", "--adopt", "--allow-scale"})
	}); code != 0 {
		t.Fatalf("--adopt + --allow-scale 后应当接管，退出码 %d", code)
	}
	if code := quiet(t, func() int { return cmdRender([]string{v, "--config", cfg, "--check"}) }); code != 0 {
		t.Fatal("接管后应当一致")
	}
}

// 一次误操作不该静默把 bot 上下线：新增/删除 project 必须显式点头。
func TestApplyRefusesScaleChange(t *testing.T) {
	v := copyFixture(t, "one")
	cfg := filepath.Join(filepath.Dir(v), "gateway", "config.toml")
	if code := quiet(t, func() int { return cmdRender([]string{v, "--config", cfg, "--apply"}) }); code != 0 {
		t.Fatal("首次落盘应当成功")
	}
	writePersona(t, v, "carol", "---\nname: carol\ndisplay_name: Carol\nrole: manager\nfeishu:\n  app_id: cli_demo_c\n  open_id: ou_demo_c\n---\n- 新同事\n")

	if code := quiet(t, func() int { return cmdRender([]string{v, "--config", cfg, "--apply"}) }); code != 1 {
		t.Fatalf("新增 project 应当被拦，退出码 %d", code)
	}
	if code := quiet(t, func() int {
		return cmdRender([]string{v, "--config", cfg, "--apply", "--allow-scale"})
	}); code != 0 {
		t.Fatalf("--allow-scale 后应当落盘，退出码 %d", code)
	}
	if code := quiet(t, func() int { return cmdRender([]string{v, "--config", cfg, "--check"}) }); code != 0 {
		t.Fatal("落盘后应当一致")
	}
}

// 结构快照：只锁语义（project 集合 + persona 哈希 + 行数）。
// 全文 golden 的失败模式是「改一个字就红」，然后人就会习惯性 -update，测试就死了。
func TestStructuralSnapshot(t *testing.T) {
	o, err := org.Load(fixturePath(t, "one"))
	if err != nil {
		t.Fatal(err)
	}
	host := org.Host{VaultRoot: "/vault", HomesRoot: "/homes", DataDir: "/data"}
	plan, err := renderpkg.Build(o, renderpkg.Options{Host: host, Version: version, Now: time.Unix(0, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	got := snapshotOf(plan)
	golden := filepath.Join("testdata", "golden", "one.snapshot")
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("已重写 %s", golden)
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v（首次生成跑 go test ./... -run TestStructuralSnapshot -update）", err)
	}
	if got != string(want) {
		t.Fatalf("结构快照不符\n--- 实际\n%s\n--- 期望\n%s\n确认是语义变了再 -update", got, want)
	}
}

func snapshotOf(p *renderpkg.Plan) string {
	var b strings.Builder
	b.WriteString("# 结构快照：只锁语义。全文 golden 会因文案改动反复变红，然后被习惯性 -update 掉。\n")
	fmt.Fprintf(&b, "projects=%s\n", strings.Join(p.Projects, ","))
	names := append([]string(nil), p.Projects...)
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "persona.%s=%s\n", n, p.PersonaHash[n])
	}
	fmt.Fprintf(&b, "lines=%d\n", strings.Count(p.Text, "\n"))
	return b.String()
}

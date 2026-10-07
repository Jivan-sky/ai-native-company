package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"anc/internal/org"
	renderpkg "anc/internal/render"
)

// applyFixture 搭一套「跑得动 apply」的最小现场：拷一份 org 真相源 + 一份凭据文件。
//
// 凭据里的键**从渲染产物自己算出来**，不手抄 —— 手抄的那份会跟着实现悄悄漂。
// daemon 用一个空文件顶替：dry-run 只解析路径，不会去执行它。
func applyFixture(t *testing.T) (vault, cfg, secrets, daemon string) {
	t.Helper()
	vault = copyFixture(t, "one")
	work := t.TempDir()
	cfg = filepath.Join(work, "gateway", "config.toml")
	secrets = filepath.Join(work, "secrets.env")
	daemon = filepath.Join(work, "cc-connect.exe")
	if err := os.WriteFile(daemon, []byte("dummy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := org.Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := renderpkg.Build(o, renderpkg.Options{
		Host:    org.Host{VaultRoot: vault, HomesRoot: filepath.Join(work, "homes"), DataDir: filepath.Join(work, "data")},
		Version: version,
		Now:     time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, ref := range renderpkg.EnvRefs(plan.Text) {
		fmt.Fprintf(&b, "%s=值-%s\n", ref, ref)
	}
	if err := os.WriteFile(secrets, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return vault, cfg, secrets, daemon
}

// dry-run 是现场最先跑的形态：它不许动任何东西 —— 不写 config、不碰 daemon、
// 也不许依赖「只有 apply 之后才存在」的文件（上游装载体就属于这种）。
func TestApplyDryRunTouchesNothing(t *testing.T) {
	vault, cfg, secrets, daemon := applyFixture(t)
	code := quiet(t, func() int {
		return cmdApply([]string{vault, "--config", cfg, "--secrets", secrets, "--daemon", daemon})
	})
	if code != 0 {
		t.Fatalf("dry-run 退出码 = %d，想要 0", code)
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Error("dry-run 写了 config")
	}
}

// 产物引用的键缺一个就不许往下走：装载会先杀掉正在跑的 daemon，
// 一个必然起不来的实例 = 这台机器上原来在跑的 bot 全部下线。
func TestApplyRefusesWhenSecretKeyMissing(t *testing.T) {
	vault, cfg, secrets, daemon := applyFixture(t)
	if err := os.WriteFile(secrets, []byte("# 一个键都没写\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code := quiet(t, func() int {
		return cmdApply([]string{vault, "--config", cfg, "--secrets", secrets, "--daemon", daemon})
	})
	if code != 1 {
		t.Fatalf("缺键时退出码 = %d，想要 1", code)
	}
}

// 凭据文件读不到 = 同样的结论（不装载），但要说清是「读不到」而不是「缺键」。
func TestApplyRefusesWhenSecretsFileMissing(t *testing.T) {
	vault, cfg, secrets, daemon := applyFixture(t)
	code := quiet(t, func() int {
		return cmdApply([]string{vault, "--config", cfg, "--secrets", secrets + ".nope", "--daemon", daemon})
	})
	if code != 1 {
		t.Fatalf("凭据文件不存在时退出码 = %d，想要 1", code)
	}
}

// apply 落盘走的是与 `render --apply` 同一套门禁（同一条实现）：
// 没有 anc 指纹的现有配置不许静默覆盖，且拒绝时一个字节都不许改。
func TestApplyRefusesForeignConfigWithoutAdopt(t *testing.T) {
	vault, cfg, secrets, daemon := applyFixture(t)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	handwritten := "# 人手写的配置，没有 anc 指纹\n"
	if err := os.WriteFile(cfg, []byte(handwritten), 0o600); err != nil {
		t.Fatal(err)
	}
	code := quiet(t, func() int {
		return cmdApply([]string{vault, "--config", cfg, "--secrets", secrets, "--daemon", daemon, "--apply"})
	})
	if code != 1 {
		t.Fatalf("他源配置时退出码 = %d，想要 1", code)
	}
	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != handwritten {
		t.Error("被拒绝时还改了配置")
	}
}

// 阶段 C 先落 Windows 腿：别的平台明确报错，不许把前四步做完再留个读不到凭据的 daemon。
func TestApplyPlatformGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 腿已实现；这条守的是 Linux / macOS 的分支")
	}
	vault, cfg, secrets, daemon := applyFixture(t)
	code := quiet(t, func() int {
		return cmdApply([]string{vault, "--config", cfg, "--secrets", secrets, "--daemon", daemon})
	})
	if code != 1 {
		t.Fatalf("非 Windows 平台退出码 = %d，想要 1（明确报未实现）", code)
	}
}

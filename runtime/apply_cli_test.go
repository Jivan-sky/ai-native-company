package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"anc/internal/apply"
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
	for _, ref := range plan.SecretKeys {
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

// 装载只落 Windows 与 Linux 两腿：别的平台（macOS 等）明确报错，
// 不许把前四步做完再留个读不到凭据的 daemon（macOS 零覆盖，议题 #23）。
func TestApplyPlatformGuard(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		t.Skip("Windows / Linux 两腿都已实现；这条守的是 macOS 等其它平台")
	}
	vault, cfg, secrets, daemon := applyFixture(t)
	code := quiet(t, func() int {
		return cmdApply([]string{vault, "--config", cfg, "--secrets", secrets, "--daemon", daemon})
	})
	if code != 1 {
		t.Fatalf("非 Windows/Linux 平台退出码 = %d，想要 1（明确报未实现）", code)
	}
}

// 「daemon 没起来」时给的那句下一步必须按平台分岔 —— 两个平台要查的东西完全不同
// （Windows 是计划任务 + 电源条件，Linux 是 systemd 单元 + drop-in 的 reload）。
// 这条只钉「答得上话、且两边不一样」，不钉文案细节：改词不该让测试红。
func TestStartupHintIsPlatformSpecific(t *testing.T) {
	win := startupHint("windows")
	lin := startupHint("linux")
	if win == "" || lin == "" {
		t.Fatal("两个平台都该有话说")
	}
	if win == lin {
		t.Error("Windows 与 Linux 该给的下一步不同 —— 要查的地方本来就不一样")
	}
	if !strings.Contains(lin, "systemctl") {
		t.Errorf("Linux 那句该指向 systemctl，实际：%s", lin)
	}
	if !strings.Contains(win, "schtasks") {
		t.Errorf("Windows 那句该指向计划任务，实际：%s", win)
	}
	if startupHint("darwin") == "" {
		t.Error("没覆盖的平台也要给一句兜底话，不许空手")
	}
}

// 成员名改成一个拼不出合法键名的形状之后，装载必须拦 —— 而且是在**渲染之前**就拦。
//
// 这条是「静默」变「出声」的落点：改名之前，产物里那个键回读不到，体检拿着自己
// 漏掉一个键的清单报「✅ 齐」，现场等来的是一个没有凭据、起不来的 bot。
// 现在红档拦在真相源那一层，命令连渲染都进不去。
func TestApplyBlocksUnwritableSecretKeyName(t *testing.T) {
	vault, cfg, secrets, daemon := applyFixture(t)
	// 改名要连着 company.md 的 admins 一起改：不跟着改会被 company.admins.unknown 拦下，
	// 退出码同样是 1，但拦下的就不是本用例要测的那条规则了。
	for _, rel := range []string{
		filepath.Join("members", "alice", "persona.md"),
		filepath.Join("company", "company.md"),
	} {
		path := filepath.Join(vault, rel)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.ReplaceAll(string(raw), "alice", "alice-2")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code := quiet(t, func() int {
		return cmdApply([]string{vault, "--config", cfg, "--secrets", secrets, "--daemon", daemon})
	})
	if code != 1 {
		t.Fatalf("成员名拼不出合法键名时退出码 = %d，想要 1", code)
	}
	// 那个键即便写进凭据文件也读不懂 —— 解析器与成员名规则用的是同一把尺子。
	if apply.LegalKey("ANC_FEISHU_SECRET_ALICE-2") {
		t.Fatal("ANC_FEISHU_SECRET_ALICE-2 被当成合法键名了：解析尺子漏了")
	}
}

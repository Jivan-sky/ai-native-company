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

// 成员名改成一个**键名写得出、但凭据文件里还没有**的形状之后，装载必须停在体检那一步。
//
// 2026-10-08 改：键名是全函数派生（alice-2 → ANC_FEISHU_SECRET_ALICEx00002d2），所以拦下它的
// 不再是「写不出来」，而是「这份凭据文件里没这个键」。派生本身的判据在 render 的用例里。
func TestApplyBlocksMissingKeyForRenamedMember(t *testing.T) {
	vault, cfg, secrets, daemon := applyFixture(t)
	renameMemberInPlace(t, vault, "alice", "alice-2")
	code := quiet(t, func() int {
		return cmdApply([]string{vault, "--config", cfg, "--secrets", secrets, "--daemon", daemon})
	})
	if code != 1 {
		t.Fatalf("凭据文件里没有改名后的键时退出码 = %d，想要 1", code)
	}
	// 键名本身是写得出的一档 —— 否则又回到「照着 FIX 补也永远补不上」那个形状
	if !apply.LegalKey(renderpkg.FeishuSecretKey("alice-2")) {
		t.Fatal("编码后的键名应当合法：装载侧那把尺子不收它")
	}
}

// 中文成员名 + 凭据文件里有那个转义键 → dry-run 一路绿灯（[1/7]…[7/7] 都不拦）。
//
// 这是「中文名能跑通」在命令这一层的落点：真相源校验（名字当得了目录名）+ 渲染（键名写得出）
// + 体检（凭据文件里真有这个键）三关全过。上游收不收中文 project 名 / 中文 work_dir 见
// runtime/DESIGN.md §7.1.16 的「未验」一节 —— 这一条只到装载前的 dry-run。
func TestApplyAcceptsChineseMemberName(t *testing.T) {
	vault, cfg, secrets, daemon := applyFixture(t)
	renameMemberInPlace(t, vault, "alice", "张三")
	// 名字改了，键名跟着改：按公开的派生算法算出来，往凭据文件里补这一条（不手抄常量）
	key := renderpkg.FeishuSecretKey("张三")
	f, err := os.OpenFile(secrets, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(f, "%s=值-%s\n", key, key); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	code := quiet(t, func() int {
		return cmdApply([]string{vault, "--config", cfg, "--secrets", secrets, "--daemon", daemon})
	})
	if code != 0 {
		t.Fatalf("中文成员名的 dry-run 退出码 = %d，想要 0（键名 %s）", code, key)
	}
}

// renameMemberInPlace 就地改成员名：persona.md 与 company.md 里的引用一起改。
// 不跟 company.md 一起改会被 company.admins.unknown 先拦下 —— 退出码同样是 1，
// 但拦下的就不是本用例要测的那条了。
func renameMemberInPlace(t *testing.T, vault, from, to string) {
	t.Helper()
	for _, rel := range []string{
		filepath.Join("members", from, "persona.md"),
		filepath.Join("company", "company.md"),
	} {
		path := filepath.Join(vault, rel)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.ReplaceAll(string(raw), from, to)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

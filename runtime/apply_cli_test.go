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

// ---- 第 7 步的就绪判据（2026-10-10 沙箱真跑之后补的）----------------------------------------
//
// 实测现场：一份产物里有一个 `unwired: true` 的成员 bot（占位 app_id）。上游对它的两种表现都撞到过 ——
// 一次是 `platform ready` 紧跟 `websocket error: app_id is invalid`（先绿后死），
// 另一次是 `failed to create platform` 干脆不打那一行。
// 旧判据 `want = len(plan.Projects)` 把这种 project 也算进期望值：前者报假绿（2/2），
// 后者报假红（1/2）—— 同一个事实，两种错误读数。

// readyOrgFixture 拷一份 one 组织，把 devbot 声明成 unwired（**声明**，不是拿 app_id 长相猜的），
// 再渲染一遍：回读判据拿到的就是「谁该被等」这一个集合。
func readyOrgFixture(t *testing.T) (*org.Org, *renderpkg.Plan) {
	t.Helper()
	vault := copyFixture(t, "one")
	path := filepath.Join(vault, "members", "devbot", "persona.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(raw), "\ndisabled: false\n", "\ndisabled: false\nunwired: true\n", 1)
	if body == string(raw) {
		t.Fatal("夹具变了：members/devbot/persona.md 里没有 `disabled: false` 那一行")
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := org.Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	plan, err := renderpkg.Build(o, renderpkg.Options{
		Host:    org.Host{VaultRoot: vault, HomesRoot: filepath.Join(work, "homes"), DataDir: filepath.Join(work, "data")},
		Version: version,
		Now:     time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return o, plan
}

// 期望值里不能有声明未接线的那个 —— 否则同一个部署会一次假绿一次假红。
func TestReadyExpectationExcludesUnwiredProjects(t *testing.T) {
	o, plan := readyOrgFixture(t)
	if len(plan.Projects) != 2 {
		t.Fatalf("夹具应当渲染 2 个 project（unwired 只是不参与就绪判据，不是不渲染），实际 %v", plan.Projects)
	}
	expect := readyExpectation(plan, o)
	if len(expect) != 1 || !expect["demo-alice"] {
		t.Fatalf("期望值应当只剩接线的那个，实际 %v", expect)
	}
}

// 没有可期待的 project 时不许打绿：空集上的「都绿了」是最纯的那种假绿。
func TestReadyExpectationEmptyWhenAllUnwired(t *testing.T) {
	o, plan := readyOrgFixture(t)
	// 把接线的那个也声明成未接线，期望值就该是空的。
	path := filepath.Join(o.Root, "members", "alice", "persona.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(raw), "\ndisabled: false\n", "\ndisabled: false\nunwired: true\n", 1)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	o2, err := org.Load(o.Root)
	if err != nil {
		t.Fatal(err)
	}
	if got := readyExpectation(plan, o2); len(got) != 0 {
		t.Fatalf("全声明未接线时期望值应当是空的，实际 %v", got)
	}
}

// countReady 数的是**期望里的**名字，不是 `platform ready` 的行数。
func TestCountReadyCountsOnlyExpectedProjects(t *testing.T) {
	log := writeReadyLog(t,
		`time=1 level=INFO msg="platform ready" project=demo-devbot platform=feishu`,
		`time=2 level=ERROR msg="feishu: websocket error" error="1000040346: app_id is invalid"`,
		`time=3 level=INFO msg="platform ready" project=demo-alice platform=feishu`,
	)
	if got, _ := countReady(log, 0, map[string]bool{"demo-alice": true}); got != 1 {
		t.Errorf("ready = %d，想要 1（不在期望里的 devbot 不该算）", got)
	}
	if got, _ := countReady(log, 0, map[string]bool{"demo-alice": true, "demo-devbot": true}); got != 2 {
		t.Errorf("ready = %d，想要 2（两个都在期望里）", got)
	}
}

// project 名 = `<公司 id>-<成员名>`，成员名可以带空格 —— 那时上游给值加引号，两种写法都要认。
// 只认一种，带空格的名字会静默数不到，判据就退回「按行数」那套假绿。
func TestCountReadyHandlesQuotedProjectNames(t *testing.T) {
	log := writeReadyLog(t, `time=1 level=INFO msg="platform ready" project="demo-张 三" platform=feishu`)
	if got, _ := countReady(log, 0, map[string]bool{"demo-张 三": true}); got != 1 {
		t.Errorf("带空格的 project 名没认出来：ready = %d，想要 1", got)
	}
}

// 只数**重启之后新增**的那段：上一次的绿字不许盖住这一次的没起来。
func TestCountReadyCountsOnlyNewLines(t *testing.T) {
	log := writeReadyLog(t, `time=1 level=INFO msg="platform ready" project=demo-alice platform=feishu`)
	expect := map[string]bool{"demo-alice": true, "demo-bob": true}
	if got, _ := countReady(log, 0, expect); got != 1 {
		t.Fatalf("首段 ready = %d，想要 1", got)
	}
	fi, err := os.Stat(log)
	if err != nil {
		t.Fatal(err)
	}
	from := fi.Size()
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("time=2 level=INFO msg=\"platform ready\" project=demo-bob platform=feishu\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got, _ := countReady(log, from, expect); got != 1 {
		t.Errorf("从偏移起 ready = %d，想要 1（只算新增那段）", got)
	}
}

func writeReadyLog(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cc-connect.log")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/org"
	renderpkg "anc/internal/render"
	"anc/internal/service"
)

const serviceUsage = `anc service —— 用户级服务定义（一个 bot 一套）

用法：
  anc service render    --org <vault> --bot <成员> [--goos darwin|linux|windows] [--out <文件>] [--apply] [--adopt]
  anc service status    --org <vault> --bot <成员>
  anc service install   --org <vault> --bot <成员> --apply
  anc service uninstall --org <vault> --bot <成员> --apply

选项：
  --homes <目录>   每 bot 的家目录根（必须与 anc render 的 --homes 一致，否则 cwd 对不上）
  --account <账号> Windows 计划任务的运行账号；其余平台 = 谁装就是谁（单元里不写账号）
  --goos <平台>    生成哪个平台的单元（默认本机）
  --out <文件>     单元落点（默认按平台约定：LaunchAgents / systemd --user / 计划任务 XML）
  --apply          真落盘、真执行装载命令（默认只打印要看的东西）
  --adopt          接管一份没有 anc 指纹的现有单元文件（只挡「别人的文件」，不挡你）

这是**用户级**服务（macOS LaunchAgent / systemd --user / Windows 登录时计划任务），
不写系统级 daemon，不需要管理员权限。要哪个账号跑，就以哪个账号执行 install。

退出码：0 成功/一致；1 校验或漂移；2 用法错误
`

func cmdService(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, serviceUsage)
		return 2
	}
	sub := args[0]
	switch sub {
	case "render", "status", "install", "uninstall":
	default:
		fmt.Fprintf(os.Stderr, "错误：未知子命令 %q\n\n", sub)
		fmt.Fprint(os.Stderr, serviceUsage)
		return 2
	}

	fs := flag.NewFlagSet("service "+sub, flag.ContinueOnError)
	orgDir := fs.String("org", "", "org 真相源目录（vault）")
	bot := fs.String("bot", "", "成员名")
	homes := fs.String("homes", "", "每 bot 的家目录根")
	goosFlag := fs.String("goos", "", "目标平台")
	out := fs.String("out", "", "单元落点")
	account := fs.String("account", "", "Windows 计划任务的运行账号")
	apply := fs.Bool("apply", false, "真落盘 / 真执行")
	adopt := fs.Bool("adopt", false, "接管没有指纹的现有文件")
	flagArgs, posArgs := splitArgs(args[1:], map[string]bool{"apply": true, "adopt": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	root := *orgDir
	if root == "" && len(posArgs) > 0 {
		root = posArgs[0]
	}
	if root == "" || *bot == "" {
		fmt.Fprint(os.Stderr, serviceUsage)
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 2
	}
	o, err := org.Load(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 1
	}
	printIssues(o.Warnings)
	m, ok := o.Member(*bot)
	if !ok {
		fmt.Fprintf(os.Stderr, "错误：%s 里没有成员 %q\n", abs, *bot)
		return 1
	}
	if m.Disabled {
		fmt.Fprintf(os.Stderr, "错误：成员 %q 是停用状态 —— 停用的 bot 不该有服务单元\n", *bot)
		return 1
	}

	homesRoot := *homes
	if homesRoot == "" {
		homesRoot = filepath.Join(filepath.Dir(abs), "homes")
	}
	host := org.Host{VaultRoot: abs, HomesRoot: homesRoot, DataDir: filepath.Join(filepath.Dir(abs), "data")}

	// 用**自己**的绝对路径：相对路径写进单元后，装载时 cwd 一变就找不到自己。
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：拿不到 anc 自身路径：%v\n", err)
		return 1
	}
	if self, err = filepath.Abs(self); err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}

	a, err := service.Render(service.Spec{
		CompanyID: o.Company.ID,
		Bot:       m.Name,
		AncBin:    self,
		WorkDir:   renderpkg.WorkDir(m, host),
		LogDir:    filepath.Join(homesRoot, m.Name, "state"),
		Account:   *account,
		GOOS:      *goosFlag,
		Version:   version,
		Now:       time.Now(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 2
	}
	target := *out
	if target == "" {
		target = service.ExpandHome(a.Path)
	}

	fmt.Printf("anc service %s —— %s\n", sub, a.Kind)
	fmt.Printf("  bot      %s（服务名 %s）\n", m.Name, serviceLabel(a, o.Company.ID, m.Name))
	fmt.Printf("  平台     %s\n", a.GOOS)
	fmt.Printf("  单元     %s\n", target)
	fmt.Printf("  启动     %s serve --bot %s\n", self, m.Name)
	fmt.Printf("  cwd      %s\n", renderpkg.WorkDir(m, host))
	fmt.Printf("  日志     %s\n", filepath.Join(homesRoot, m.Name, "state"))
	fmt.Println()
	for _, n := range a.Notes {
		fmt.Printf("  ⚠️  %s\n", n)
	}

	switch sub {
	case "render":
		return serviceRender(a, target, *apply, *adopt)
	case "status":
		return serviceStatus(a, target)
	case "install":
		if !*apply {
			fmt.Printf("\n将写入 %s\n", target)
			fmt.Printf("将执行 %s\n", strings.Join(expandAll(a.Install), " "))
			fmt.Printf("\n（dry-run，什么都没做；要真装加 --apply）\n")
			return 0
		}
		if code := serviceRender(a, target, true, *adopt); code != 0 {
			return code
		}
		out, err := runArgv(expandAll(a.Install))
		fmt.Printf("\n$ %s\n%s", strings.Join(expandAll(a.Install), " "), out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：装载失败：%v\n", err)
			return 1
		}
		fmt.Printf("\n结论：已装载。用 `anc service status --org %s --bot %s` 看它起没起。\n", abs, m.Name)
		return 0
	case "uninstall":
		if !*apply {
			fmt.Printf("\n将执行 %s\n", strings.Join(expandAll(a.Uninstall), " "))
			fmt.Printf("\n（dry-run，什么都没做；要真卸加 --apply）\n")
			return 0
		}
		out, err := runArgv(expandAll(a.Uninstall))
		fmt.Printf("\n$ %s\n%s", strings.Join(expandAll(a.Uninstall), " "), out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：卸载失败：%v\n", err)
			return 1
		}
		fmt.Printf("\n结论：已卸载。单元文件还在 %s，自己决定要不要删（我不替你删文件）。\n", target)
		return 0
	}
	return 2
}

// serviceRender 写单元文件：默认只打印，--apply 才落盘。指纹纪律与 config 一致。
func serviceRender(a service.Artifact, target string, apply, adopt bool) int {
	if !apply {
		fmt.Printf("\n--- %s ---\n%s", target, a.Content)
		fmt.Printf("\n（dry-run，未落盘；要落盘加 --apply）\n")
		return 0
	}
	existing, readErr := os.ReadFile(target)
	hasExisting := readErr == nil
	if hasExisting {
		_, oldInputs, _, oldFP := service.Fingerprint(string(existing))
		_, newInputs, _, _ := service.Fingerprint(a.Content)
		if !oldFP && !adopt {
			fmt.Fprintf(os.Stderr, "\n拒绝覆盖：%s 没有 anc 指纹（手写或他源文件）。确认要接管再加 --adopt。\n", target)
			return 1
		}
		if oldFP && oldInputs == newInputs && string(existing) == a.Content {
			fmt.Printf("\n结论：已是同一份（含指纹），无需重写。\n")
			return 0
		}
		fmt.Printf("\n  覆盖     %s（原有文件先备份）\n", target)
	}
	if hasExisting {
		backup := fmt.Sprintf("%s.bak-%s", target, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backup, existing, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 备份失败: %v\n", err)
			return 1
		}
		fmt.Printf("  备份     %s\n", backup)
	}
	if err := writeAtomic(target, a.Content); err != nil {
		fmt.Fprintf(os.Stderr, "错误: 落盘失败: %v\n", err)
		return 1
	}
	fmt.Printf("  写入     %s\n", target)
	return 0
}

// serviceStatus 只读：文件在不在、指纹对不对、服务管理器认不认它。
func serviceStatus(a service.Artifact, target string) int {
	fmt.Println()
	b, err := os.ReadFile(target)
	if err != nil {
		fmt.Printf("  单元文件 不存在（%s）—— 还没装，或装在别处\n", target)
		fmt.Printf("\n结论：未装载。\n")
		return 1
	}
	ver, inputs, at, ok := service.Fingerprint(string(b))
	if !ok {
		fmt.Printf("  单元文件 存在但**没有 anc 指纹** —— 手写或他源文件，anc 不认它是自己的产物\n")
	} else {
		_, wantInputs, _, _ := service.Fingerprint(a.Content)
		state := "与本次输入一致"
		if inputs != wantInputs {
			state = "与本次输入不一致（改过 org 或路径了，重跑 render --apply）"
		}
		fmt.Printf("  单元文件 有指纹 v=%s inputs=%.12s at=%s —— %s\n", ver, inputs, at, state)
	}
	argv := expandAll(a.Status)
	out, err := runArgv(argv)
	fmt.Printf("\n$ %s\n%s", strings.Join(argv, " "), out)
	if err != nil {
		fmt.Printf("\n结论：服务管理器里没有它（或没在跑）—— 文件在，但没装载。\n")
		return 1
	}
	fmt.Printf("\n结论：服务管理器认得它。\n")
	return 0
}

func serviceLabel(a service.Artifact, companyID, bot string) string {
	switch a.Kind {
	case "launchd":
		return "com.anc." + companyID + "." + bot
	case "systemd":
		return "anc-" + companyID + "-" + bot + ".service"
	default:
		return `ANC\` + companyID + `\` + bot
	}
}

// expandAll 把 argv 里的 ~/ 展开成真实路径（只在真的要执行时用）。
func expandAll(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, a := range argv {
		out = append(out, service.ExpandHome(a))
	}
	return out
}

func runArgv(argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("空命令")
	}
	c := exec.Command(argv[0], argv[1:]...)
	b, err := c.CombinedOutput()
	return string(b), err
}

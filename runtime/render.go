package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"anc/internal/org"
	renderpkg "anc/internal/render"
)

const renderUsage = `anc render / anc org check —— org 真相源 → gateway config

用法：
  anc org check <vault 目录>                     只加载 + 校验 org，不渲染
  anc render <vault 目录> [选项]                 渲染 gateway config（默认 dry-run）

选项：
  --config <文件>    输出的 config.toml（默认 <vault>/../gateway/config.toml）
  --homes <目录>     每 bot 的家目录根（默认 <vault>/../homes）
  --data <目录>      gateway 的 data_dir（默认 <homes>/../data）
  --apply            真落盘（时间戳备份 + 原子写）
  --check            与现有配置比对，有漂移就退出码 1
  --adopt            接管一份没有 anc 指纹的现有配置（否则拒绝覆盖）
  --allow-scale      允许这次渲染新增或删除 project（防一次误操作静默上线/下线 bot）

退出码：0 一致/成功；1 校验或漂移；2 用法错误
`

func cmdRender(args []string) int {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	cfg := fs.String("config", "", "输出的 config.toml")
	homes := fs.String("homes", "", "每 bot 的家目录根")
	data := fs.String("data", "", "gateway 的 data_dir")
	apply := fs.Bool("apply", false, "真落盘")
	check := fs.Bool("check", false, "比对现有配置")
	adopt := fs.Bool("adopt", false, "接管没有指纹的现有配置")
	allowScale := fs.Bool("allow-scale", false, "允许新增/删除 project")
	vault := fs.String("vault", "", "vault 目录（也可用位置参数）")
	// Go 的 flag 包遇到第一个位置参数就停止解析，所以先按「哪些是布尔开关」把位置参数摘出来。
	flagArgs, posArgs := splitArgs(args, map[string]bool{"apply": true, "check": true, "adopt": true, "allow-scale": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	root := strings.TrimSpace(*vault)
	if root == "" && len(posArgs) > 0 {
		root = posArgs[0]
	}
	if root == "" {
		fmt.Fprint(os.Stderr, renderUsage)
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 2
	}
	// 默认路径：vault 的兄弟目录，绝不写进 vault（git 只放 org 真相）。
	cfgPath := *cfg
	if cfgPath == "" {
		cfgPath = filepath.Join(filepath.Dir(abs), "gateway", "config.toml")
	}
	homesPath := *homes
	if homesPath == "" {
		homesPath = filepath.Join(filepath.Dir(abs), "homes")
	}
	dataPath := *data
	if dataPath == "" {
		dataPath = filepath.Join(filepath.Dir(abs), "data")
	}

	o, err := org.Load(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 1
	}
	host := org.Host{VaultRoot: abs, HomesRoot: homesPath, DataDir: dataPath}
	plan, err := renderpkg.Build(o, renderpkg.Options{Host: host, Version: version, Now: time.Now()})
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 1
	}

	existing, readErr := os.ReadFile(cfgPath)
	hasExisting := readErr == nil
	_, oldInputs, _, oldFP := "", "", "", false
	if hasExisting {
		_, oldInputs, _, oldFP = renderpkg.Fingerprint(string(existing))
	}

	fmt.Printf("anc render —— org → gateway config\n")
	fmt.Printf("  vault      %s\n", abs)
	fmt.Printf("  成员       启用 %d / 共 %d\n", len(o.Enabled()), len(o.Members))
	fmt.Printf("  project    %d 个：%s\n", len(plan.Projects), strings.Join(plan.Projects, ", "))
	fmt.Printf("  inputs     %s\n", plan.InputsHash)
	fmt.Printf("  目标       %s（%s）\n", cfgPath, map[bool]string{true: "已存在", false: "不存在"}[hasExisting])
	for _, w := range plan.Warns {
		fmt.Printf("  ⚠️  %s\n", w)
	}

	if *check {
		if !hasExisting {
			fmt.Printf("\n结论：目标配置不存在 —— 需要 `anc render --apply`。\n")
			return 1
		}
		oldHashes := renderpkg.PersonaHashesIn(string(existing))
		var changed, added, removed []string
		for _, p := range plan.Projects {
			oh, ok := oldHashes[p]
			switch {
			case !ok:
				added = append(added, p)
			case oh != plan.PersonaHash[p]:
				changed = append(changed, p)
			}
		}
		for p := range oldHashes {
			if !contains(plan.Projects, p) {
				removed = append(removed, p)
			}
		}
		sort.Strings(removed)
		if len(added)+len(changed)+len(removed) == 0 && oldFP && oldInputs == plan.InputsHash {
			fmt.Printf("\n结论：OK，0 处漂移。\n")
			return 0
		}
		fmt.Printf("\n结论：检测到漂移 ——\n")
		for _, p := range added {
			fmt.Printf("  新增 project   %s\n", p)
		}
		for _, p := range changed {
			fmt.Printf("  persona 有变   %s\n", p)
		}
		for _, p := range removed {
			fmt.Printf("  将删除 project %s\n", p)
		}
		if !oldFP {
			fmt.Printf("  现有配置没有 anc 指纹（手写或他源）——要接管需显式 `--adopt`\n")
		} else if oldInputs != plan.InputsHash {
			fmt.Printf("  inputs 变化    %s → %s\n", short(oldInputs), short(plan.InputsHash))
		}
		fmt.Printf("FIX: 跑 `anc render --apply`%s%s\n",
			map[bool]string{true: " --adopt", false: ""}[!oldFP],
			map[bool]string{true: " --allow-scale", false: ""}[len(added)+len(removed) > 0])
		return 1
	}

	if !*apply {
		fmt.Printf("\n（dry-run，未落盘；要落盘加 --apply）\n")
		return 0
	}

	// 差分校验（两条门）
	if hasExisting && !oldFP && !*adopt {
		fmt.Fprintf(os.Stderr, "\n拒绝覆盖：%s 没有 anc 指纹（手写或他源配置）。确认要接管再加 --adopt。\n", cfgPath)
		return 1
	}
	if hasExisting {
		oldProjects := renderpkg.ProjectsIn(string(existing))
		added, removed := diffSets(plan.Projects, oldProjects)
		if (len(added) > 0 || len(removed) > 0) && !*allowScale {
			fmt.Fprintf(os.Stderr, "\n拒绝落盘：本次会新增 %v / 删除 %v 个 project。确认无误再加 --allow-scale。\n", added, removed)
			return 1
		}
	}

	if hasExisting {
		backup := fmt.Sprintf("%s.bak-%s", cfgPath, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backup, existing, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 备份失败: %v\n", err)
			return 1
		}
		fmt.Printf("\n  备份       %s\n", backup)
	}
	if err := writeAtomic(cfgPath, plan.Text); err != nil {
		fmt.Fprintf(os.Stderr, "错误: 落盘失败: %v\n", err)
		return 1
	}
	fmt.Printf("  写入       %s（%d 个 project）\n", cfgPath, len(plan.Projects))
	fmt.Printf("\n结论：已落盘。gateway 重启与功能探针（kickstart + 90s 窗口）在阶段 B 落地。\n")
	return 0
}

// cmdOrgCheck 只加载 + 校验，不渲染。
func cmdOrgCheck(args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, renderUsage)
		return 2
	}
	abs, err := filepath.Abs(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 2
	}
	o, err := org.Load(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 1
	}
	fmt.Printf("OK —— org 校验通过\n")
	fmt.Printf("  公司    %s（id=%s，平台=%s）\n", o.Company.Name, o.Company.ID, o.Company.Platform)
	fmt.Printf("  角色    %s\n", strings.Join(roleNames(o), ", "))
	fmt.Printf("  成员    启用 %d / 共 %d\n", len(o.Enabled()), len(o.Members))
	fmt.Printf("  路由    %s\n", routingDirs(o))
	return 0
}

func roleNames(o *org.Org) []string {
	out := make([]string, 0, len(o.Roles))
	for k := range o.Roles {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func routingDirs(o *org.Org) string {
	if len(o.Routing) == 0 {
		return "（无数据目录）"
	}
	out := make([]string, 0, len(o.Routing))
	for _, r := range o.Routing {
		out = append(out, r.Dir)
	}
	return strings.Join(out, ", ")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func diffSets(newList, oldList []string) (added, removed []string) {
	for _, p := range newList {
		if !contains(oldList, p) {
			added = append(added, p)
		}
	}
	for _, p := range oldList {
		if !contains(newList, p) {
			removed = append(removed, p)
		}
	}
	return added, removed
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// writeAtomic：同目录 temp + fsync + rename，权限 600（配置里只有 ${ENV} 引用，但仍收紧）。
func writeAtomic(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".anc-render-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

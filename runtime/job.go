package main

// anc job —— ANC 自己会干的事：跑一次 / 看有哪些 / 按调度跑（tick）。
//
// 与 `anc apply` 那条线的区别：`apply` 是「把配置装上去」，`job` 是「**ANC 自己去调外部系统**」。
// 这是它从记账变成干活的第一步，所以三件事格外较真：
//   - 凭据只在 secrets 里，spec 里只有键名（spec 要进 git）；
//   - 幂等键是数据，同一天只做一次（失败不推进，偶发失败不会让今天永远不做）；
//   - 落盘是原子写（中途被杀不留半个文件，不留「看起来跑过了」的假象）。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"anc/internal/apply"
	"anc/internal/job"
)

const jobUsage = `anc job —— ANC 自己去调外部系统（一次执行 / 按调度）

用法：
  anc job run <vault> <spec.json> [--secrets <文件>] [--force] [--json]
  anc job ls  <vault> [--dir <作业目录>] [--json]
  anc job tick <vault> [--dir <作业目录>] [--secrets <文件>] [--json]

选项：
  --dir <目录>     作业目录（默认 <vault>/../jobs）
  --secrets <文件> 凭据文件（默认 ~/.anc/secrets.env）
  --force          忽略幂等键，强制再跑一次（人工重跑用）
  --json           机器读的出口

口径：
  · spec 里只写**凭据的名字**（secret: GITHUB_TOKEN），值住 ~/.anc/secrets.env；
  · 幂等键默认 {{date}}：同一天成功跑过就不再跑（失败不推进，不会把今天永久跳过）；
  · 落盘走原子写；状态落 <vault>/../data/jobs/<name>.json（运行态，不进真相源）。
`

func cmdJob(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, jobUsage)
		return 2
	}
	switch args[0] {
	case "run":
		return cmdJobRun(args[1:])
	case "ls":
		return cmdJobLs(args[1:])
	case "tick":
		return cmdJobTick(args[1:])
	case "install":
		return cmdJobInstall(args[1:])
	case "uninstall":
		return cmdJobUninstall(args[1:])
	}
	fmt.Fprint(os.Stderr, jobUsage)
	return 2
}

// jobDirs 把 vault 相关的三个目录一次算齐：真相源、运行态、作业目录。
func jobDirs(vault, dirFlag string) (vaultAbs, dataDir, jobsDir string, code int) {
	abs, err := filepath.Abs(vault)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return "", "", "", 2
	}
	parent := filepath.Dir(abs)
	dataDir = filepath.Join(parent, "data")
	jobsDir = strings.TrimSpace(dirFlag)
	if jobsDir == "" {
		jobsDir = filepath.Join(parent, "jobs")
	}
	return abs, dataDir, jobsDir, 0
}

func loadSecrets(path string) map[string]string {
	if strings.TrimSpace(path) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return map[string]string{}
		}
		path = filepath.Join(home, ".anc", "secrets.env")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	return apply.ParseSecrets(path, string(b)).Values
}

func cmdJobRun(args []string) int {
	fs := flag.NewFlagSet("job run", flag.ContinueOnError)
	secretsFile := fs.String("secrets", "", "凭据文件（默认 ~/.anc/secrets.env）")
	force := fs.Bool("force", false, "忽略幂等键")
	asJSON := fs.Bool("json", false, "JSON 出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"force": true, "json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 2 {
		fmt.Fprint(os.Stderr, jobUsage)
		return 2
	}
	vault, dataDir, _, code := jobDirs(posArgs[0], "")
	if code != 0 {
		return code
	}
	spec, err := job.Load(posArgs[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 2
	}
	res, err := job.Run(context.Background(), spec, job.Options{
		Vault:   vault,
		DataDir: dataDir,
		Secrets: loadSecrets(*secretsFile),
		Force:   *force,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if *asJSON {
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(b))
	} else {
		printJobResult(res)
	}
	if res.Status == "failed" {
		return 1
	}
	return 0
}

func printJobResult(r job.Result) {
	mark := map[string]string{"ok": "✅", "skipped": "…", "failed": "⚠️"}[r.Status]
	fmt.Printf("%s 作业 %s · %s\n", mark, r.Job, r.Status)
	fmt.Printf("  幂等键   %s\n", r.Key)
	if r.Status == "skipped" {
		fmt.Printf("  原因     %s\n", r.Why)
		return
	}
	fmt.Printf("  HTTP     %d（%d 次尝试）· %s\n", r.Code, r.Attempts, r.Duration.Round(time.Millisecond))
	if r.Status == "ok" {
		fmt.Printf("  落盘     %s（%d 字节，sha256 %s）\n", r.Path, r.Bytes, shortSHA(r.SHA256))
		return
	}
	fmt.Printf("  原因     %s\n", r.Why)
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func cmdJobLs(args []string) int {
	fs := flag.NewFlagSet("job ls", flag.ContinueOnError)
	dirFlag := fs.String("dir", "", "作业目录（默认 <vault>/../jobs）")
	asJSON := fs.Bool("json", false, "JSON 出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 {
		fmt.Fprint(os.Stderr, jobUsage)
		return 2
	}
	_, dataDir, jobsDir, code := jobDirs(posArgs[0], *dirFlag)
	if code != 0 {
		return code
	}
	specs, err := loadJobs(jobsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	type row struct {
		Name    string     `json:"name"`
		Title   string     `json:"title"`
		Every   string     `json:"every"`
		At      string     `json:"at,omitempty"`
		Enabled bool       `json:"enabled"`
		State   *job.State `json:"state,omitempty"`
	}
	var rows []row
	for _, s := range specs {
		st, _ := job.ReadState(dataDir, s.Name)
		rows = append(rows, row{s.Name, s.Title, s.Schedule.Every, s.Schedule.At, s.EnabledOrDefault(), st})
	}
	if *asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	fmt.Printf("anc job —— 作业目录 %s\n\n", jobsDir)
	if len(rows) == 0 {
		fmt.Println("  还没有作业。写一份 spec 放进去（见 testdata/jobs/issue-sweep.json），再 `anc job run <vault> <spec>`。")
		return 0
	}
	for _, r := range rows {
		sched := r.Every
		if r.At != "" {
			sched += " @" + r.At
		}
		last := "从没跑过"
		if r.State != nil && !r.State.LastAt.IsZero() {
			last = r.State.LastAt.Local().Format("01-02 15:04") + " · " + map[bool]string{true: "成功", false: "上次失败"}[r.State.LastOK]
		}
		fmt.Printf("  %-16s %-22s %s\n", r.Name, sched, last)
		if r.Title != "" {
			fmt.Printf("      %s\n", r.Title)
		}
	}
	return 0
}

func loadJobs(dir string) ([]job.Spec, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []job.Spec
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := job.Load(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func cmdJobTick(args []string) int {
	fs := flag.NewFlagSet("job tick", flag.ContinueOnError)
	dirFlag := fs.String("dir", "", "作业目录（默认 <vault>/../jobs）")
	secretsFile := fs.String("secrets", "", "凭据文件（默认 ~/.anc/secrets.env）")
	asJSON := fs.Bool("json", false, "JSON 出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 {
		fmt.Fprint(os.Stderr, jobUsage)
		return 2
	}
	vault, dataDir, jobsDir, code := jobDirs(posArgs[0], *dirFlag)
	if code != 0 {
		return code
	}
	if st, err := os.Stat(jobsDir); err != nil || !st.IsDir() {
		// 「目录不在」与「没有到点」是两件事：混在一起，一次装错路径会永远看起来正常。
		fmt.Fprintf(os.Stderr, "错误：作业目录不存在：%s\n", jobsDir)
		return 1
	}
	specs, err := loadJobs(jobsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if len(specs) == 0 {
		fmt.Fprintf(os.Stderr, "错误：作业目录里没有 spec：%s\n", jobsDir)
		return 1
	}
	now := time.Now()
	sec := loadSecrets(*secretsFile)
	var results []job.Result
	failed := 0
	for _, s := range specs {
		if !s.EnabledOrDefault() {
			continue
		}
		st, _ := job.ReadState(dataDir, s.Name)
		due, next := job.Due(s, st, now)
		if !due {
			continue
		}
		res, err := job.Run(context.Background(), s, job.Options{Vault: vault, DataDir: dataDir, Secrets: sec, Now: now})
		if err != nil {
			res = job.Result{Job: s.Name, Status: "failed", Why: err.Error(), At: now}
		}
		res.NextAt = next
		results = append(results, res)
		if res.Status == "failed" {
			failed++
		}
	}
	if *asJSON {
		b, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(b))
	} else if len(results) == 0 {
		fmt.Println("tick：没有到点的作业")
	} else {
		for _, r := range results {
			printJobResult(r)
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// ---- 装机：launchd ----

const jobLaunchdUsage = `anc job install|uninstall <vault> [--dir <作业目录>] [--every <5m>] [--apply]

把一个「每 N 分钟 tick 一次」的用户级 LaunchAgent 装到 ~/Library/LaunchAgents/。
tick 只做本地判断（到点才真跑），所以 N 小一点不费外部调用。

  --every <5m>   tick 间隔（默认 5m）
  --apply        装完立刻 launchctl bootstrap（不写就只生成 plist，打印命令）
`

func buildJobPlist(label, self, vault, jobsDir, secrets string, interval time.Duration) (string, error) {
	return job.BuildPlist(label, self, vault, jobsDir, secrets, int(interval.Seconds())), nil
}

func runLaunchctl(args ...string) error { return job.RunLaunchctl(args...) }

func uidStr() string { return fmt.Sprintf("%d", os.Getuid()) }

func sanitizeLabel(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "vault"
	}
	return out
}

func cmdJobInstall(args []string) int {
	fs := flag.NewFlagSet("job install", flag.ContinueOnError)
	dirFlag := fs.String("dir", "", "作业目录（默认 <vault>/../jobs）")
	secretsFile := fs.String("secrets", "", "凭据文件（默认 ~/.anc/secrets.env）")
	every := fs.String("every", "5m", "tick 间隔")
	apply := fs.Bool("apply", false, "装完立刻加载")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"apply": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 {
		fmt.Fprint(os.Stderr, jobLaunchdUsage)
		return 2
	}
	vault, _, jobsDir, code := jobDirs(posArgs[0], *dirFlag)
	if code != 0 {
		return code
	}
	// launchd 的 cwd 是 /：**装的时候就要绝对化**，否则 `.devbox/...` 会指到 `/devbox/...`。
	if abs, err := filepath.Abs(vault); err == nil {
		vault = abs
	}
	if abs, err := filepath.Abs(jobsDir); err == nil {
		jobsDir = abs
	}
	if strings.TrimSpace(*secretsFile) != "" {
		if abs, err := filepath.Abs(*secretsFile); err == nil {
			*secretsFile = abs
		}
	}
	interval, err := time.ParseDuration(*every)
	if err != nil || interval < time.Minute {
		fmt.Fprintf(os.Stderr, "错误：--every=%q（至少 1m）\n", *every)
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：取不到自己的路径：%v\n", err)
		return 1
	}
	label := "com.anc.jobs." + sanitizeLabel(filepath.Base(vault))
	plist, err := buildJobPlist(label, self, vault, jobsDir, *secretsFile, interval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	path := filepath.Join(dir, label+".plist")
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	fmt.Printf("✅ 已写 %s\n   label %s · 每 %s tick 一次 · vault %s\n", path, label, interval, vault)
	if !*apply {
		fmt.Printf("\n加载（或加 --apply 自动做）：\n  launchctl bootout gui/$(id -u)/%s 2>/dev/null; launchctl bootstrap gui/$(id -u) %s\n", label, path)
		return 0
	}
	_ = runLaunchctl("bootout", "gui/"+uidStr()+"/"+label)
	if err := runLaunchctl("bootstrap", "gui/"+uidStr(), path); err != nil {
		fmt.Fprintf(os.Stderr, "错误：launchctl bootstrap 失败：%v\n", err)
		return 1
	}
	fmt.Println("✅ 已加载（launchctl bootstrap）")
	return 0
}

func cmdJobUninstall(args []string) int {
	fs := flag.NewFlagSet("job uninstall", flag.ContinueOnError)
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 {
		fmt.Fprint(os.Stderr, jobLaunchdUsage)
		return 2
	}
	vault, _, _, code := jobDirs(posArgs[0], "")
	if code != 0 {
		return code
	}
	label := "com.anc.jobs." + sanitizeLabel(filepath.Base(vault))
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	_ = runLaunchctl("bootout", "gui/"+uidStr()+"/"+label)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	fmt.Printf("✅ 已卸载 %s（plist 已删）\n", label)
	return 0
}

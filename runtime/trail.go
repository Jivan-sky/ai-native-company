package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"anc/internal/gateway"
	"anc/internal/harness"
	"anc/internal/judge"
	renderpkg "anc/internal/render"
	"anc/internal/trail"
)

const trailUsage = `anc trail —— 谁问了什么、agent 干了什么、花了多少（只读聚合，什么都不写）

用法：
  anc trail <vault 目录> [选项]

选项：
  --config <文件>     gateway config（默认 <vault>/../gateway/config.toml）
  --data <目录>       gateway data_dir（默认 <vault>/../data）
  --harness <id>      按哪一家的口径读（默认 claude）；口径在表里，不在代码里
  --harnesses <文件>   接入口径表（整份替换内置那份）
  --claude-home <目录> 直接指定记录根（不改口径，只改去哪找）
	--project <名字>    只看一个 project
	--turns <N>         每个会话最多列几轮（默认 20；0 = 全列）
  --rules <文件>      换一份判据表（默认内置；判据是数据，不是代码）
  --no-judge          只看事实，不要判断层
  --json              给看板 / 归档消费的 JSON（打到 stdout）

它把两份**现成**记录合成一条时间线，源都来自公开文件：
  1. cc-connect 的会话落盘 JSON —— 哪个 bot 的哪个会话槽对应哪个 harness 会话 id（两份记录之间唯一的桥）；
  2. harness 原生记录 —— 逐轮的 token / 工具 / 被拒 / 耗时 / 成本。

三条口径是实测出来的（不是看着像）：
  · 不按行累加。同一条消息会写多行，按行相加会把账翻倍 —— 按 message.id 合并；
  · 子 agent 的 token 算在这个 bot 头上（Agent 工具拉起的旁路会话）；
  · 成本用 harness 自己算的（它连「未知模型没价格」都标了），我们不维护价格表。

读不到原生记录时**明说读不到**，不当成 0 消耗 —— 静默少报和假绿是同一类错误。

退出码：0 账齐全；1 有账读不全 / 没有可归集的记录；2 用法错误
`

func cmdTrail(args []string) int {
	fs := flag.NewFlagSet("trail", flag.ContinueOnError)
	cfg := fs.String("config", "", "gateway config")
	data := fs.String("data", "", "gateway data_dir")
	harnessID := fs.String("harness", harness.DefaultID, "按哪一家的口径读")
	harnessTable := fs.String("harnesses", "", "接入口径表（整份替换内置那份）")
	claudeHome := fs.String("claude-home", "", "直接指定记录根（不改口径）")
	only := fs.String("project", "", "只看一个 project")
	turnLimit := fs.Int("turns", 20, "每个会话最多列几轮")
	rulesFile := fs.String("rules", "", "判据表文件（整份替换内置的）")
	noJudge := fs.Bool("no-judge", false, "只看事实，不要判断层")
	asJSON := fs.Bool("json", false, "输出 JSON")
	vault := fs.String("vault", "", "vault 目录（也可用位置参数）")

	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true, "no-judge": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	root := strings.TrimSpace(*vault)
	if root == "" && len(posArgs) > 0 {
		root = posArgs[0]
	}
	if root == "" {
		fmt.Fprint(os.Stderr, trailUsage)
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 2
	}
	cfgPath := *cfg
	if cfgPath == "" {
		cfgPath = filepath.Join(filepath.Dir(abs), "gateway", "config.toml")
	}
	dataPath := *data
	if dataPath == "" {
		dataPath = filepath.Join(filepath.Dir(abs), "data")
	}
	tb, fam, home, homeWhy, ferr := resolveRecordsRoot(*harnessTable, *harnessID, *claudeHome)
	if ferr != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", ferr)
		return 2
	}
	if rerr := fam.CanRead(); rerr != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", rerr)
		return 1
	}
	// 记录根**按家各算一份**：codex 在它自己的家目录、openclaw 在它自己的 ——
	// 一个组织里不同 bot 用不同 harness 是常态，所以一次 trail 里根不止一个。
	// 兜底那一家（--harness 指的）先按旗标算，其余按各自的环境变量 / 家目录算。
	rootCache := map[string]familyRoot{fam.ID: {ID: fam.ID, Display: fam.Display, Root: home, Why: homeWhy}}
	rootOf := func(f harness.Family) familyRoot {
		if r, ok := rootCache[f.ID]; ok {
			return r
		}
		root, why := f.ResolveRoot("")
		r := familyRoot{ID: f.ID, Display: f.Display, Root: root, Why: why}
		rootCache[f.ID] = r
		return r
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: 读不到 config %s（先跑 anc render）\n", cfgPath)
		return 2
	}
	workDirs := renderpkg.WorkDirs(string(raw))
	projects := make([]string, 0, len(workDirs))
	for p := range workDirs {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	if *only != "" {
		if _, ok := workDirs[*only]; !ok {
			fmt.Fprintf(os.Stderr, "错误: 配置里没有 project %q\n", *only)
			return 2
		}
		projects = []string{*only}
	}

	var all []trail.Session
	incomplete := false
	for _, p := range projects {
		sessions := collectSessions(tb, fam, rootOf, p, workDirs[p], dataPath)
		for _, s := range sessions {
			if len(s.Problems) > 0 {
				incomplete = true
			}
		}
		all = append(all, sessions...)
	}
	if len(all) == 0 {
		incomplete = true
	}

	// 判断层：判据是数据（内置一份，--rules 可整份换），引擎不认识任何一条具体判据。
	// **它只出结论，不拦任何事、不改退出码** —— 判错了的代价应该只是多一行话。
	rules := judge.Builtin()
	rulesWhy := fmt.Sprintf("内置 %d 条", len(rules))
	if *rulesFile != "" {
		loaded, err := judge.Load(*rulesFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			return 2
		}
		rules, rulesWhy = loaded, *rulesFile
	}
	usedRoots := make([]familyRoot, 0, len(rootCache))
	for _, r := range rootCache {
		usedRoots = append(usedRoots, r)
	}
	sort.Slice(usedRoots, func(i, j int) bool { return usedRoots[i].ID < usedRoots[j].ID })

	findings := make([][]judge.Finding, len(all))
	if !*noJudge {
		for i, s := range all {
			findings[i] = judge.Judge(s, rules)
		}
	}

	if *asJSON {
		items := make([]map[string]any, 0, len(all))
		for i, s := range all {
			item := map[string]any{"session": s}
			if !*noJudge {
				item["findings"] = findings[i]
			}
			items = append(items, item)
		}
		out := map[string]any{
			"schema":       trail.Schema,
			"judge_schema": judge.Schema,
			"vault":        abs,
			"data":         dataPath,
			// 兜底那一家（--harness 指的）的根保留原名（消费方可能已经在读）。
			"claude_home": home,
			"claude_why":  homeWhy,
			// 这一次真正用到的每一家 + 各自的根 —— 混编的组织里不止一家。
			"records":  rootsJSON(usedRoots),
			"rules":    rulesWhy,
			"sessions": items,
			"totals":   totals(all),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	} else {
		printTrail(abs, dataPath, usedRoots, rulesWhy, all, findings, *turnLimit, !*noJudge)
	}
	if incomplete {
		return 1
	}
	return 0
}

// resolveRecordsRoot 按接入口径表定出「兜底哪一家 + 它的记录根」，并把整张表一并交出来
// （桥里写的 agent_type 要拿这张表翻成家）。
//
// 表从哪来（内置 / --harnesses）和兜底用哪一家（--harness）都在这里收口 ——
// trail 与 audit 共用同一份口径，所以「换一家」是一处的事，不是两处。
func resolveRecordsRoot(tableFile, familyID, flagVal string) (harness.Table, harness.Family, string, string, error) {
	tb, err := harness.Load(tableFile)
	if err != nil {
		return harness.Table{}, harness.Family{}, "", "", err
	}
	id := strings.TrimSpace(familyID)
	if id == "" {
		id = harness.DefaultID
	}
	fam, ok := tb.Lookup(id)
	if !ok {
		return tb, harness.Family{}, "", "", fmt.Errorf("口径表里没有 %q 这家（表里有：%s）", id, strings.Join(tb.IDs(), " / "))
	}
	root, why := fam.ResolveRoot(flagVal)
	if why != "" {
		why = why + "；口径 " + fam.Display
	}
	return tb, fam, root, why, nil
}

// familyRoot 是「一家 + 它的记录根」——一次 trail 里可能有多家（桥里写谁就是谁）。
type familyRoot struct {
	ID      string
	Display string
	Root    string
	Why     string
}

// rootsJSON 是 / --json 里那份「这一次用到了哪些家、各自的根在哪」。
func rootsJSON(rs []familyRoot) []map[string]any {
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, map[string]any{"harness": r.ID, "display": r.Display, "root": r.Root, "why": r.Why})
	}
	return out
}

// pickFamily 决定**这一段会话**按哪一家的口径读 —— 认的是桥自己写的 agent_type，
// 不是命令行那个旗标（一个组织里不同 bot 用不同 harness，旗标一次只能给一家）。
//
// 三种情形，都不猜：
//   - 桥里写了、表里也认 → 用那一家（记录根也换成那一家自己的）；
//   - 桥里没写（老槽、从没起来过）→ 退回旗标那一家 —— 旗标本来就是干这个的；
//   - 桥里写了、表里不认 → 返回 why，调用方**明说读不到**。不许按别家口径硬读：
//     按错的口径读出来的数字比读不到更糟（读不到会被追，读错了不会）。
func pickFamily(tb harness.Table, fallback harness.Family, agentType string) (harness.Family, string) {
	want := strings.TrimSpace(agentType)
	if want == "" {
		return fallback, ""
	}
	if f, ok := tb.ForAgentType(want); ok {
		return f, ""
	}
	return harness.Family{}, fmt.Sprintf(
		"桥里说这一段是 %q，接入口径表里没有对得上的那家（表里认的类型：%s）—— 不按别家口径硬读",
		want, strings.Join(tb.AgentTypes(), " / "))
}

// collectSessions 把一个 project 的记录归集齐：cc-connect 说「有哪些 harness 会话」，
// 原生记录给出「每一段干了什么」。
func collectSessions(tb harness.Table, fallback harness.Family, rootOf func(harness.Family) familyRoot, project, workDir, dataPath string) []trail.Session {
	files, _ := filepath.Glob(filepath.Join(gateway.CCConnect.SessionsDir(dataPath), project+"_*.json"))
	sort.Strings(files)
	var out []trail.Session
	for _, f := range files {
		bridges, err := trail.Bridges(project, f)
		if err != nil {
			out = append(out, trail.Session{
				Schema: trail.Schema, Project: project,
				Problems: []string{err.Error()},
			})
			continue
		}
		for _, b := range bridges {
			fam, why := pickFamily(tb, fallback, b.AgentType)
			if why != "" {
				// 桥说的那家我们不认识：ID 照搬（人要看得出是哪一段），账明说读不到。
				id := b.SessionID
				if id == "" && len(b.PastIDs) > 0 {
					id = b.PastIDs[0]
				}
				out = append(out, trail.Session{
					Schema: trail.Schema, Project: project, Slot: b.Slot, ID: id,
					AgentType: b.AgentType, Turns: []trail.Turn{}, Problems: []string{why},
				})
				continue
			}
			root := rootOf(fam).Root
			if b.SessionID != "" {
				s := trail.ReadSessionIn(fam, root, project, b.Slot, false, workDir, b.SessionID, b.AgentType)
				out = append(out, s)
			}
			for _, past := range b.PastIDs {
				s := trail.ReadSessionIn(fam, root, project, b.Slot, true, workDir, past, b.AgentType)
				out = append(out, s)
			}
		}
	}
	return out
}

func totals(all []trail.Session) trail.Usage {
	var u trail.Usage
	for _, s := range all {
		u = u.Add(s.Usage)
	}
	return u
}

func printTrail(vault, data string, roots []familyRoot, rulesWhy string, all []trail.Session, findings [][]judge.Finding, turnLimit int, withJudge bool) {
	fmt.Println("anc trail —— 只读聚合：谁问了什么、agent 干了什么、花了多少")
	fmt.Printf("  vault    %s\n", vault)
	fmt.Printf("  data     %s\n", data)
	// 用到的每一家都列出来（混编的组织里不止一家）—— 一行一个根，
	// 因为「哪段会话是从哪读的」正是这份账可不可信的前提。
	for i, r := range roots {
		label := "  记录    "
		if i > 0 {
			label = "          "
		}
		if r.Root == "" {
			fmt.Printf("%s⚠️  %s（口径 %s）\n", label, r.Why, r.Display)
			continue
		}
		fmt.Printf("%s%s（口径 %s）\n", label, r.Root, r.Display)
	}
	if withJudge {
		fmt.Printf("  判据     %s —— 判据是数据，换一份不用重编译（--rules）\n", rulesWhy)
	}
	fmt.Println()

	if len(all) == 0 {
		fmt.Println("  没有可归集的记录：这个配置下的 bot 从来没起过 agent 会话。")
		fmt.Println("  「没记录」不等于「没花销」—— 要看它起没起来，跑 anc probe。")
		return
	}

	cur := ""
	var sum trail.Usage
	var cost float64
	unread := 0
	for i, s := range all {
		if s.Project != cur {
			cur = s.Project
			fmt.Printf("%s\n", cur)
		}
		if !s.Found {
			unread++
			printSession(s, findings[i], turnLimit, withJudge)
			continue
		}
		sum = sum.Add(s.Usage)
		cost += s.CostUSD
		printSession(s, findings[i], turnLimit, withJudge)
	}
	// 有读不到的会话时，合计必须**说明它只算读到的那些** —— 否则这个 0
	// 和「真的一分没花」长得一模一样，那正是我们一直在防的假绿。
	note := ""
	if unread > 0 {
		note = fmt.Sprintf("（其中 %d 段读不到，没算进来）", unread)
	}
	fmt.Printf("合计 %d 段会话%s · 输入 %d · 输出 %d · 缓存读 %d · 成本 $%.6f\n",
		len(all), note, sum.In, sum.Out, sum.CacheRead, cost)
}

func printSession(s trail.Session, finds []judge.Finding, turnLimit int, withJudge bool) {
	tag := s.AgentType
	if tag == "" {
		tag = "未知 harness"
	}
	if s.Historic {
		tag += "，历史会话"
	}
	if !s.Found {
		fmt.Printf("  会话 %s（%s）⚠️ 归集不上\n", shortID(s.ID), tag)
		for _, p := range s.Problems {
			fmt.Printf("      ⚠️  %s\n", p)
		}
		printFindings(finds, withJudge)
		fmt.Println()
		return
	}
	fmt.Printf("  会话 %s（%s，%s → %s，%d 轮）\n",
		shortID(s.ID), tag,
		s.Started.Local().Format("01-02 15:04:05"),
		s.Ended.Local().Format("01-02 15:04:05"),
		len(s.Turns))
	fmt.Printf("      账    输入 %d · 输出 %d · 缓存读 %d · 缓存写 %d\n",
		s.Usage.In, s.Usage.Out, s.Usage.CacheRead, s.Usage.CacheWrite)
	if s.CostUSD > 0 || s.UnknownCost {
		warn := ""
		if s.UnknownCost {
			warn = "（有模型没价格，harness 自己标的）"
		}
		fmt.Printf("      成本  $%.6f%s\n", s.CostUSD, warn)
	}
	if s.TotalDuration > 0 {
		fmt.Printf("      耗时  总 %s · API %s · 工具 %s\n",
			shortDur(s.TotalDuration), shortDur(s.APIDuration), shortDur(s.ToolDuration))
	}
	if len(s.Tools) > 0 {
		fmt.Printf("      工具  %s\n", fmtCounts(totalCount(s.Tools), s.Tools, 6))
	}
	if len(s.Denials) > 0 {
		fmt.Printf("      被拒  %s —— 权限规则挡下的动作，不是故障（要看挡了啥，去翻记录）\n",
			fmtCounts(totalCount(s.Denials), s.Denials, 4))
	}
	for _, sub := range s.Subagents {
		name := sub.Agent
		if name == "" {
			name = "子任务"
		}
		where := "归属不明"
		if sub.Turn > 0 {
			where = fmt.Sprintf("第 %d 轮拉起", sub.Turn)
		}
		fmt.Printf("      子任务 %s（%s，%s）%d 轮 · 输入 %d · 输出 %d —— 算在本会话账上\n",
			name, sub.Desc, where, sub.Turns, sub.Usage.In, sub.Usage.Out)
	}
	if s.GitBranch != "" {
		fmt.Printf("      分支  %s\n", s.GitBranch)
	}
	for _, p := range s.Problems {
		fmt.Printf("      ⚠️  %s\n", p)
	}

	if len(s.Turns) > 0 {
		list := s.Turns
		more := 0
		if turnLimit > 0 && len(list) > turnLimit {
			more = len(list) - turnLimit
			list = list[len(list)-turnLimit:]
		}
		fmt.Printf("      ── 逐轮（%d 轮）──\n", len(s.Turns))
		if more > 0 {
			fmt.Printf("      …（前面 %d 轮略）\n", more)
		}
		for _, t := range list {
			var bits []string
			if t.Duration > 0 {
				bits = append(bits, shortDur(t.Duration))
			}
			bits = append(bits, fmt.Sprintf("in %d out %d", t.Usage.In, t.Usage.Out))
			if len(t.Tools) > 0 {
				bits = append(bits, fmtCounts(totalCount(t.Tools), t.Tools, 4))
			}
			if t.Denied > 0 {
				bits = append(bits, fmt.Sprintf("被拒 %d", t.Denied))
			}
			if n := failedIn(t.Failures); n > 0 {
				bits = append(bits, fmt.Sprintf("失败 %d", n))
			}
			fmt.Printf("      %s  %s\n", t.At.Local().Format("15:04:05"), t.Prompt)
			fmt.Printf("                %s\n", strings.Join(bits, " · "))
			for _, f := range t.Failures {
				fmt.Printf("                ↳ %s：%s —— %s\n", failKind(f.Kind), orDash(f.Tool), f.Why)
			}
		}
	}
	printFindings(finds, withJudge)
	fmt.Println()
}

// printFindings 打判断层。**它和事实分开摆**：事实是记录里抄下来的，判断是判据判的，
// 两者混在一起，读的人就分不清哪句能当证据用。
func printFindings(finds []judge.Finding, withJudge bool) {
	if !withJudge {
		return
	}
	if len(finds) == 0 {
		fmt.Println("      ── 判断 ──")
		fmt.Println("      没有判据命中 —— 这是「没看出问题」，不是「没问题」；")
		fmt.Println("      判据表只覆盖了手上这几条，扩大覆盖靠改那份数据（--rules），不靠改代码。")
		return
	}
	fmt.Println("      ── 判断（判据判的，不是记录里抄的）──")
	for _, f := range finds {
		fmt.Printf("      [%s] %s\n", f.Level, f.Title)
		fmt.Printf("            %s\n", f.Say)
		fmt.Printf("            据于 %s · 规则 %s\n", f.Where, f.Rule)
		for _, e := range f.Evidence {
			fmt.Printf("            证据 %s\n", e)
		}
	}
}

func failedIn(list []trail.Failure) int {
	n := 0
	for _, f := range list {
		if f.Kind != "denied" {
			n++
		}
	}
	return n
}

func failKind(kind string) string {
	if kind == "denied" {
		return "被权限挡下"
	}
	return "执行失败"
}

func orDash(s string) string {
	if s == "" {
		return "工具名未知"
	}
	return s
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// shortDur 把时长写成「人一眼能比大小」的样子（秒级以下给毫秒）。
func shortDur(d time.Duration) string {
	if d >= time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

func totalCount(list []trail.ToolCount) int {
	n := 0
	for _, c := range list {
		n += c.Count
	}
	return n
}

func fmtCounts(total int, list []trail.ToolCount, max int) string {
	parts := make([]string, 0, len(list))
	for i, c := range list {
		if i >= max {
			parts = append(parts, fmt.Sprintf("…等 %d 种", len(list)))
			break
		}
		parts = append(parts, fmt.Sprintf("%s ×%d", c.Name, c.Count))
	}
	return fmt.Sprintf("%d 次：%s", total, strings.Join(parts, "、"))
}

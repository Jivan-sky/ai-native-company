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

	"anc/internal/audit"
	"anc/internal/org"
	renderpkg "anc/internal/render"
)

// auditSchema 是 `anc audit` 的 JSON 出口版本（看板吃同一个形状）。
const auditSchema = "anc.audit/v1"

const auditUsage = `anc audit —— 行使的流水：谁在什么时候、对谁、行使了什么（含被拒绝的）

用法：
  anc audit log <vault> [选项]        读流水（按当前域表算「向谁 / 跨不跨域」）
  anc audit add <vault> [选项]        显式补记一条（归集不到的那一类）
  anc audit collect <vault> [选项]    从 harness 原生记录归集（默认 dry-run）
  anc audit tools                     打印生效的工具表

log 选项：
  --actor <名字>      只看一个人
  --result <档>       只看 ok | denied | failed
  --action <类别>     只看 read | write | invoke
  --cross             只看跨域的
  --limit <N>         列最近几条（默认 50，上限 1000）
  --json              机器读的出口

add 选项（--who → 用 --actor）：
  --actor <名字>      谁行使的（必填）
  --action <类别>     read | write | invoke（必填）
  --result <档>       ok | denied | failed（必填）
  --object <客体>     对谁（路径 / 标识）；空 = 抽不出，页面上按「未归属」显示
  --why <原话>        结果的原话（例如那条拒绝理由）
  --at <RFC3339>      何时（默认现在）
  --on-behalf-of <人> 署名：这个 bot 代理的是谁（SPEC §6 凭据与授权分离）
  --id <标识>         唯一 id（默认按时间生成；同一条别写两次）

collect 选项：
  --config <文件>     gateway config（默认 <vault>/../gateway/config.toml）
  --claude-home <目录> harness 记录根（默认 $CLAUDE_CONFIG_DIR，其次 <用户目录>/.claude）
  --since <RFC3339>   只要这个时刻之后的
  --write             真落盘（不加就是 dry-run，只打印会写几条）
  --json              机器读的出口

四条口径：
  · **只记不拦**。审计是「先记下来」，不是「先拦住」—— 拦住是授权层的事。
    它判错了，代价应该只是一行话，不是「一件本该发生的事没发生」。
  · **离线**。ANC 不在行使路径上，所以流水靠两条腿凑：事后归集 + 显式补记。
    由此推出一件读的人必须知道的事：**「本可以行使但没行使」永远不可能自动归集出来** ——
    它没发生，就没有记录可抽。那一类只能在 anc audit add 里补。
  · **不存派生**。流水里存的是当时的事实（目标客体是原样的路径）；
    「这在哪个域」「算不算跨域」是按**当前**域表算的 —— 域表改了，解读跟着改，证据不变。
  · **记不到的显式报**：读不懂的行、抽不出目标的、配不上结果的，一律列出来，不静默丢。

退出码：0 正常；1 读不动 / 有记不到的；2 用法错误
`

func cmdAudit(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, auditUsage)
		return 2
	}
	switch args[0] {
	case "log":
		return cmdAuditLog(args[1:])
	case "add":
		return cmdAuditAdd(args[1:])
	case "collect":
		return cmdAuditCollect(args[1:])
	case "tools":
		return cmdAuditTools(args[1:])
	default:
		fmt.Fprint(os.Stderr, auditUsage)
		return 2
	}
}

// loadScope 读真相源建一张「客体 → 域」的映射。org 红档时**不拦**：
// 归集与读流水都不该被真相源的格式问题挡住 —— 那件事由 `anc org check` 报。
// 拿不到域表时一切落「未归属」（Known=false），并如实说明原因。
func loadScope(abs string) (*audit.Scope, *org.Org, string) {
	o, err := org.Load(abs)
	if err != nil {
		return audit.NewScope(abs, nil), nil, "org 真相源读不动（" + firstLine(err.Error()) + "）—— 域归属一律落「未归属」"
	}
	return audit.NewScope(abs, o), o, ""
}

// ---------- log ----------

func cmdAuditLog(args []string) int {
	fs := flag.NewFlagSet("audit log", flag.ContinueOnError)
	onlyActor := fs.String("actor", "", "只看一个人")
	onlyResult := fs.String("result", "", "只看 ok|denied|failed")
	onlyAction := fs.String("action", "", "只看 read|write|invoke")
	onlyCross := fs.Bool("cross", false, "只看跨域的")
	limit := fs.Int("limit", 50, "列最近几条")
	asJSON := fs.Bool("json", false, "JSON 出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"cross": true, "json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) == 0 {
		fmt.Fprint(os.Stderr, auditUsage)
		return 2
	}
	abs, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if *limit < 1 {
		fmt.Fprintln(os.Stderr, "错误：--limit 要是个正整数")
		return 2
	}
	if *limit > 1000 {
		*limit = 1000
	}

	doc, err := audit.Load(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	sc, _, warn := loadScope(abs)
	views := audit.Views(doc.Records, sc)

	// 筛 → 倒序（最新的在上）→ 截断。
	kept := make([]audit.EntryView, 0, len(views))
	for _, v := range views {
		if a := strings.TrimSpace(*onlyActor); a != "" && v.Actor != a {
			continue
		}
		if r := strings.TrimSpace(*onlyResult); r != "" && v.Band != strings.ToLower(r) {
			continue
		}
		if a := strings.TrimSpace(*onlyAction); a != "" && v.Action != a {
			continue
		}
		if *onlyCross && !(v.Known && v.Cross) {
			continue
		}
		kept = append(kept, v)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		ti, _ := kept[i].Time()
		tj, _ := kept[j].Time()
		return ti.After(tj)
	})
	shown := kept
	if len(shown) > *limit {
		shown = shown[:*limit]
	}

	if *asJSON {
		printAuditJSON(abs, doc, views, kept, shown, warn)
		if len(doc.Bad) > 0 {
			return 1
		}
		return 0
	}
	printAuditLog(abs, doc, views, len(kept), shown, warn)
	if len(doc.Bad) > 0 {
		return 1
	}
	return 0
}

func printAuditLog(abs string, doc audit.Doc, all []audit.EntryView, matched int, shown []audit.EntryView, warn string) {
	fmt.Printf("anc audit —— 行使的流水（谁 / 何时 / 对谁 / 类别 / 结果）\n")
	fmt.Printf("  vault   %s\n", abs)
	bands := audit.Tally(doc.Records)
	scs := audit.TallyScopes(all)
	fmt.Printf("  流水    %d 条（四档 ok %d / denied %d / failed %d / 认不出 %d）\n",
		doc.Len(), bands.OK, bands.Denied, bands.Failed, bands.Unknown)
	fmt.Printf("  落点    域内 %d · vault 内非域 %d · 域外 %d · 目标抽不出 %d  →  其中跨域 %d\n",
		scs.Domain, scs.Vault, scs.Outside, scs.Unknown, scs.Cross)
	if warn != "" {
		fmt.Printf("  ⚠️      %s\n", warn)
	}

	fmt.Printf("\n行使（筛出 %d 条，这里铺最近 %d 条）\n", matched, len(shown))
	if len(shown) == 0 {
		fmt.Printf("  没有符合条件的记录。\n")
		fmt.Printf("  空不是错 —— 「还没有行使」是个真实状态；接了 harness 之后用 `anc audit collect` 归集。\n")
	} else {
		fmt.Printf("  %-22s %-14s %-8s %-14s %-7s %s\n", "何时", "谁", "类别", "对谁", "结果", "目标")
		for _, v := range shown {
			actee := zoneLabel(v)
			obj := v.Object
			if strings.TrimSpace(obj) == "" {
				obj = "（目标抽不出）"
			}
			fmt.Printf("  %-22s %-14s %-8s %-12s %-7s %s\n",
				shortTime(v.At), padRight(v.Actor, 14), padRight(v.Action, 8),
				padRight(actee, 14), padRight(v.Band, 7), obj)
			if strings.TrimSpace(v.Why) != "" {
				fmt.Printf("  %-22s ↳ %s\n", "", v.Why)
			}
		}
	}
	if matched > len(shown) {
		fmt.Printf("\n  还有 %d 条更早的 —— 加 --limit，或按 --actor / --result / --action / --cross 筛。\n", matched-len(shown))
	}
	if len(doc.Bad) > 0 {
		fmt.Printf("\n  ⚠️  有 %d 行读不懂（少一行就可能让一次行使看起来没发生）：%s\n",
			len(doc.Bad), strings.Join(doc.Bad, "、"))
	}
	fmt.Printf("\n  这份流水只记「发生了什么」。谁**想**行使但没行使，只有人补记的那一类才有 ——\n")
	fmt.Printf("  没发生的事没有记录可归集，别把这份流水当成全部行使。\n")
}

func printAuditJSON(abs string, doc audit.Doc, all []audit.EntryView, matched, shown []audit.EntryView, warn string) {
	out := map[string]any{
		"schema":  auditSchema,
		"vault":   abs,
		"bands":   audit.Tally(doc.Records),
		"total":   doc.Len(),
		"matched": len(matched),
		"records": shown,
		"bad":     orEmpty(doc.Bad),
	}
	if warn != "" {
		out["warning"] = warn
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
	}
}

// ---------- add ----------

func cmdAuditAdd(args []string) int {
	fs := flag.NewFlagSet("audit add", flag.ContinueOnError)
	actor := fs.String("actor", "", "谁行使的")
	action := fs.String("action", "", "read|write|invoke")
	result := fs.String("result", "", "ok|denied|failed")
	object := fs.String("object", "", "对谁")
	why := fs.String("why", "", "结果的原话")
	at := fs.String("at", "", "何时（RFC3339，默认现在）")
	behalf := fs.String("on-behalf-of", "", "署名")
	id := fs.String("id", "", "唯一 id")
	detail := fs.String("detail", "", "展开")
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) == 0 {
		fmt.Fprint(os.Stderr, auditUsage)
		return 2
	}
	abs, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	// `--at` 是**这条记录的字段**，不是查询参数：给错了就是「这条记录记不下」——
	// 退出码 1，与 envelope「缺 who = 拒收 = 1」同一条口径。
	// （对比：`--limit` / `--since` 是查询语法，敲错了算用法错，退出码 2。）
	when := time.Now()
	if v := strings.TrimSpace(*at); v != "" {
		t, perr := time.Parse(time.RFC3339, v)
		if perr != nil {
			fmt.Fprintln(os.Stderr, "错误：--at 要是 RFC3339（例如 2026-10-08T12:00:00+08:00）—— 这条记不下")
			return 1
		}
		when = t
	}
	rid := strings.TrimSpace(*id)
	if rid == "" {
		rid = "manual:" + when.Format(time.RFC3339Nano)
	}
	r := audit.Record{
		ID:         rid,
		At:         when.Format(time.RFC3339),
		Actor:      strings.TrimSpace(*actor),
		Action:     strings.TrimSpace(*action),
		Object:     strings.TrimSpace(*object),
		Result:     strings.TrimSpace(*result),
		Why:        strings.TrimSpace(*why),
		OnBehalfOf: strings.TrimSpace(*behalf),
		Detail:     strings.TrimSpace(*detail),
		Source:     "manual",
	}
	path, err := audit.Append(abs, r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	fmt.Printf("记了一条：%s\n", path)
	fmt.Printf("  %s  %s  %s → %s  %s\n", r.At, r.Actor, r.Action, dashOf(r.Object), r.Result)
	return 0
}

// ---------- collect ----------

func cmdAuditCollect(args []string) int {
	fs := flag.NewFlagSet("audit collect", flag.ContinueOnError)
	cfg := fs.String("config", "", "gateway config")
	claudeHome := fs.String("claude-home", "", "harness 记录根")
	since := fs.String("since", "", "只要这个时刻之后的")
	write := fs.Bool("write", false, "真落盘")
	asJSON := fs.Bool("json", false, "JSON 出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"write": true, "json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) == 0 {
		fmt.Fprint(os.Stderr, auditUsage)
		return 2
	}
	abs, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	cfgPath := *cfg
	if cfgPath == "" {
		cfgPath = filepath.Join(filepath.Dir(abs), "gateway", "config.toml")
	}
	home, homeWhy := resolveClaudeHome(*claudeHome)

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：读不到 config %s（先跑 anc render）\n", cfgPath)
		return 1
	}
	workDirs := renderpkg.WorkDirs(string(raw))
	if len(workDirs) == 0 {
		fmt.Fprintf(os.Stderr, "错误：config 里没有 project（%s）\n", cfgPath)
		return 1
	}

	// actor 名：project 名是 `<公司 id>-<成员名>`，能解出成员就用成员名。
	// org 读不动也不拦 —— 归集的是运行态事实，不该被真相源的格式问题挡住。
	var actorOf func(string) string
	o, oerr := org.Load(abs)
	if oerr == nil {
		actorOf = func(project string) string {
			if name := strings.TrimPrefix(project, o.Company.ID+"-"); name != project {
				if _, ok := o.Member(name); ok {
					return name
				}
			}
			return project
		}
	}

	known := map[string]bool{}
	if doc, err := audit.Load(abs); err == nil {
		known = doc.IDs()
	}

	var sinceT time.Time
	if v := strings.TrimSpace(*since); v != "" {
		t, perr := time.Parse(time.RFC3339, v)
		if perr != nil {
			fmt.Fprintln(os.Stderr, "错误：--since 要是 RFC3339")
			return 2
		}
		sinceT = t
	}

	res := audit.Collect(audit.CollectOptions{
		ClaudeHome: home,
		Projects:   workDirs,
		ActorOf:    actorOf,
		Known:      known,
		Since:      sinceT,
	})

	sc, _, _ := loadScope(abs)
	views := audit.Views(res.Records, sc)

	written := 0
	var writeErr error
	if *write {
		for _, r := range res.Records {
			if _, werr := audit.Append(abs, r); werr != nil {
				writeErr = werr
				break
			}
			written++
		}
	}

	if *asJSON {
		out := map[string]any{
			"schema":   auditSchema,
			"vault":    abs,
			"config":   cfgPath,
			"home":     home,
			"home_why": homeWhy,
			"shards":   res.Shards,
			"calls":    res.Calls,
			"new":      len(res.Records),
			"written":  written,
			"records":  views,
			"problems": orEmpty(res.Problems),
		}
		if writeErr != nil {
			out["error"] = writeErr.Error()
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		}
	} else {
		printAuditCollect(abs, cfgPath, home, homeWhy, res, views, written, writeErr, *write)
	}
	if writeErr != nil || len(res.Problems) > 0 {
		return 1
	}
	return 0
}

func printAuditCollect(abs, cfgPath, home, homeWhy string, res audit.CollectResult,
	views []audit.EntryView, written int, writeErr error, write bool) {

	fmt.Printf("anc audit collect —— 从 harness 原生记录归集行使（%s）\n",
		map[bool]string{true: "已落盘", false: "dry-run，没写任何东西"}[write])
	fmt.Printf("  vault     %s\n", abs)
	fmt.Printf("  config    %s\n", cfgPath)
	fmt.Printf("  harness   %s（%s）\n", home, homeWhy)
	fmt.Printf("  扫到      %d 个分片 / %d 次行使\n", res.Shards, res.Calls)
	fmt.Printf("  新记录    %d 条", len(res.Records))
	if write {
		fmt.Printf("（已写 %d 条）", written)
	} else {
		fmt.Printf("（`--write` 才落盘）")
	}
	fmt.Printf("\n")

	if len(views) > 0 {
		bands := audit.Bands{}
		for _, v := range views {
			switch v.Band {
			case audit.ResultOK:
				bands.OK++
			case audit.ResultDenied:
				bands.Denied++
			case audit.ResultFailed:
				bands.Failed++
			default:
				bands.Unknown++
			}
		}
		scs := audit.TallyScopes(views)
		fmt.Printf("  四档      ok %d / denied %d / failed %d / 认不出 %d\n",
			bands.OK, bands.Denied, bands.Failed, bands.Unknown)
		fmt.Printf("  落点      域内 %d · vault 内非域 %d · 域外 %d · 目标抽不出 %d  →  其中跨域 %d\n",
			scs.Domain, scs.Vault, scs.Outside, scs.Unknown, scs.Cross)
		fmt.Printf("\n  抽样（最多 8 条）\n")
		n := len(views)
		if n > 8 {
			n = 8
		}
		for _, v := range views[:n] {
			fmt.Printf("    %-20s %-8s %-14s %-7s %s\n",
				shortTime(v.At), padRight(v.Action, 8), padRight(zoneLabel(v), 14), padRight(v.Band, 7),
				dashOf(v.Object))
		}
	}
	if len(res.Problems) > 0 {
		fmt.Printf("\n  ⚠️  记不到的（%d 条，必须说 —— 静默少记和假绿是同一类错误）\n", len(res.Problems))
		for _, p := range res.Problems {
			fmt.Printf("    · %s\n", p)
		}
	}
	if writeErr != nil {
		fmt.Printf("\n  ⚠️  落盘中断：%s（已经写进去的留着，重跑不会重复 —— id 去重）\n", writeErr)
	}
	if len(res.Records) == 0 && len(res.Problems) == 0 {
		fmt.Printf("\n  没有新行使可记。要么这段时间真的没有，要么记录目录不在（那件事由 `anc trail` 查）。\n")
	}
}

// ---------- tools ----------

func cmdAuditTools(args []string) int {
	fs := flag.NewFlagSet("audit tools", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "JSON 出口")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rules := audit.BuiltinTools()
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"schema": auditSchema, "tools": rules}); err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		}
		return 0
	}
	fmt.Printf("anc audit —— 生效的工具表（出厂 %d 条；这是**数据**，加工具不用改代码）\n\n", len(rules))
	fmt.Printf("  %-16s %-8s %s\n", "工具", "类别", "目标键（input 里哪个键是「对谁」）")
	for _, r := range rules {
		target := strings.Join(r.Target, " | ")
		if target == "" {
			target = "（没有 —— 这类行使的目标抽不出来，会进 Problems）"
		}
		fmt.Printf("  %-16s %-8s %s\n", r.Tool, r.Action, target)
	}
	fmt.Printf("\n  不在表里的工具**不会静默丢掉**：归集时计入 Problems 报出来。\n")
	return 0
}

// zoneLabel 把「对谁」那一栏说清楚。四类各自是什么，见 audit 包的 Zone 常量 ——
// 关键是**「判不出」不许显示成「不跨域」**：那是最坏的一种假绿。
func zoneLabel(v audit.EntryView) string {
	switch v.Zone {
	case audit.ZoneDomain:
		switch {
		case !v.Known:
			return v.Actee + "（跨域未判）"
		case v.Cross:
			return v.Actee + " ⚠跨域"
		default:
			return v.Actee
		}
	case audit.ZoneVault:
		return "vault 内·非域"
	case audit.ZoneOutside:
		return "⚠ 域外"
	default:
		return "判不出"
	}
}

// dashOf 把空值显示成一个说法，而不是一串空白（看板同款纪律：缺什么就说缺什么）。
func dashOf(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// shortTime 只留「日期 时:分:秒」，把时区与毫秒切掉 —— 一排时间戳里那些是噪音。
func shortTime(iso string) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(iso))
	if err != nil {
		return iso
	}
	return t.Format("2006-01-02 15:04:05")
}

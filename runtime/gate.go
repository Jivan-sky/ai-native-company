package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"anc/internal/judge"
	"anc/internal/org"
	"anc/internal/timeline"
)

// gateSchema 是 `anc gate` 的 JSON 出口版本。
const gateSchema = "anc.gate/v1"

const gateUsage = `anc gate —— 交付链路的门：五道门的证据落痕了没有（只出结论，不设门禁）

用法：
  anc gate <vault> [--rules <文件>] [--show-rules] [--json]

选项：
  --rules <文件>   换一份判据表（整份替换内置的；判据是数据，不是代码）
  --show-rules    只打印生效判据表，不跑判据
  --json          机器读的出口

口径（三条）：
  · 事实只有两个来源，都是真相源：org（人 / 岗 / 域 / 项目 / 授权）+ timeline（留痕）。
    判据不自己造事实 —— 它只决定「这几个数放在一起算不算一件事」。
  · 一道门就是一个 case，命名约定 gate:<slug>（例：gate:proof）。**门名与判据都是数据**：
    改名单、加一道门、换一套判法，都换那份数据（--rules），不用改代码。
  · **这里没有门禁**：只出结论，判据本身不改退出码。它判错了，代价应该只是一行话，
    不是「一件本该发生的事没发生」。（读不到 vault 这类真错误仍然非 0 —— 那不是判据的意见。）
`

func cmdGate(args []string) int {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	rulesFile := fs.String("rules", "", "判据表文件（整份替换内置的）")
	showRules := fs.Bool("show-rules", false, "只打印生效判据表")
	asJSON := fs.Bool("json", false, "JSON 出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"show-rules": true, "json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) == 0 {
		fmt.Fprint(os.Stderr, gateUsage)
		return 2
	}
	abs, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}

	rules := judge.BuiltinDelivery()
	rulesWhy := fmt.Sprintf("内置 %d 条", len(rules))
	if *rulesFile != "" {
		loaded, err := judge.Load(*rulesFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		rules, rulesWhy = loaded, *rulesFile
	}
	if *showRules {
		printGateRules(rulesWhy, rules)
		return 0
	}

	// 事实来源 ①：org 真相源。它自己那份校验的发现照旧由 `anc org check` 报，这里不重复。
	o, err := org.Load(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	// 事实来源 ②：timeline 留痕。目录不存在 = 还没人记过 = 空时间线，不是错。
	doc, err := timeline.Load(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	facts := judge.VaultFacts(o, doc)
	findings := judge.JudgeDelivery(facts, rules)

	if *asJSON {
		printGateJSON(abs, o, facts, rulesWhy, findings)
		return 0
	}
	printGate(abs, o, facts, rulesWhy, findings)
	return 0
}

func printGate(vault string, o *org.Org, f judge.DeliveryFacts, rulesWhy string, findings []judge.Finding) {
	fmt.Printf("anc gate —— 交付链路的门（只出结论，不设门禁）\n")
	fmt.Printf("  vault      %s\n", vault)
	fmt.Printf("  org        %s（启用 %d / 共 %d）\n", o.Company.Name, len(o.Enabled()), len(o.Members))
	fmt.Printf("  timeline   %d 条留痕 / %d 个 case（读不懂 %d 行）\n",
		f.Metrics["records"], f.Metrics["cases"], len(f.Bad))
	fmt.Printf("  判据       %s\n", rulesWhy)

	fmt.Printf("\n事实（全部来自真相源，判据不自己造）\n")
	fmt.Printf("  项目 %d   业务域 %d   立项书副本 %d   授权 %d   启用的人 %d\n",
		f.Metrics["projects"], f.Metrics["domains"], f.Metrics["charters"], f.Metrics["grants"], f.Metrics["members"])
	fmt.Printf("  门（timeline 的 case = gate:<slug>）\n")
	for _, g := range judge.BuiltinGates() {
		cf := f.ByCase[judge.GateCase(g.Slug)]
		mark := "○"
		if cf.Evidence > 0 {
			mark = "●"
		}
		extra := ""
		if cf.Unknown > 0 {
			extra = fmt.Sprintf("   灰 %d（status 词认得的不在配色表里）", cf.Unknown)
		}
		fmt.Printf("    %s %-10s %-6s 绿 %d / 黄 %d / 红 %d   共 %d 条%s\n",
			mark, g.Slug, g.Name, cf.Done, cf.Running, cf.Stuck, cf.Evidence, extra)
	}

	fmt.Printf("\n结论（%d 条）\n", len(findings))
	if len(findings) == 0 {
		fmt.Printf("  手上这几条判据都没命中。\n")
		fmt.Printf("  注意这**不等于**门过了：判据只覆盖了它能从留痕里看出来的那两类。\n")
	}
	for _, fd := range findings {
		icon := "ℹ️ "
		if fd.Level == judge.LevelWarn {
			icon = "⚠️  "
		}
		fmt.Printf("\n  %s[%s] %s\n", icon, fd.Rule, fd.Title)
		fmt.Printf("      %s\n", fd.Say)
		fmt.Printf("      来源 %s / 位置 %s\n", fd.Source, fd.Where)
		for _, ev := range fd.Evidence {
			fmt.Printf("      - %s\n", ev)
		}
	}
	if len(findings) > 0 {
		fmt.Printf("\n  判据表只覆盖了手上这几条，扩大覆盖靠改那份数据（--rules），不靠改代码。\n")
	}
	if len(f.Bad) > 0 {
		fmt.Printf("  ⚠️  timeline 有 %d 行读不懂（少一行就可能把「卡住」看成「没事」）：%s\n",
			len(f.Bad), strings.Join(f.Bad, "、"))
	}
}

func printGateJSON(vault string, o *org.Org, f judge.DeliveryFacts, rulesWhy string, findings []judge.Finding) {
	type gateRow struct {
		Slug    string `json:"slug"`
		Name    string `json:"name"`
		Green   int    `json:"green"`
		Yellow  int    `json:"yellow"`
		Red     int    `json:"red"`
		Unknown int    `json:"unknown"`
	}
	rows := []gateRow{}
	for _, g := range judge.BuiltinGates() {
		cf := f.ByCase[judge.GateCase(g.Slug)]
		rows = append(rows, gateRow{g.Slug, g.Name, cf.Done, cf.Running, cf.Stuck, cf.Unknown})
	}
	out := map[string]any{
		"schema":  gateSchema,
		"vault":   vault,
		"company": o.Company.Name,
		"rules":   rulesWhy,
		"facts": map[string]any{
			"counts": f.Metrics,
			"gates":  rows,
			"bad":    f.Bad,
		},
		"findings": findings,
		"gates":    judge.BuiltinGates(),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
	}
}

func printGateRules(why string, rules []judge.Rule) {
	fmt.Printf("anc gate —— 生效判据表（%s）\n\n", why)
	for _, r := range rules {
		fmt.Printf("  %-32s %-9s %-5s %s\n", r.ID, r.Scope, r.Level, r.Title)
		var conds []string
		for _, c := range r.When {
			conds = append(conds, c.String())
		}
		fmt.Printf("      当 %s\n", strings.Join(conds, " 且 "))
		fmt.Printf("      说 %s\n", r.Say)
		if r.NoEvidenceOk {
			fmt.Printf("      豁免证据：这条判据的结论本身就是「没有证据」\n")
		}
	}
}

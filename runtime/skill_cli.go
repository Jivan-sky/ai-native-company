package main

// anc skill —— 让 agent **自己查**有哪些技能、哪些是新的。
//
// 我们不做推送（用户定的）：推送等于替每个 agent 做决定，而技能会改行为。
// 这条命令只回答两个问题：有什么、哪些是新的；然后把"要不要用"交回给人。

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"anc/internal/exp"
)

const skillUsage = `anc skill —— 技能目录（拉取式，不推送）
  anc skill list  --vault D [--json]              # 全部已部署技能
  anc skill check --vault D --for WHO [--json]    # 只看「上次看过之后」新增或有变化的
  anc skill seen  --vault D --for WHO             # 把当前的都标记为已看过

纪律：**不要自己采纳**。看完之后问你的用户："有新技能 X，要不要用？"
采纳 = 人改真相源里的 skills 列，不是 agent 自己改。
`

func cmdSkill(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, skillUsage)
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("skill "+sub, flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 目录")
	for_ := fs.String("for", "", "谁在看（成员名或 agent slug）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	fa, pos := splitArgs(args[1:], map[string]bool{"json": true})
	if err := fs.Parse(fa); err != nil {
		return 2
	}
	if *vault == "" && len(pos) > 0 {
		*vault = pos[0]
	}
	if *vault == "" {
		fmt.Fprintln(os.Stderr, "错误：缺 --vault")
		return 2
	}

	switch sub {
	case "list":
		items, err := exp.Catalog(*vault)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		printCatalog(items, *asJSON)
	case "check":
		if *for_ == "" {
			fmt.Fprintln(os.Stderr, "错误：check 要写 --for（谁在看）")
			return 2
		}
		items, err := exp.NewSince(*vault, *for_)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		miss, _ := exp.MissingSkillFiles(*vault)
		if *asJSON {
			b, _ := json.MarshalIndent(map[string]any{"new": items, "missing": miss}, "", "  ")
			fmt.Println(string(b))
			return 0
		}
		if len(items) == 0 {
			fmt.Println("没有新技能。")
		} else {
			printCatalog(items, false)
			fmt.Println()
			fmt.Println("提醒（给 agent 自己看，不是给人下命令）：")
			fmt.Println("  · 这些是**公司已有**的技能，不是给你自动装上的；")
			fmt.Println("  · 要不要用，**先问你的用户**：「有新技能 <名字>，要不要用？」")
			fmt.Println("  · 采纳由人改真相源的 skills 列，不要自己改。")
		}
		if len(miss) > 0 {
			fmt.Printf("\n! 有 %d 条账本说已部署、盘上却没有：%v\n", len(miss), miss)
		}
	case "seen":
		if *for_ == "" {
			fmt.Fprintln(os.Stderr, "错误：seen 要写 --for（谁看的）")
			return 2
		}
		items, err := exp.Catalog(*vault)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		if err := exp.MarkSeen(*vault, *for_, items); err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		fmt.Printf("已记下 %s 看过 %d 条技能\n", *for_, len(items))
	default:
		fmt.Fprint(os.Stderr, skillUsage)
		return 2
	}
	return 0
}

func printCatalog(items []exp.CatalogItem, asJSON bool) {
	if asJSON {
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return
	}
	if len(items) == 0 {
		fmt.Println("还没有已部署的技能。")
		return
	}
	for _, it := range items {
		fmt.Printf("%-28s v%-3d %-14s %s\n", it.Name, it.Version, it.Scope, it.Description)
	}
}

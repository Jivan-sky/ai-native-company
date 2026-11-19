package main

// anc blocker / anc breakthrough —— 卡点与突破的录入口。
//
// 为什么需要命令行入口：对象建好了但没人能写，等于没建。
// 这两个命令只做三件事：记一条、解开一条、看"踩了几次"。

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"anc/internal/casework"
)

const caseworkUsage = `anc blocker —— 卡点（「这个坑踩了几次」靠它答）
  anc blocker add --vault D --title T --kind K [--case C] [--domain X] [--stage S]
                  [--waiting-on W] [--why ...] [--from EV[,EV]] [--id ID]
  anc blocker resolve --vault D --id ID --by WHO
  anc blocker list --vault D [--json]
  anc blocker recurring --vault D [--json]     # 按「域 + 类别」归组，看哪类坑反复踩

anc breakthrough —— 突破（必须挂在一条真实卡点上）
  anc breakthrough add --vault D --blocker blk-1 --method "怎么解的" [--cost C] [--scope S]
                       [--by WHO] [--reusable] [--evidence E[,E]] [--id ID]
  anc breakthrough list --vault D [--json]
`

func cmdBlocker(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, caseworkUsage)
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("blocker "+sub, flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 目录")
	id := fs.String("id", "", "卡点 id")
	title := fs.String("title", "", "卡在哪（一句话）")
	kind := fs.String("kind", "", "类别：口径/权限/工具/人/外部")
	caseID := fs.String("case", "", "归属的 case / 任务")
	domain := fs.String("domain", "", "业务域")
	stage := fs.String("stage", "", "卡在流程哪一步")
	waiting := fs.String("waiting-on", "", "在等谁 / 等什么")
	why := fs.String("why", "", "展开：现象与上下文")
	from := fs.String("from", "", "出处：事件 id 或 文件:行号，逗号分隔")
	by := fs.String("by", "", "谁解的")
	asJSON := fs.Bool("json", false, "输出 JSON")

	fa, pos := splitArgs(args[1:], map[string]bool{})
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
	case "add":
		b, err := casework.AddBlocker(*vault, casework.Blocker{
			ID: *id, Title: *title, Kind: *kind, Case: *caseID, Domain: *domain,
			Stage: *stage, WaitingOn: *waiting, Why: *why, From: splitComma(*from)})
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		printBlockers([]casework.Blocker{b}, *asJSON)
	case "resolve":
		b, err := casework.Resolve(*vault, *id, *by)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		printBlockers([]casework.Blocker{b}, *asJSON)
	case "list":
		all, err := casework.ListBlockers(*vault)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		printBlockers(all, *asJSON)
	case "recurring":
		groups, err := casework.Recurring(*vault)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		if *asJSON {
			b, _ := json.MarshalIndent(groups, "", "  ")
			fmt.Println(string(b))
			return 0
		}
		if len(groups) == 0 {
			fmt.Println("还没有卡点记录 —— 一条都没有，说明「踩了几次」暂时答不出来")
			return 0
		}
		for _, g := range groups {
			fmt.Printf("%-12s %-8s 共 %d 次（已解 %d）  %s\n",
				firstNonEmpty(g.Domain, "(未标域)"), g.Kind, g.Count, g.Solved,
				strings.Join(g.BlockerIDs, ", "))
		}
	default:
		fmt.Fprint(os.Stderr, caseworkUsage)
		return 2
	}
	return 0
}

func cmdBreakthrough(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, caseworkUsage)
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("breakthrough "+sub, flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 目录")
	id := fs.String("id", "", "突破 id")
	blocker := fs.String("blocker", "", "对应哪条卡点（必填）")
	method := fs.String("method", "", "怎么解的（要能被人照着再做一遍）")
	cost := fs.String("cost", "", "代价：工时 / 返工 / 钱")
	scope := fs.String("scope", "", "适用于哪类任务")
	by := fs.String("by", "", "谁解的")
	evidence := fs.String("evidence", "", "证据，逗号分隔")
	reusable := fs.Bool("reusable", false, "能不能复用（能进蒸馏候选）")
	asJSON := fs.Bool("json", false, "输出 JSON")

	fa, pos := splitArgs(args[1:], map[string]bool{})
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
	case "add":
		bt, err := casework.AddBreakthrough(*vault, casework.Breakthrough{
			ID: *id, BlockerID: *blocker, Method: *method, Cost: *cost, Scope: *scope,
			By: *by, Reusable: *reusable, Evidence: splitComma(*evidence)})
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		if *asJSON {
			b, _ := json.MarshalIndent(bt, "", "  ")
			fmt.Println(string(b))
			return 0
		}
		fmt.Printf("%s  → %s  %s\n", bt.ID, bt.BlockerID, bt.Method)
	case "list":
		all, err := casework.ListBreakthroughs(*vault)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		if *asJSON {
			b, _ := json.MarshalIndent(all, "", "  ")
			fmt.Println(string(b))
			return 0
		}
		for _, bt := range all {
			flagReuse := ""
			if bt.Reusable {
				flagReuse = " [可复用]"
			}
			fmt.Printf("%-24s %-24s %s%s\n", bt.ID, bt.BlockerID, bt.Method, flagReuse)
		}
	default:
		fmt.Fprint(os.Stderr, caseworkUsage)
		return 2
	}
	return 0
}

func printBlockers(list []casework.Blocker, asJSON bool) {
	if asJSON {
		b, _ := json.MarshalIndent(list, "", "  ")
		fmt.Println(string(b))
		return
	}
	for _, x := range list {
		wait := ""
		if x.WaitingOn != "" {
			wait = " 等 " + x.WaitingOn
		}
		fmt.Printf("%-20s %-10s %-8s %s%s\n", x.ID, x.Status, x.Kind, x.Title, wait)
	}
}

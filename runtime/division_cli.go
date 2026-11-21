package main

// anc division —— 「这件事该谁拍板」的可执行判定（判据 11'）。
//
// 它不猜事实：三个事实（跨数据 / 花钱+承诺+权限 / 可不可逆）由提案人或一线 agent 声明，
// 本命令按声明的树给结论 + 理由 + 命中哪条规则。同样的事实永远同一个答案。

import (
	"flag"
	"fmt"
	"os"

	"anc/internal/division"
)

const divisionUsage = `anc division —— 该谁拍板（可执行判定树）

  anc division --cross-data --irreversible        # 声明事实，拿结论
  anc division --selftest                          # 用内置 20 题答案集回放
  anc division --json                              # 机器读

事实（都默认 false —— false 不是"小事"，是"提案人说不是"）：
  --cross-data     会不会把别人的数据/结论带到别人眼前
  --money          花钱
  --promise        改对外承诺（报价 / SOW / 合同）
  --permissions    动别人的权限
  --irreversible   做错了收不回（删 / 合并 / 对外发送）

序：跨数据 → 钱/承诺/权限 → 不可逆 → 才轮到 agent 自己做。
`

func cmdDivision(args []string) int {
	fs := flag.NewFlagSet("division", flag.ContinueOnError)
	crossData := fs.Bool("cross-data", false, "会不会把别人的东西带到别人眼前")
	money := fs.Bool("money", false, "花钱")
	promise := fs.Bool("promise", false, "改对外承诺")
	perms := fs.Bool("permissions", false, "动别人的权限")
	irrev := fs.Bool("irreversible", false, "做错了收不回")
	name := fs.String("name", "", "这件事叫什么（只用于输出）")
	selftest := fs.Bool("selftest", false, "用内置答案集回放")
	asJSON := fs.Bool("json", false, "输出 JSON")
	fa, _ := splitArgs(args, map[string]bool{"cross-data": true, "money": true, "promise": true,
		"permissions": true, "irreversible": true, "selftest": true, "json": true})
	if err := fs.Parse(fa); err != nil {
		return 2
	}

	if *selftest {
		res, err := division.SelfTest()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		if *asJSON {
			printJSON(res)
		} else {
			fmt.Printf("anc division --selftest\n  题数 %d / 一致 %d（%.0f%%）\n", res.Total, res.Agreed, res.Rate*100)
			for _, d := range res.Disagreed {
				fmt.Println("  ✗ " + d)
			}
			if len(res.Disagreed) == 0 {
				fmt.Println("  结论 树与答案集一致 —— 但答案集本身要人签过字才算数")
			}
		}
		if len(res.Disagreed) > 0 {
			return 1
		}
		return 0
	}

	v := division.Decide(division.Case{Name: *name, CrossData: *crossData, Money: *money,
		Promise: *promise, Permissions: *perms, Irreversible: *irrev})
	if *asJSON {
		printJSON(v)
		return 0
	}
	who := "agent 可做（人抽查）"
	if v.Human {
		who = "人拍板"
	}
	if *name != "" {
		fmt.Printf("%s\n", *name)
	}
	fmt.Printf("  结论  %s\n", who)
	fmt.Printf("  理由  %s\n", v.Reason)
	fmt.Printf("  规则  %s\n", v.Rule)
	for _, p := range v.Path {
		fmt.Printf("  路径  %s\n", p)
	}
	return 0
}

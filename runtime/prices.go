package main

// anc prices —— 看价格表（数据；出厂是空表）。
//
// 为什么单独一条命令：价格是**数据**，谁都能换（`--prices` 指一份客户自己的）；
// 而「这个数哪天生效、出处是哪」必须和数字一起看，否则算出来的成本不可信。

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"anc/internal/pricing"
)

const pricesUsage = `anc prices —— 看生效的价格表（一条模型一行；单位与币种在表头）

用法：
  anc prices [--prices <文件>] [--json]

选项：
  --prices <文件>  用这份表（默认用内嵌的出厂表 —— **空表**）
  --json           出 JSON

口径：
  · 出厂表是空的：我们不知道各家 2026 年的挂牌价，编一个数比没有更坏；
  · 没有条目的模型一律显式「无价格」，不按相似模型估；
  · 要让 anc trail 算钱，就 anc trail --prices <文件>（默认不算钱）。
`

func cmdPrices(args []string) int {
	fs := flag.NewFlagSet("prices", flag.ContinueOnError)
	file := fs.String("prices", "", "价格表文件（默认内嵌空表）")
	asJSON := fs.Bool("json", false, "出 JSON")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) > 0 {
		fmt.Fprint(os.Stderr, pricesUsage)
		return 2
	}
	t, err := pricing.Load(*file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if *asJSON {
		b, err := json.MarshalIndent(t, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	fmt.Println("anc prices —— 生效的价格表")
	if *file == "" {
		fmt.Println("  来源     内嵌出厂表（空）")
	} else {
		fmt.Printf("  来源     %s\n", *file)
	}
	fmt.Printf("  币种     %s · 单位 %s\n", t.Currency, t.Unit)
	if len(t.Entries) == 0 {
		fmt.Println("  条目     0 条")
		fmt.Println()
		fmt.Println("  出厂表是空的：不知道各家挂牌价，编一个数比没有更坏。")
		fmt.Println("  要算钱：写一份表（模型 / 四类单价 / 生效日期 / 出处），再 `anc trail --prices <文件>`。")
		return 0
	}
	fmt.Printf("  条目     %d 条\n\n", len(t.Entries))
	for _, e := range t.Entries {
		fmt.Printf("  %-24s in %-8g out %-8g cache_read %-8g cache_write %-8g  生效 %s  %s\n",
			e.Model, e.In, e.Out, e.CacheRead, e.CacheWrite, orDash(e.EffectiveFrom), orDash(e.Source))
	}
	return 0
}

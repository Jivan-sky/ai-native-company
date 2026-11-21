package main

// anc effect —— 飞轮第⑥环：用了之后有没有用。
//
// 只报能算出来的；算不出来的写在 Unknown 里，不编数字。

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"anc/internal/effect"
)

const effectUsage = `anc effect —— 效果盘点（飞轮第⑥环）
  anc effect <vault> [--json]

报三样：
  1 卡点按月次数与方向（down 才是好事）
  2 已部署经验被哪些岗位采纳（roles/*/persona.md 的 skills 列）
  3 算不出来的（装配只记到段级、没开流水等）—— 与结论一起给
`

func cmdEffect(args []string) int {
	fs := flag.NewFlagSet("effect", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "输出 JSON")
	fa, pos := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(fa); err != nil {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprint(os.Stderr, effectUsage)
		return 2
	}
	abs, err := filepath.Abs(pos[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	rep, err := effect.Compute(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if *asJSON {
		printJSON(rep)
		return 0
	}
	fmt.Println("anc effect —— 用了之后有没有用")
	fmt.Println("  卡点走势（down 才是好事）：")
	if len(rep.Trends) == 0 {
		fmt.Println("    （没有数据）")
	}
	for _, t := range rep.Trends {
		var parts []string
		for _, m := range t.Months {
			parts = append(parts, m.Month+"="+itoaSmall(m.Count))
		}
		fmt.Printf("    %-10s %-8s %s  → %s\n", t.Domain, t.Kind, joinSmall(parts, " "), t.Direction)
	}
	fmt.Println("  已部署经验：")
	if len(rep.Skills) == 0 {
		fmt.Println("    （没有）")
	}
	for _, s := range rep.Skills {
		adopt := "无人声明使用"
		if s.Adopted > 0 {
			adopt = "被 " + itoaSmall(s.Adopted) + " 个岗位采纳：" + joinSmall(s.AdoptedBy, "、")
		}
		fmt.Printf("    %-28s v%-2d %s\n", s.ID, s.Version, adopt)
	}
	if len(rep.Unknown) > 0 {
		fmt.Println("  算不出来的（这一版就是这么定的）：")
		for _, u := range rep.Unknown {
			fmt.Println("    · " + u)
		}
	}
	return 0
}

func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func joinSmall(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}

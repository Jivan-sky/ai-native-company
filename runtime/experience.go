package main

// anc experience —— 经验对象（治理判据 7/8/12）。
//
// 为什么要有这条命令：经验是**唯一会改 Agent 行为**的东西，所以它不能像草稿那样随手改。
// 状态机只允许往前走：candidate → approved → deployed → retired；
// 没 approve 就 deploy 一律拒（判据 7），每次 deploy 留版本与 digest（判据 8），
// 否决与废止都留理由（判据 12）。

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"anc/internal/exp"
)

const experienceUsage = `anc experience —— 经验对象（候选 → 确认 → 部署 → 废止）

  anc experience add --vault D --id ID --title T --claim C --scope S --from EV[,EV] [--by WHO]
  anc experience approve --vault D --id ID --by WHO [--why W]
  anc experience deploy  --vault D --id ID --by WHO
  anc experience reject  --vault D --id ID --by WHO --why W
  anc experience retire  --vault D --id ID --by WHO --why W
  anc experience list    --vault D [--json]

纪律：
  - add 必须给 --from（没有出处的经验无法回溯）
  - deploy 必须先 approve（未经人工确认不得生效）
  - reject / retire 必须给 --why（否决与下线的理由比同意更重要）
`

func cmdExperience(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, experienceUsage)
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("experience "+sub, flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 目录")
	id := fs.String("id", "", "经验 id")
	title := fs.String("title", "", "标题")
	claim := fs.String("claim", "", "一句话结论")
	scope := fs.String("scope", "", "适用范围")
	from := fs.String("from", "", "出处：事件 id 或 文件:行号，逗号分隔")
	by := fs.String("by", "", "谁")
	why := fs.String("why", "", "为什么")
	asJSON := fs.Bool("json", false, "输出 JSON")

	flagArgs, pos := splitArgs(args[1:], map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if *vault == "" && len(pos) > 0 {
		*vault = pos[0]
	}
	if *vault == "" {
		fmt.Fprintln(os.Stderr, "错误：缺 --vault")
		return 2
	}

	var (
		out any
		err error
	)
	switch sub {
	case "add":
		var e exp.Experience
		e, err = exp.Add(*vault, exp.Experience{ID: *id, Title: *title, Claim: *claim,
			Scope: *scope, From: splitComma(*from), By: *by})
		out = e
	case "approve":
		out, err = exp.Approve(*vault, *id, *by, *why)
	case "deploy":
		out, err = exp.Deploy(*vault, *id, *by)
	case "reject":
		out, err = exp.Reject(*vault, *id, *by, *why)
	case "retire":
		out, err = exp.Retire(*vault, *id, *by, *why)
	case "list":
		var list []exp.Experience
		list, err = exp.List(*vault)
		out = list
	default:
		fmt.Fprint(os.Stderr, experienceUsage)
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if *asJSON {
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	switch v := out.(type) {
	case exp.Experience:
		fmt.Printf("%s  %s  v%d  %s\n", v.ID, v.Status, v.Version, v.Title)
	case []exp.Experience:
		for _, e := range v {
			fmt.Printf("%-24s %-10s v%-2d %s\n", e.ID, e.Status, e.Version, e.Title)
		}
	}
	return 0
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

package main

// anc bench —— 「不问人，能不能拿到」的测量器（CLI 那一层）。
//
// 判分逻辑在 internal/bench（纯函数、有单测）。这一层只干三件事：
// 装 org、对每个视角调**同一个装配点**（ctxpack.Assemble —— CLI / 看板 / MCP 读出口共用的那一处）、
// 把结果摆成人能读的一张表和一份 JSON。
//
// 为什么用装配点而不是自己再去读一遍 vault：那样量的是「我会不会读文件」，
// 不是「agent 到手了什么」。装配点是三个出口的唯一来源，量它就等于量真实送达。

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"anc/internal/bench"
	"anc/internal/ctxpack"
	"anc/internal/org"
)

const benchUsage = `anc bench —— 「不问人，能不能拿到」的测量器

用法：
  anc bench <vault> --suite <目录|文件> [--k <视角数>] [--json] [--min <0..1>]

选项：
  --suite <路径>  任务集：目录（按文件名排序读全部 *.json）或单个 .json
  --k <n>         每个任务最多跑几个视角（0 = 任务里写的全跑）
  --json          出 JSON（机器读；人读那份是默认输出）
  --min <0..1>    最低 pass^k 率；给了且低于它就退出码 1（默认 0 = 只报数，不判）

它判什么：
  一个任务开工前需要的东西（域 / 项目 / 技能 / 授权 / 工具）有没有**送到这个人手上**。
  每条没送到的都要给出「该问谁」—— 所以分数可以直接读成「这个任务有几处得靠人开口」。
  同一个任务要**所有视角都送到**才算过（pass^k）：人事一动就断的装配，不算装配。

它不判什么：
  模型聪不聪明、活最后做没做成、答案对不对。那些要真跑 agent，是另外一关。

口径：任务集是**数据**。客户可以把自己的一天活填成一份 suite 交给我们跑；
我们自己造的任务在报告里单独计（自造的证明「仪器能报数也能报错」，不构成外部证据）。
`

func cmdBench(args []string) int {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	suite := fs.String("suite", "", "任务集：目录或单个 .json")
	k := fs.Int("k", 0, "每个任务最多跑几个视角（0 = 全跑）")
	asJSON := fs.Bool("json", false, "出 JSON")
	minRate := fs.Float64("min", 0, "最低 pass^k 率；低于它以退出码 1 结束")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 || *suite == "" {
		fmt.Fprint(os.Stderr, benchUsage)
		return 2
	}
	vault := posArgs[0]

	s, err := bench.LoadSuite(*suite)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 2
	}
	o, err := org.Load(vault)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：vault 加载失败（先 `anc org check %s`）：\n%v\n", vault, err)
		return 1
	}
	now := time.Now()
	rep := bench.Report{Schema: bench.Schema, Vault: vault}

	for _, t := range s.Tasks {
		tr := bench.TaskResult{ID: t.ID, Title: t.Title, Origin: t.Origin, Note: t.Note}
		pass := true
		for _, who := range bench.FilterViews(t.As, *k) {
			out, _ := ctxpack.Assemble(o, vault, who, "", "", now, ctxpack.TasksIn{})
			v := bench.Grade(out, o, allowedToolsOf(o, out), t)
			tr.Views = append(tr.Views, v)
			if !v.AllDelivered() {
				pass = false
			}
		}
		tr.PassK = pass
		rep.Add(tr)
	}

	if *asJSON {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		fmt.Println(string(b))
	} else {
		printBench(rep, *suite)
	}
	if *minRate > 0 && rep.PassKRate < *minRate {
		fmt.Fprintf(os.Stderr, "\n低于门槛：pass^k %.0f%% < --min %.0f%%\n", rep.PassKRate*100, *minRate*100)
		return 1
	}
	return 0
}

// allowedToolsOf 取这个视角所代岗位的预授权工具。
// 身份解不出来（视角写错、成员被停用）时返回 nil —— 那会让 tool 类 need 全部判没到手，
// 与「人都不认识，当然什么也拿不到」一致，不假装它拿到过。
func allowedToolsOf(o *org.Org, out ctxpack.Out) []string {
	if out.Identity == nil {
		return nil
	}
	r, ok := o.Roles[out.Identity.Role]
	if !ok {
		return nil
	}
	return r.AllowedTools
}

func printBench(rep bench.Report, suite string) {
	fmt.Println("anc bench —— 不问人，能不能拿到")
	fmt.Printf("  vault    %s\n", rep.Vault)
	fmt.Printf("  任务集   %s（%d 个任务：外部 %d / 自造 %d）\n", suite, len(rep.Tasks), rep.External, rep.Synthetic)
	fmt.Printf("  视角     %d 个（每个任务按它自己写的视角跑，全部送到才算过）\n\n", rep.Views)

	for _, t := range rep.Tasks {
		mark := "✅"
		if !t.PassK {
			mark = "❌"
		}
		title := t.Title
		if title == "" {
			title = t.ID
		}
		fmt.Printf("  %s %s\n", mark, title)
		if t.Note != "" {
			fmt.Printf("      口径：%s\n", t.Note)
		}
		for _, v := range t.Views {
			who := v.Who
			if v.Name != "" && v.Name != v.Who {
				who = fmt.Sprintf("%s（%s）", v.Who, v.Name)
			}
			fmt.Printf("      %-18s 到手 %d/%d  要问人 %d\n", who, v.Delivered, v.Total, v.Asks)
			for _, n := range v.Needs {
				if n.Delivered {
					continue
				}
				// 没到手的那几条是这份输出里唯一值得人读的部分：缺什么、去哪看、该问谁。
				ask := n.Ask
				if ask == "" {
					ask = "（没有指路 —— 这一条谁也不知道该问谁）"
				}
				fmt.Printf("        缺 %s=%s  → 该问 %s\n", n.Kind, n.Value, ask)
			}
		}
	}

	fmt.Printf("\n  送达率   %d/%d（%.0f%%）\n", rep.Delivered, rep.Needs, rep.DeliveryRate*100)
	fmt.Printf("  pass^k   %d/%d 个任务「全部视角全到手」（%.0f%%）\n", passkCount(rep), len(rep.Tasks), rep.PassKRate*100)
	fmt.Printf("  要问人   %d 处\n", rep.Asks)
	if rep.Synthetic > 0 {
		fmt.Printf("\n  注意：这份任务集里有 %d 个是我们自己造的 —— 它证明仪器能报数、能报错，\n"+
			"        不构成外部证据。外部证据要等客户 / 第三方交一份他们自己的 suite。\n", rep.Synthetic)
	}
}

func passkCount(rep bench.Report) int {
	n := 0
	for _, t := range rep.Tasks {
		if t.PassK {
			n++
		}
	}
	return n
}

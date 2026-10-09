package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/gateway"
	"anc/internal/notify"
	"anc/internal/org"
	"anc/internal/probe"
	renderpkg "anc/internal/render"
)

const notifyUsage = `anc notify —— 把运行态告警送到负责人面前（默认只打印，不发送）

用法：
  anc notify <vault 目录> [选项]

选项：
  --config <文件>   gateway config（默认 <vault>/../gateway/config.toml）
  --data <目录>     gateway data_dir（默认 <vault>/../data）
  --stale <时长>    多久没有成功交互算不新鲜（默认 24h）
  --stall <时长>    最后一轮没人回多久算卡住（默认 10m）
  --cooldown <时长> 同一个问题多久内不重复喊（默认 30m）
  --send            真发；不加就只打印「该喊谁、喊什么」
  --json            给 watchdog / 定时任务消费的 JSON（打到 stdout）

跟 anc probe 的分工是一条硬边界：
  probe 只观测、只读，永远没有副作用；notify 有 —— 它通过 cc-connect 的 socket
  把消息推进人的聊天里。合在一起会毁掉 probe 的立身之本（只读命令被随手跑），
  所以分成两条命令。判据还是同一份（internal/probe），不写两套。

通知判据只有两条：
  1. 只看红。黄 = 运行中 / 这一窗口没观测到，不要人动手；把黄也推给人，
     人被吵烦就会静音，红的告警也一起死。
  2. 边沿触发 + 冷却。状态没变不喊；一直红着每过一个冷却期提醒一次
     （否则就成了「没消息 = 没事」）；红转绿喊一次「已恢复」并清账。

送哪去：company.admins 各自的 bot —— 推给**管理员自己**的聊天，不是出故障那个 bot
（出故障的那个可能连话都回不了）。这就是「报红要说清交给谁」的落地。

游标落在 <data>/state/notify.json（不进 git）。**dry-run 不写游标**，
写了会把下一次真发的告警吞掉。

退出码：0 该喊的都送到了（或没有要喊的）；1 有该喊的没送出去；2 用法错误
`

func cmdNotify(args []string) int {
	fs := flag.NewFlagSet("notify", flag.ContinueOnError)
	cfg := fs.String("config", "", "gateway config")
	data := fs.String("data", "", "gateway data_dir")
	stale := fs.Duration("stale", probe.DefaultStale, "多久没有成功交互算不新鲜")
	stall := fs.Duration("stall", probe.DefaultStall, "最后一轮没人回多久算卡住")
	cooldown := fs.Duration("cooldown", notify.DefaultCooldown, "同一个问题多久内不重复喊")
	send := fs.Bool("send", false, "真发（不加则只打印）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	vault := fs.String("vault", "", "vault 目录（也可用位置参数）")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"send": true, "json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	root := strings.TrimSpace(*vault)
	if root == "" && len(posArgs) > 0 {
		root = posArgs[0]
	}
	if root == "" {
		fmt.Fprint(os.Stderr, notifyUsage)
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

	// 该喊谁：人名（给人看）+ project 名（给机器用）。真相源读不动就**不猜** ——
	// 一份红报告配上猜错的责任人，比不喊还糟。
	var handlers, targets, unresolved, unwired []string
	if o, err := org.Load(abs); err == nil {
		handlers = o.AdminLabels()
		targets, unresolved = notifyTargets(o)
		// 声明「还没接凭据」的 bot 不该被喊 —— 它不是事故，喊了就是狼来了。
		// 跟探针共用同一支算（各算各的迟早对不上号）。
		unwired = renderpkg.UnwiredProjects(o)
		if len(o.Company.Admins) == 0 {
			unresolved = append(unresolved, "真相源里 company.admins 是空的 —— 现在没人可交")
		}
	} else {
		unresolved = append(unresolved,
			"真相源读不动，算不出该交给谁（不猜）："+firstLine(err.Error()))
	}

	rep, err := probe.Run(probe.Options{
		Vault: abs, Config: cfgPath, DataDir: dataPath,
		Stale: *stale, Stall: *stall, Admins: handlers, Unwired: unwired,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		fmt.Fprintf(os.Stderr, "FIX: 先 `anc render %s --apply`\n", abs)
		return 1
	}

	cursorPath := notify.CursorPath(dataPath)
	cur, err := notify.LoadCursor(cursorPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 1
	}
	plan := notify.Decide(cur, rep, *cooldown, time.Now())

	rows := make([]sentRow, 0, len(plan.Actions))
	failed := false

	for _, a := range plan.Actions {
		text := notify.Text(a, handlers, *cooldown)
		row := sentRow{Action: a, Text: text, To: targets}
		if *send {
			if len(targets) == 0 {
				row.Errors = append(row.Errors, "没有可推的 bot —— 喊不出去（"+strings.Join(unresolved, "；")+"）")
			}
			for _, t := range targets {
				if err := notify.Push(gateway.CCConnect.SocketPath(dataPath), t, text); err != nil {
					row.Errors = append(row.Errors, t+"："+err.Error())
				}
			}
			// 只要有一个人没收到就算没喊完：宁可让收到的人多收一遍，
			// 也不能让没收到的那位被当成「已经告诉过他了」（游标因此不落，下轮重试）。
			if len(row.Errors) > 0 {
				failed = true
			}
		}
		rows = append(rows, row)
	}

	if *send && !failed {
		// 只有真发过才落游标。dry-run 落了会把下一次真发的告警吞掉。
		if err := plan.Next.Save(cursorPath); err != nil {
			fmt.Fprintf(os.Stderr, "错误: 写游标失败：%v\n", err)
			return 1
		}
	}

	if *asJSON {
		out := map[string]any{
			"schema":   notify.Schema,
			"sent":     *send,
			"cooldown": cooldown.String(),
			"handlers": handlers,
			"targets":  targets,
			"actions":  rows,
			"silent":   plan.Silent,
		}
		if len(unresolved) > 0 {
			out["unresolved"] = unresolved
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	} else {
		printNotify(rep, plan, rows, handlers, targets, unresolved, *send, *cooldown)
	}

	if !*send && len(plan.Actions) > 0 {
		return 1 // 有事、没送出去
	}
	if failed {
		return 1
	}
	return 0
}

// notifyTargets 把 company.admins 落成「往哪个 project 推」。
//
// 推给**管理员自己**的 bot，而不是出故障那个 bot —— 出故障的那个可能连话都回不了，
// 往它那儿喊等于对着坏掉的喇叭喊。推不出去的（不是成员 / 已停用）单独收着，
// 让调用方明说，而不是静默少喊一个人。
func notifyTargets(o *org.Org) (targets, unresolved []string) {
	for _, name := range o.Company.Admins {
		m, ok := o.Member(name)
		if !ok {
			unresolved = append(unresolved, name+" 不是成员，没有 bot 可推")
			continue
		}
		if m.Disabled {
			unresolved = append(unresolved, name+" 已停用，没有在跑的 bot")
			continue
		}
		targets = append(targets, o.Company.ID+"-"+m.Name)
	}
	return targets, unresolved
}

// sentRow 是一条动作的执行结果：--json 直接序列化它，终端也照它打印。
type sentRow struct {
	Action notify.Action `json:"action"`
	Text   string        `json:"text"`
	To     []string      `json:"to"`
	Errors []string      `json:"errors,omitempty"`
}

func printNotify(rep probe.Report, plan notify.Plan, rows []sentRow, handlers, targets, unresolved []string, send bool, cooldown time.Duration) {
	mode := "dry-run（没有发送）"
	if send {
		mode = "已发送"
	}
	fmt.Printf("anc notify —— 运行态告警（%s）\n", mode)
	fmt.Printf("  vault    %s\n", rep.Vault)
	fmt.Printf("  data     %s\n", rep.DataDir)
	fmt.Printf("  冷却     %s\n", cooldown)
	if len(handlers) > 0 {
		fmt.Printf("  该交给    %s\n", strings.Join(handlers, "、"))
		fmt.Printf("  推到      %s\n", strings.Join(targets, "、"))
	} else {
		fmt.Printf("  该交给    ⚠️  %s\n", strings.Join(unresolved, "；"))
	}
	fmt.Println()

	red, yellow := countStates(rep)
	if len(plan.Actions) == 0 {
		fmt.Printf("  没有要喊的（红 %d / 黄 %d）\n", red, yellow)
		if yellow > 0 {
			fmt.Println("  黄档不喊：它只是运行中 / 这一窗口没观测到，不要人动手 ——")
			fmt.Println("  把它也推给人，人就会静音，红的告警也一起死。")
		}
	} else {
		for _, r := range rows {
			mark := map[notify.Kind]string{notify.KindAlert: "新告警", notify.KindReminder: "仍未解决", notify.KindRecovery: "已恢复"}[r.Action.Kind]
			fmt.Printf("  [%s] %s\n", mark, r.Action.Key)
			for _, line := range strings.Split(r.Text, "\n") {
				fmt.Printf("      %s\n", line)
			}
			for _, e := range r.Errors {
				fmt.Printf("      ⚠️  %s\n", e)
			}
			fmt.Println()
		}
	}
	if len(plan.Silent) > 0 {
		fmt.Printf("  （%d 条在冷却期内被压下，不重复喊）\n", len(plan.Silent))
	}
	if len(unresolved) > 0 && len(handlers) > 0 {
		fmt.Printf("  ⚠️  %s\n", strings.Join(unresolved, "；"))
	}
	fmt.Println()
	if !send && len(plan.Actions) > 0 {
		fmt.Println("  要真发：加 --send（dry-run 不写游标）。")
	}
}

func countStates(rep probe.Report) (red, yellow int) {
	for _, f := range rep.Bots {
		switch f.State {
		case probe.StateFail:
			red++
		case probe.StateWarn:
			yellow++
		}
	}
	if rep.Gateway != "up" {
		red++
	}
	return red, yellow
}

package main

// anc app —— 挂到 ANC 上的脚本应用：装 / 审 / 跑 / 撤。
//
// 与 `anc job` 的分工：job 是「ANC 自己发起的 HTTP 调用」；app 是「别人写的代码，
// 在沙箱里跑」。两者都进同一本账。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"anc/internal/apps"
	"anc/internal/audit"
	"anc/internal/timeline"
)

const appUsage = `anc app —— 脚本应用：装进来（带 hash）/ 审批 / 沙箱里跑 / 撤销

用法：
  anc app install <vault> <目录>
  anc app ls      <vault>
  anc app approve <vault> <名> --by <谁>
  anc app revoke  <vault> <名>
  anc app run     <vault> <名> [--json]

口径：
  · 装的时候对整个目录算 sha256；跑之前重算，对不上就拒（审批的是那一个版本）；
  · 未审批不跑、已撤销不跑；
  · 没有沙箱（找不到 docker）就**拒绝执行**，不退化成裸跑。
`

func cmdApp(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, appUsage)
		return 2
	}
	switch args[0] {
	case "install":
		return cmdAppInstall(args[1:])
	case "ls":
		return cmdAppLs(args[1:])
	case "approve":
		return cmdAppApprove(args[1:])
	case "revoke":
		return cmdAppRevoke(args[1:])
	case "run":
		return cmdAppRun(args[1:])
	}
	fmt.Fprint(os.Stderr, appUsage)
	return 2
}

func cmdAppInstall(args []string) int {
	fs := flag.NewFlagSet("app install", flag.ContinueOnError)
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 2 {
		fmt.Fprint(os.Stderr, appUsage)
		return 2
	}
	st, err := apps.Install(posArgs[0], posArgs[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	fmt.Printf("✅ 已装 %s@%s\n   hash %s…\n   状态 %s（要跑得先 `anc app approve`）\n",
		st.Name, st.Version, st.SHA256[:12], st.State)
	return 0
}

func cmdAppLs(args []string) int {
	fs := flag.NewFlagSet("app ls", flag.ContinueOnError)
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 {
		fmt.Fprint(os.Stderr, appUsage)
		return 2
	}
	list, err := apps.List(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Println("还没有装任何应用。")
		return 0
	}
	for _, st := range list {
		extra := ""
		if st.ApprovedBy != "" {
			extra = fmt.Sprintf(" · 审批 %s @ %s", st.ApprovedBy, st.ApprovedAt)
		}
		fmt.Printf("  %-14s %-8s %-9s %s…%s\n", st.Name, st.Version, st.State, st.SHA256[:12], extra)
	}
	return 0
}

func cmdAppApprove(args []string) int {
	fs := flag.NewFlagSet("app approve", flag.ContinueOnError)
	by := fs.String("by", "", "审批人（必填）")
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 2 || strings.TrimSpace(*by) == "" {
		fmt.Fprint(os.Stderr, appUsage)
		return 2
	}
	st, err := apps.Approve(posArgs[0], posArgs[1], *by)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	fmt.Printf("✅ 已审批 %s（%s @ %s）\n", st.Name, st.ApprovedBy, st.ApprovedAt)
	return 0
}

func cmdAppRevoke(args []string) int {
	fs := flag.NewFlagSet("app revoke", flag.ContinueOnError)
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 2 {
		fmt.Fprint(os.Stderr, appUsage)
		return 2
	}
	st, err := apps.Revoke(posArgs[0], posArgs[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	fmt.Printf("✅ 已撤销 %s（下一次 run 就拒）\n", st.Name)
	return 0
}

func cmdAppRun(args []string) int {
	fs := flag.NewFlagSet("app run", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "JSON 出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 2 {
		fmt.Fprint(os.Stderr, appUsage)
		return 2
	}
	vault, name := posArgs[0], posArgs[1]
	res, err := apps.Run(context.Background(), vault, name, apps.Options{
		Record: func(r apps.Result, st apps.Installed) { recordAppRun(vault, r, st) },
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if *asJSON {
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(b))
	} else {
		printAppResult(res)
	}
	if res.Status != "ok" {
		return 1
	}
	return 0
}

func printAppResult(r apps.Result) {
	mark := map[string]string{"ok": "✅", "refused": "⛔", "failed": "⚠️"}[r.Status]
	fmt.Printf("%s 应用 %s · %s（%s）\n", mark, r.App, r.Status, r.Duration.Round(time.Millisecond))
	if r.Why != "" {
		fmt.Printf("  原因 %s\n", r.Why)
	}
	if r.Output != "" {
		for _, line := range strings.Split(r.Output, "\n") {
			fmt.Printf("  | %s\n", line)
		}
	}
}

// recordAppRun 把「跑/拒」都写进 audit 与 timeline —— 拒绝也是事实。
func recordAppRun(vault string, r apps.Result, st apps.Installed) {
	at := r.At.Format(time.RFC3339)
	result := audit.ResultOK
	if r.Status != "ok" {
		result = audit.ResultFailed
	}
	_, _ = audit.Append(vault, audit.Record{
		ID: fmt.Sprintf("app:%s:%d", r.App, r.At.UnixNano()), At: at,
		Actor: "anc", Action: audit.ActionInvoke, Object: "app:" + r.App,
		Result: result, Why: firstNonEmpty(r.Why, fmt.Sprintf("退出码 %d", r.Code)),
		Tool: "app:" + r.App, Source: "app", Ref: st.SHA256[:12],
	})
	tlStatus := "done"
	if r.Status != "ok" {
		tlStatus = "failed"
	}
	_, _ = timeline.Append(vault, timeline.Entry{
		ID: fmt.Sprintf("app:%s:%d", r.App, r.At.UnixNano()), Case: "app:" + r.App, At: at,
		Kind: "execution", Status: tlStatus, By: "anc",
		Title:  fmt.Sprintf("应用 %s %s", r.App, map[string]string{"ok": "跑通", "refused": "被拒", "failed": "失败"}[r.Status]),
		Detail: r.Why,
	})
}

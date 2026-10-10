package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/approvals"
	"anc/internal/envelope"
	"anc/internal/hot"
	"anc/internal/org"
)

// anc approvals —— 提案的待批队列与「点头」。
//
// 它落在整条链的哪一段（SPEC §6 授权模型 / §7 写路径；2026-10-10 拍板 —— 这里不新定口径）：
//
//	提案（信封 kind: proposal）→ 待批队列 → 人给一个可识别信号 → 留痕
//	                                                         ↓
//	               grants/ 那个文件由**人侧的管理者 bot** 写，不是 ANC 本体
//
// 审批的**落点**（看板 / 飞书卡片）都走同一个入口 —— 三个前端，一条管道（SPEC §7）。
const approvalsUsage = `anc approvals —— 提案的待批队列与「点头」（审批的落点走同一个入口）

用法：
  anc approvals add <vault> <信封.json> [--at RFC3339]
  anc approvals ls [--json] [--stale 24h]
  anc approvals decide <vault> <提案 id> -by <谁> -signal approve|reject|hold [-why <一句话>] [--at RFC3339]

它只做三件（ANC 只做这三件）：
  1 收提案   kind: proposal 的信封入队 —— 其余 kind 不入这个队（它们的去处不是「等人点头」）
  2 送给人   队列带着「该谁批」：域表 who 那一列 —— 岗位，不是人名（人名由展示层现算）
  3 留痕     点头落两笔：timeline 一条 decision + audit 一条 invoke

它不做：
  · 不写真相源 —— grants/ 那个文件由人侧的管理者 bot 写，不是 ANC
  · 不校「点的人是不是该批的人」—— 先原样留痕，判断交给 agent
  · 不认语义 —— 点头只认 approve / reject / hold 三个字，别的一律拒

待批 = 「进行中的事实」→ 热层；点头 = 「达标」→ 先落库、再清热层。
热层地址与前缀走 hot 那几个口子（--addr / --prefix / ANC_HOT_ADDR / ANC_HOT_PREFIX）。
`

func cmdApprovals(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, approvalsUsage)
		return 2
	}
	switch args[0] {
	case "add":
		return cmdApprovalsAdd(args[1:])
	case "ls", "list":
		return cmdApprovalsLs(args[1:])
	case "decide":
		return cmdApprovalsDecide(args[1:])
	}
	fmt.Fprint(os.Stderr, approvalsUsage)
	return 2
}

func cmdApprovalsAdd(args []string) int {
	fs := flag.NewFlagSet("approvals add", flag.ContinueOnError)
	o := addHotOpts(fs)
	at := fs.String("at", "", "RFC3339，默认现在")
	flagArgs, pos := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(pos) < 2 {
		fmt.Fprintln(os.Stderr, "错误：用法 anc approvals add <vault> <信封.json>")
		return 2
	}
	vault, err := filepath.Abs(pos[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	raw, err := os.ReadFile(pos[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：读不到信封：%v\n", err)
		return 1
	}
	e, err := envelope.Parse(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：这封信读不懂：%v\n", err)
		return 1
	}
	when := time.Now()
	if strings.TrimSpace(*at) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*at))
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：--at 必须是 RFC3339：%v\n", err)
			return 2
		}
		when = t
	}
	p, err := approvals.FromEnvelope(e, when)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	// 「该谁批」从域表解。**解不出只提示、不拦** —— 队列照收，缺的那一项写在明面上。
	note := ""
	if o0, err := org.Load(vault); err == nil {
		who, why := approvals.Approver(o0, e.Scope.Domain)
		p.To = who
		note = why
	} else {
		note = fmt.Sprintf("没加载到 org 真相源（%v）", err)
	}
	st, err := o.open()
	if err != nil {
		return hotUnreachable(err)
	}
	defer st.Close()
	saved, created, err := approvals.Enqueue(st, p)
	if err != nil {
		return hotFail(err)
	}
	fmt.Println("anc approvals add —— 提案入队（等人点头）")
	fmt.Printf("  提案 id   %s\n", saved.ID)
	fmt.Printf("  一句话    %s\n", saved.Title)
	fmt.Printf("  该谁批    %s\n", approverLine(saved.To, note))
	fmt.Printf("  进队时间  %s\n", saved.Enqueued)
	if created {
		fmt.Println("  结果      新入队")
	} else {
		fmt.Println("  结果      已经在队里（不重复入队，时间戳没动）")
	}
	return 0
}

func cmdApprovalsLs(args []string) int {
	fs := flag.NewFlagSet("approvals ls", flag.ContinueOnError)
	o := addHotOpts(fs)
	stale := fs.Duration("stale", hot.DefaultStale, "等了多久就算旧（只画标记，不拦不删）")
	asJSON := fs.Bool("json", false, "打 JSON")
	flagArgs, _ := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	st, err := o.open()
	if err != nil {
		return hotUnreachable(err)
	}
	defer st.Close()
	list, err := approvals.ListPending(st)
	if err != nil {
		return hotFail(err)
	}
	if *asJSON {
		out := map[string]any{
			"schema":  "anc.approvals/v1",
			"addr":    o.shownAddr(),
			"prefix":  st.Prefix(),
			"count":   len(list),
			"pending": list,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		return 0
	}
	fmt.Printf("anc approvals ls —— 待批 %d 条（热层 %s，前缀 %s）\n", len(list), o.shownAddr(), st.Prefix())
	if len(list) == 0 {
		fmt.Println("  队列是空的。提案进来才有：anc approvals add <vault> <信封.json>")
		return 0
	}
	now := time.Now()
	for _, p := range list {
		fmt.Printf("\n  %s\n", p.ID)
		fmt.Printf("    一句话    %s\n", p.Title)
		fmt.Printf("    该谁批    %s\n", approverLine(p.To, ""))
		fmt.Printf("    谁提的    %s\n", orDash(p.Who))
		if b := strings.TrimSpace(p.Body); b != "" {
			fmt.Printf("    提案原文  %s\n", strings.ReplaceAll(b, "\n", " / "))
		}
		fmt.Printf("    进队时间  %s%s\n", p.Enqueued, waitedMark(p.Enqueued, now, *stale))
	}
	return 0
}

func cmdApprovalsDecide(args []string) int {
	fs := flag.NewFlagSet("approvals decide", flag.ContinueOnError)
	o := addHotOpts(fs)
	by := fs.String("by", "", "谁点的（open_id / 人名 / bot 名；必填）")
	sig := fs.String("signal", "", "点头信号：approve / reject / hold")
	why := fs.String("why", "", "一句话理由（可选，原样留在痕里）")
	at := fs.String("at", "", "RFC3339，默认现在")
	flagArgs, pos := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(pos) < 2 {
		fmt.Fprintln(os.Stderr, "错误：用法 anc approvals decide <vault> <提案 id> -by <谁> -signal approve|reject|hold")
		return 2
	}
	vault, err := filepath.Abs(pos[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	s, ok := approvals.ParseSignal(*sig)
	if !ok {
		fmt.Fprintf(os.Stderr, "错误：认不出的信号 %q —— 只认 approve / reject / hold（不猜）\n", *sig)
		return 2
	}
	when := time.Now()
	if strings.TrimSpace(*at) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*at))
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：--at 必须是 RFC3339：%v\n", err)
			return 2
		}
		when = t
	}
	d, err := approvals.New(pos[1], *by, s, *why, when)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 2
	}
	st, err := o.open()
	if err != nil {
		return hotUnreachable(err)
	}
	defer st.Close()
	r, err := approvals.Settle(st, vault, d)
	if err != nil {
		if errors.Is(err, approvals.ErrNotQueued) {
			fmt.Fprintf(os.Stderr, "拒绝：%v\n", err)
			fmt.Fprintln(os.Stderr, "  · 队列里没有这条 = 要么没提过，要么已经结过账 —— 两个都别硬点。")
			return 1
		}
		return hotFail(err)
	}
	fmt.Println("anc approvals decide —— 点头（落两笔：库 + 审计）")
	fmt.Printf("  提案      %s\n", r.Pending.ID)
	fmt.Printf("  一句话    %s\n", r.Pending.Title)
	fmt.Printf("  决定      %s（%s）\n", r.Decision.Verdict(), r.Decision.By)
	fmt.Printf("  落库      %s\n", r.Timeline)
	fmt.Printf("  审计      %s\n", r.Audit)
	fmt.Println("  队列      已清（热层里那条已删）")
	fmt.Println("  下一步    grants/ 那个文件由人侧的管理者 bot 写 —— ANC 不写真相源。")
	return 0
}

// approverLine 说明「该谁批」：解出来写岗位，解不出照实说为什么 —— 不留空让人以为只是没填。
func approverLine(who, note string) string {
	who, note = strings.TrimSpace(who), strings.TrimSpace(note)
	switch {
	case who != "" && note != "":
		return who + "（" + note + "）"
	case who != "":
		return who + "（岗位 —— 人名由展示层现算）"
	case note != "":
		return "没解出来：" + note
	}
	return "没解出来"
}

// waitedMark 只画标记：等了多久、有没有超过 --stale。**不拦、不删。**
func waitedMark(enqueued string, now time.Time, stale time.Duration) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(enqueued))
	if err != nil {
		return "  !! 进队时间读不懂"
	}
	waited := now.Sub(t).Round(time.Minute)
	if stale > 0 && now.Sub(t) > stale {
		return fmt.Sprintf("  !! 已等 %s（超过 --stale）", waited)
	}
	return fmt.Sprintf("（已等 %s）", waited)
}

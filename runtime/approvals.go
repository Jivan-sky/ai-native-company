package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/approvals"
	"anc/internal/card"
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
  anc approvals card <vault> <提案 id> [--json] [--frame <帧文件>]
  anc approvals card --dump-frame [--frame <帧文件>]
  anc approvals card-action <vault> --event <事件体文件|-> [--app-id <id>] [--at RFC3339] [--json]

它只做三件（ANC 只做这三件）：
  1 收提案   kind: proposal 的信封入队 —— 其余 kind 不入这个队（它们的去处不是「等人点头」）
  2 送给人   队列带着「该谁批」：域表 who 那一列 —— 岗位，不是人名（人名由展示层现算）
  3 留痕     点头落两笔：timeline 一条 decision + audit 一条 invoke

飞书卡片那两个子命令（落点是**数据**，不是写死的页面）：
  card         出这一条提案的卡片。帧（长什么样）住在外面的 JSON 里，值全是**现算**的：
               谁提的、属于谁、该谁批、等多久、该什么颜色 —— 一个字段都没写进代码。
               改帧不用重编译：--dump-frame 拿走改，--frame 指回来。
  card-action  收一次**按键回程**（飞书 card.action.trigger 那件事），
               用 open_id 定位「谁点的 / 落在哪台 bot / 这个 agent 属于谁」，然后走
               **与 CLI / 看板同一个信号入口**落两笔痕。认不出的人不猜 —— 原样留痕。

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
	case "card":
		return cmdApprovalsCard(args[1:])
	case "card-action":
		return cmdApprovalsCardAction(args[1:])
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

// ---------- card：出卡片 ----------
//
// 帧是**数据**（外面那份 JSON），值是**实时业务**（热层那条 + 真相源 + 钟）。
// 这一条命令只做三件事：取那条提案、把值算出来、按帧渲染。一个业务字段都没进代码。
func cmdApprovalsCard(args []string) int {
	fs := flag.NewFlagSet("approvals card", flag.ContinueOnError)
	h := addHotOpts(fs)
	asJSON := fs.Bool("json", false, "打飞书 interactive 卡片的 JSON（递出去的那一份）")
	framePath := fs.String("frame", "", "帧文件；不给就按 templates/cards/approval.json 找，找不到用出厂帧")
	dump := fs.Bool("dump-frame", false, "只打这一版帧（拿走改的那一份），不打某一条提案")
	flagArgs, pos := splitArgs(args, map[string]bool{"json": true, "dump-frame": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	f, where, err := card.FindFrame("approval", *framePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if *dump {
		b, err := f.Encode()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		fmt.Println("anc approvals card --dump-frame —— 这一版帧（改完用 --frame 指回来，不用重编译）")
		fmt.Printf("  现在用的是  %s\n", where)
		fmt.Printf("  能用的槽位  %s\n", strings.Join(approvals.SlotKeys(), " / "))
		fmt.Println("  槽位写法    {{键}}；值里没有那个键，渲染时那一格会写明「（没这个值：键）」并喊一声")
		os.Stdout.Write(b)
		fmt.Println()
		return 0
	}
	if len(pos) < 2 {
		fmt.Fprintln(os.Stderr, "错误：用法 anc approvals card <vault> <提案 id> [--json] [--frame <帧文件>]")
		return 2
	}
	vault, err := filepath.Abs(pos[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	st, err := h.open()
	if err != nil {
		return hotUnreachable(err)
	}
	defer st.Close()
	p, ok, err := approvals.Find(st, pos[1])
	if err != nil {
		return hotFail(err)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "拒绝：队列里没有 %s —— 要么没提过，要么已经结过账（看 timeline 里 case=%s 的行）\n",
			pos[1], pos[1])
		return 1
	}
	// 真相源加载不上也照渲染：身份那几格会如实说「认不出」，而不是让整张卡片出不来。
	o, oerr := org.Load(vault)
	c, notes := p.Card(f, o, time.Now())
	if *asJSON {
		b, err := c.FeishuJSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		os.Stdout.Write(b)
	} else {
		fmt.Println("anc approvals card —— 一条待批的卡片（收的人看到的就是下面这些）")
		fmt.Printf("  提案      %s\n", p.ID)
		fmt.Printf("  帧        %s\n", where)
		// 空就说清是空的 —— **不借别的命令的文案**（orDash 那句「工具名未知」是行使流水那边的用词）。
		tone, status := strings.TrimSpace(c.Header.Tone), strings.TrimSpace(p.Status)
		if tone == "" {
			tone = "（帧里没给颜色 —— 发出去按平台默认蓝）"
		}
		if status == "" {
			status = "（热层那条没写状态）"
		}
		fmt.Printf("  颜色      %s\n", tone)
		fmt.Printf("  状态      %s\n", status)
		if oerr != nil {
			fmt.Printf("  真相源    没加载上（%v）—— 身份那几格因此是「认不出」\n", oerr)
		}
		for _, n := range notes {
			fmt.Printf("  !! %s：%s\n", n.Where, n.What)
		}
		fmt.Println()
		fmt.Println(c.Text())
		fmt.Println()
		fmt.Println("递出去：--json 打飞书 interactive 卡片的 JSON；帧改这里不动，改 --frame 那份文件。")
	}
	return 0
}

// ---------- card-action：收按键回程 ----------
//
// 一次按键 = 一条**可识别的信号**。这条命令把它接到与 CLI / 看板**同一个信号入口**
// （approvals.New + approvals.Settle），落同一份两笔痕。
//
// 从 open_id 定位「谁点的 / 落在哪台 bot / 这个 agent 属于谁」用 org 那三条入口现查；
// **认不出就不猜**（原样拿 open_id 当 by 留痕）—— 先留痕，判断交给 agent（2026-10-10 口径）。
func cmdApprovalsCardAction(args []string) int {
	fs := flag.NewFlagSet("approvals card-action", flag.ContinueOnError)
	h := addHotOpts(fs)
	event := fs.String("event", "", "事件体文件；- 表示从 stdin 读（必填）")
	appID := fs.String("app-id", "", "这条回调落在哪个 app（网关那一侧知道）；给了就能定位是哪台 bot、它属于谁")
	at := fs.String("at", "", "RFC3339，默认现在")
	asJSON := fs.Bool("json", false, "打 JSON 回执")
	flagArgs, pos := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(pos) < 1 || strings.TrimSpace(*event) == "" {
		fmt.Fprintln(os.Stderr, "错误：用法 anc approvals card-action <vault> --event <事件体文件|-> [--app-id <id>]")
		return 2
	}
	vault, err := filepath.Abs(pos[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	raw, err := readEvent(*event)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	click, err := card.ParseClick(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "拒绝：%v\n", err)
		return 1
	}
	id, sigWord, ok := click.Point()
	if !ok {
		fmt.Fprintf(os.Stderr,
			"拒绝：这条回调没带齐对哪一条、点的什么（要 %s 与 %s 两个键都在）—— 缺一个也不猜\n",
			card.ClickIDKey, card.ClickSignalKey)
		return 1
	}
	sig, ok := approvals.ParseSignal(sigWord)
	if !ok {
		fmt.Fprintf(os.Stderr, "拒绝：认不出的信号 %q —— 只认 approve / reject / hold（不猜）\n", sigWord)
		return 1
	}
	if strings.TrimSpace(click.OpenID) == "" {
		fmt.Fprintln(os.Stderr, "拒绝：事件里没带 open_id —— 一次点不出人的头，等于没点头（不拿占位符顶上去）")
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
	// 真相源加载不上也照收：先留痕（判断交给 agent），定位那几格如实说认不出。
	o, oerr := org.Load(vault)
	by, via := locate(o, click.OpenID, *appID)
	d, err := approvals.New(id, by, sig, "", when)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 2
	}
	d.Via = via
	st, err := h.open()
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
	if *asJSON {
		out := map[string]any{
			"schema": "anc.approvals.card-action/v1", "ok": true,
			"proposal": r.Pending.ID, "signal": string(r.Decision.Signal),
			"by": r.Decision.By, "via": r.Decision.Via, "at": r.At,
			"timeline": r.Timeline, "audit": r.Audit,
			"org_loaded": oerr == nil, "org_error": errText(oerr),
			"note": "真相源没动：grants/ 那个文件由人侧的管理者 bot 写（ANC 只留痕）",
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		return 0
	}
	fmt.Println("anc approvals card-action —— 一次按键回程（与 CLI / 看板同一个信号入口）")
	fmt.Printf("  谁点的    %s\n", r.Decision.By)
	fmt.Printf("  怎么来的  %s\n", r.Decision.Via)
	if oerr != nil {
		fmt.Printf("  真相源    没加载上（%v）—— 上面那两行因此是「认不出」\n", oerr)
	}
	fmt.Printf("  提案      %s\n", r.Pending.ID)
	fmt.Printf("  一句话    %s\n", r.Pending.Title)
	fmt.Printf("  决定      %s\n", r.Decision.Verdict())
	fmt.Printf("  落库      %s\n", r.Timeline)
	fmt.Printf("  审计      %s\n", r.Audit)
	fmt.Println("  队列      已清（热层里那条已删）")
	fmt.Println("  下一步    grants/ 那个文件由人侧的管理者 bot 写 —— ANC 不写真相源。")
	return 0
}

// locate 是「这点头是从哪来的」那一路定位：谁点的（人话）+ 一份判据原话。
//
// 两头各解各的、**互不代偿**（同 anc org who 的口径）：解不出就如实说，不拿另一头顶上。
// 全程只查表，一个 if 都不加（「该不该他批」是授权层的事，判断交给 agent）。
func locate(o *org.Org, openID, appID string) (by, via string) {
	by = strings.TrimSpace(openID)
	via = "飞书卡片"
	if o == nil {
		return by, via + " · 没加载到 org 真相源，认不出这是谁"
	}
	if id, ok := o.ByOpenID(openID); ok {
		// by 取**编号身份**（alice），不取显示名：这个值会一路进审计与 timeline 的
		// actor 与文件名（`audit.ShardName` / `timeline.safeName` 两把尺子还不一样长），
		// 显示名当键会闹出「换一次显示名，历史断成两截」。人话名字放 via 里给人看。
		by = id.Ref
		via += " · 点的人 " + id.Ref
		if n := strings.TrimSpace(id.Name); n != "" && n != id.Ref {
			via += "（" + n + " · " + openID + "）"
		} else {
			via += "（" + openID + "）"
		}
	} else {
		via += " · 点的人认不出（" + openID + " 在 members/ 里没人认领 —— 自报的身份不算）"
	}
	if a := strings.TrimSpace(appID); a != "" {
		if bot, ok := o.ByAppID(a); ok {
			via += " · 落在 " + bot.Ref + "（" + approvals.KindWord(bot.Kind) + "）"
			if bot.Kind == org.KindAgent {
				owner, why := o.Owner(bot)
				if owner != "" {
					via += "，属于 " + owner
				} else {
					via += "，" + why
				}
			}
		} else {
			via += " · app " + a + " 认不出是哪台 bot"
		}
	}
	return by, via
}

// readEvent 读事件体：`-` 从 stdin 读（网关可以拿管道直接喂进来）。
func readEvent(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "-" {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		if err != nil {
			return nil, fmt.Errorf("stdin 读不了：%w", err)
		}
		if len(b) == 0 {
			return nil, errors.New("stdin 是空的 —— 把事件体喂进来（或给 --event <文件>）")
		}
		return b, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("事件体读不到：%w", err)
	}
	return b, nil
}

// errText 把 error 写成回执里那一栏（nil 就是空串）。
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

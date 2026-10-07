package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/timeline"
)

const timelineUsage = `anc timeline —— 决策与执行的留存记录（append-only 行文本）

用法：
  anc timeline add <vault> -by <谁> -title <一句话> [选项]
  anc timeline list <vault> [--limit N] [--before <RFC3339>] [--band <档>] [--status <词>] [--json]

add 选项：
  -by <谁>         谁记的（必填；**也决定写进哪个文件** —— 按作者分片，见下）
  -title <一句话>  这一条讲什么（必填）
  -status <词>     当前态：done(绿) / running(黄) / blocked·failed(红)。**认不出的词照收**，只画灰
  -kind <词>       proposal / decision / execution / note —— 这是**约定不是白名单**，写别的也收
  -case <id>       归属的 case；空 = 这一条自己就是一个 case
  -to <谁>         该交给谁 —— 下一手是谁的决定（看板就靠这个回答「卡住找谁」）
  -role <岗位>  -domain <业务域>  -project <项目>
  -detail <展开>   -refs <a,b>      -at <RFC3339>（默认现在）      -id <id>（默认按时间自动生成）

口径（三条，都刻意留了「以后不用重构」的余地）：
  · 一个 case 多行 = 一次推进。折叠取**最后一条**当当前态，**历史一行不删**。
  · 真相源是 <vault>/timeline/<YYYY-MM>.<作者>.jsonl —— 按作者分片，两个 agent 同时写也碰不到同一个文件。
  · **写这个命令不设门禁**：谁能写归授权层（议题 #32–#35）。这里不假装拦得住，
    也不把「agent 只能提案」写成代码里的死规矩 —— 那是约定（-kind proposal），不是门禁。
`

func cmdTimeline(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, timelineUsage)
		return 2
	}
	switch args[0] {
	case "add":
		return cmdTimelineAdd(args[1:])
	case "list":
		return cmdTimelineList(args[1:])
	}
	fmt.Fprintf(os.Stderr, "错误：不认识子命令 %q\n\n", args[0])
	fmt.Fprint(os.Stderr, timelineUsage)
	return 2
}

func cmdTimelineAdd(args []string) int {
	fs := flag.NewFlagSet("timeline add", flag.ContinueOnError)
	id := fs.String("id", "", "这一条的 id（默认按时间自动生成）")
	caseID := fs.String("case", "", "归属的 case；空 = 自己一条")
	at := fs.String("at", "", "RFC3339，默认现在")
	kind := fs.String("kind", "", "proposal / decision / execution / note")
	status := fs.String("status", "", "done / running / blocked / failed（认不出的词照收）")
	by := fs.String("by", "", "谁记的（必填）")
	role := fs.String("role", "", "岗位")
	to := fs.String("to", "", "该交给谁")
	domain := fs.String("domain", "", "业务域")
	project := fs.String("project", "", "项目")
	title := fs.String("title", "", "一句话（必填）")
	detail := fs.String("detail", "", "展开")
	refs := fs.String("refs", "", "引用，逗号分隔")
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) == 0 {
		fmt.Fprintln(os.Stderr, "错误：缺少 <vault>")
		return 2
	}
	abs, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	stamp := time.Now().Format(time.RFC3339)
	if strings.TrimSpace(*at) != "" {
		stamp = strings.TrimSpace(*at)
	}
	ent := timeline.Entry{
		ID:      strings.TrimSpace(*id),
		Case:    strings.TrimSpace(*caseID),
		At:      stamp,
		Kind:    strings.TrimSpace(*kind),
		Status:  strings.TrimSpace(*status),
		By:      strings.TrimSpace(*by),
		Role:    strings.TrimSpace(*role),
		To:      strings.TrimSpace(*to),
		Domain:  strings.TrimSpace(*domain),
		Project: strings.TrimSpace(*project),
		Title:   strings.TrimSpace(*title),
		Detail:  strings.TrimSpace(*detail),
	}
	for _, r := range strings.Split(*refs, ",") {
		if s := strings.TrimSpace(r); s != "" {
			ent.Refs = append(ent.Refs, s)
		}
	}
	if ent.ID == "" {
		ent.ID = autoID(ent)
	}
	path, err := timeline.Append(abs, ent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	band := timeline.Band(ent.Status)
	fmt.Println("anc timeline add —— 记下这一条（append-only，不改旧行）")
	fmt.Printf("  vault    %s\n", abs)
	fmt.Printf("  记到     %s\n", relTo(abs, path))
	fmt.Printf("  %s %s\n", bandDot(band), titleLine(ent))
	fmt.Printf("  这一条   id=%s kind=%s status=%s by=%s\n",
		ent.ID, dashIfEmpty(ent.Kind), dashIfEmpty(ent.Status), dashIfEmpty(ent.By))
	if ent.Case != "" {
		fmt.Printf("  归到     case=%s（同一 case 的当前态 = 最后一条）\n", ent.Case)
	} else {
		fmt.Printf("  归到     自己一条（没写 -case）\n")
	}
	return 0
}

func cmdTimelineList(args []string) int {
	fs := flag.NewFlagSet("timeline list", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "最多几个 case（0 = 不限）")
	before := fs.String("before", "", "只看最后活动早于这个时刻的（RFC3339）—— 分页游标")
	band := fs.String("band", "", "按档过滤 green|yellow|red|unknown")
	status := fs.String("status", "", "按 status 原词过滤")
	asJSON := fs.Bool("json", false, "机器可读")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) == 0 {
		fmt.Fprintln(os.Stderr, "错误：缺少 <vault>")
		return 2
	}
	abs, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	doc, err := timeline.Load(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：读不动 timeline 目录：%v\n", err)
		return 1
	}
	all := timeline.Fold(doc.Entries)
	counts := timeline.Tally(all)

	cs := all
	if s := strings.TrimSpace(*status); s != "" {
		cs = filterCases(cs, func(c timeline.Case) bool { return c.Status == s })
	}
	if b := strings.TrimSpace(*band); b != "" {
		cs = filterCases(cs, func(c timeline.Case) bool { return c.Band == b })
	}
	if bs := strings.TrimSpace(*before); bs != "" {
		cut, perr := time.Parse(time.RFC3339, bs)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "错误：--before 需为 RFC3339，收到 %s\n", bs)
			return 2
		}
		cs = filterCases(cs, func(c timeline.Case) bool {
			t, ok := parseAt(c.LastAt)
			return ok && t.Before(cut)
		})
	}
	shown := cs
	if *limit > 0 && len(shown) > *limit {
		shown = shown[:*limit]
	}
	next := ""
	if len(shown) > 0 && len(shown) < len(cs) {
		next = shown[len(shown)-1].LastAt
	}

	if *asJSON {
		out := map[string]any{
			"schema": "anc.timeline/v1",
			"counts": counts,
			"total":  len(all),
			"cases":  shown,
			"bad":    orEmpty(doc.Bad),
		}
		if next != "" {
			out["next_before"] = next
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}

	fmt.Println("anc timeline —— 决策与执行的留存记录（只读）")
	fmt.Printf("  vault    %s\n", abs)
	fmt.Printf("  记录     %d 条 / %d 个 case\n", doc.Len(), len(all))
	fmt.Printf("  档位     🟢 %d 完成 · 🟡 %d 运行中 · 🔴 %d 卡住或失败", counts.Green, counts.Yellow, counts.Red)
	if counts.Unknown > 0 {
		fmt.Printf(" · ⚪ %d 认不出的档", counts.Unknown)
	}
	fmt.Println()
	fmt.Println()
	for _, c := range shown {
		fmt.Printf("  %s %s\n", bandDot(c.Band), c.Title)
		line := fmt.Sprintf("       %s · %s", dashIfEmpty(c.Status), ageLine(c.LastAt))
		if c.To != "" {
			line += " · 交给 " + c.To
		}
		if c.Project != "" {
			line += " · 项目 " + c.Project
		}
		if c.Domain != "" {
			line += " · 域 " + c.Domain
		}
		line += fmt.Sprintf(" · 共 %d 条", c.Entries)
		fmt.Println(line)
		fmt.Printf("       case %s\n", c.Key)
	}
	if len(shown) == 0 {
		fmt.Println("  还没有任何记录 —— 记第一条：")
		fmt.Println("    anc timeline add <vault> -by <谁> -title \"…\" -status running")
	}
	fmt.Println()
	if next != "" {
		fmt.Printf("  下一页   --before %s\n", next)
	}
	if len(doc.Bad) > 0 {
		fmt.Printf("  ⚠️  读不懂的行 %d 条：%s\n", len(doc.Bad), strings.Join(doc.Bad, "、"))
		fmt.Println("       （时间线是拿来做判断的，少一行就可能把「卡住」看成「没事」—— 去看一眼）")
	}
	fmt.Println()
	fmt.Println("口径：三档只按 status 配色（done=绿 / running=黄 / blocked·failed=红）；")
	fmt.Println("      认不出的词**原样保留**、画灰 —— 以后加词只改数据，不用改代码。")
	if len(doc.Bad) > 0 {
		return 1
	}
	return 0
}

// ---------- 小工具 ----------

func autoID(e timeline.Entry) string {
	t, err := time.Parse(time.RFC3339, e.At)
	if err != nil {
		t = time.Now()
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(e.By + "|" + e.Title + "|" + e.At))
	return fmt.Sprintf("t-%s-%04x", t.Format("20060102-150405"), h.Sum32()&0xffff)
}

func parseAt(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

func bandDot(band string) string {
	switch band {
	case timeline.BandGreen:
		return "🟢"
	case timeline.BandYellow:
		return "🟡"
	case timeline.BandRed:
		return "🔴"
	}
	return "⚪"
}

func ageLine(at string) string {
	t, ok := parseAt(at)
	if !ok {
		return "时间读不懂（" + at + "）"
	}
	d := time.Since(t)
	if d < 0 {
		// 未来时间：多半是时钟不对，或手写 -at 写错了。别显示成「-24m 前」那种像 bug 的东西。
		return "时间在未来（还有 " + shortDur(-d) + "）—— 先对一下时钟"
	}
	return "最后动于 " + shortDur(d) + " 前（" + t.Local().Format("2006-01-02 15:04") + "）"
}

func titleLine(e timeline.Entry) string {
	if e.Case != "" {
		return e.Case + " · " + e.Title
	}
	return e.Title
}

func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func orEmpty(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func filterCases(in []timeline.Case, keep func(timeline.Case) bool) []timeline.Case {
	out := make([]timeline.Case, 0, len(in))
	for _, c := range in {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

// relTo 把绝对路径压成「相对 vault」的样子 —— 回显给人看时别喷一整条本机路径。
func relTo(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

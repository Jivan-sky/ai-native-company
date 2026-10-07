package board

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"anc/internal/timeline"
)

// TimelineSchema 是这一份视图的版本号（同 /api/assets 的理由：字段增删一律改版本号，
// 前端据此判断能不能吃）。
const TimelineSchema = "anc.timeline/v1"

// 每页默认 / 上限。上限不是门禁，是防「?limit=100000」把一次请求变成全库扫描。
const (
	timelineDefaultLimit = 20
	timelineMaxLimit     = 200
)

// TimelineEntryView 是流水里的一条 + 它的**配色档**。
//
// 档位是**派生**的（按 status 算），所以它不住在真相源结构里：`timeline.Entry` 只放数据，
// 「这个词画什么颜色」是视图层的事。词表仍然只有一处说了算 —— `timeline.Band`。
type TimelineEntryView struct {
	timeline.Entry
	Band string `json:"band"`
}

// TimelineCaseView = Case，但 Recent 换成带档位的形状。
// 外层字段比内嵌的同名字段浅一层，JSON 里以这个 Recent 为准（Go 的字段提升规则）。
type TimelineCaseView struct {
	timeline.Case
	Recent []TimelineEntryView `json:"recent"`
}

// TimelineView 是 `/api/timeline` 的形状 —— 「卡在哪、谁在跟、下一步是谁的决定」。
//
// 先说清它**不是**什么：
//   - 不是审计。谁**尝试**做了什么（含越权尝试）属于事件面，见 Missing。
//   - 不是从会话里推出来的。这些记录是**人 / agent 自己写下来的**（`anc timeline add`）；
//     看板只折叠与呈现，不替谁总结，也不编一条。
//
// 只出**相对信息**：坏行只报 `文件名:行号`，不带本机路径（看板是观测面）。
type TimelineView struct {
	Schema  string             `json:"schema"`
	Wired   bool               `json:"wired"`
	Counts  timeline.Counts    `json:"counts"`
	Total   int                `json:"total"` // 折叠后的 case 总数（不受 limit 影响）
	Entries int                `json:"entries"`
	Cases   []TimelineCaseView `json:"cases"`
	Bad     []string           `json:"bad"`
	Limit   int                `json:"limit"`
	Next    string             `json:"next_before,omitempty"`
	Missing []string           `json:"missing"`
	Note    string             `json:"note"`
	Error   string             `json:"error,omitempty"`
}

// handleTimeline 读 `timeline/*.jsonl` 并折叠成 case。
//
// 目录不存在 = 还没人记过 = 空时间线，**不是错**（刚 init 的机器不该红）——
// 和 assets 同一条纪律：看板要能显示「还没开始」这个真实状态。
func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	view := TimelineView{
		Schema: TimelineSchema,
		Cases:  []TimelineCaseView{},
		Bad:    []string{},
		Limit:  timelineDefaultLimit,
		Missing: []string{
			"审计：谁尝试做了什么、有没有越权尝试 —— SPEC §6 要求「必须记尝试」，事件面还没实现",
			"谁能写：现在写入口是 `anc timeline add`，不设门禁（谁能写归授权层，议题 #32–#35）",
		},
		Note: "记录是人和 agent 自己写下来的，不是从会话里推出来的。三档只按 status 配色" +
			"（done=绿 / running=黄 / blocked·failed=红）；认不出的词原样保留、画灰 —— " +
			"以后加词只改数据，不用改代码。一个 case 多行 = 一次推进，看板取最后一条当当前态，历史一行不删。",
	}

	q := r.URL.Query()
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			view.Error = "limit 要是个正整数"
			writeJSON(w, http.StatusOK, view)
			return
		}
		if n > timelineMaxLimit {
			n = timelineMaxLimit
		}
		view.Limit = n
	}
	var cut time.Time
	if v := strings.TrimSpace(q.Get("before")); v != "" {
		t, ok := parseBefore(v)
		if !ok {
			view.Error = "before 要是 RFC3339（例如 2026-10-08T02:41:00+08:00）"
			writeJSON(w, http.StatusOK, view)
			return
		}
		cut = t
	}

	doc, err := timeline.Load(s.Vault)
	if err != nil {
		view.Error = "读不动 timeline 目录：" + s.scrub(firstLine(err.Error()))
		writeJSON(w, http.StatusOK, view)
		return
	}
	view.Wired = true
	view.Entries = doc.Len()
	view.Bad = orEmptyStrings(doc.Bad)

	all := timeline.Fold(doc.Entries)
	view.Counts = timeline.Tally(all)

	cases := all
	if v := strings.TrimSpace(q.Get("band")); v != "" {
		cases = keepCases(cases, func(c timeline.Case) bool { return c.Band == v })
	}
	if v := strings.TrimSpace(q.Get("status")); v != "" {
		cases = keepCases(cases, func(c timeline.Case) bool { return c.Status == v })
	}
	if !cut.IsZero() {
		cases = keepCases(cases, func(c timeline.Case) bool {
			t, ok := parseRFC3339(c.LastAt)
			return ok && t.Before(cut)
		})
	}
	view.Total = len(cases)

	shown := cases
	if len(shown) > view.Limit {
		shown = shown[:view.Limit]
	}
	if len(shown) > 0 && len(shown) < len(cases) {
		view.Next = shown[len(shown)-1].LastAt
	}
	view.Cases = toCaseViews(shown)
	writeJSON(w, http.StatusOK, view)
}

// toCaseViews 把折叠结果搬进视图：给每一条流水补上配色档。
func toCaseViews(in []timeline.Case) []TimelineCaseView {
	out := make([]TimelineCaseView, 0, len(in))
	for _, c := range in {
		v := TimelineCaseView{Case: c, Recent: make([]TimelineEntryView, 0, len(c.Recent))}
		for _, e := range c.Recent {
			v.Recent = append(v.Recent, TimelineEntryView{Entry: e, Band: timeline.Band(e.Status)})
		}
		out = append(out, v)
	}
	return out
}

// parseBefore 解析分页游标。
//
// 容错一次的理由：游标是 RFC3339，里面有 `+`（时区），而 **query 串里的 `+` 会被解成空格** ——
// 照抄 /api/timeline 回显的 next_before 去请求，会得到一个明明是对的却报「不是 RFC3339」的值。
// 与其让人对着一个正确的时间戳怀疑人生，不如这里认一次。
func parseBefore(v string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	if i := strings.IndexByte(v, ' '); i > 0 {
		if t, err := time.Parse(time.RFC3339, v[:i]+"+"+v[i+1:]); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseRFC3339(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

func keepCases(in []timeline.Case, keep func(timeline.Case) bool) []timeline.Case {
	out := make([]timeline.Case, 0, len(in))
	for _, c := range in {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

func orEmptyStrings(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

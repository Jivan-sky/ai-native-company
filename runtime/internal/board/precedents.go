package board

// 先例查询（判据 14 的缺口 + 15' 的第四个根因）：
// **「同类事上次怎么收场」必须查得到**，否则一线 agent 只能凭一句话判断。
//
// 三条纪律：
//  1 只返回**人/agent 自己记下来的决策**（kind=decision），不返回推测
//  2 不做相似度打分、不排"最相关" —— 那是判断，交回给读的 agent
//     （判据 15 的教训：中枢不替业务排序）
//  3 空结果就说"没查到先例"，**不拿相近的凑数**

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"anc/internal/timeline"
)

const precedentSchema = "anc.precedents/v1"

// Precedent 是一条历史决策。
type Precedent struct {
	ID      string   `json:"id"`
	Case    string   `json:"case,omitempty"` // 对应的提案 id
	At      string   `json:"at"`
	By      string   `json:"by,omitempty"`
	To      string   `json:"to,omitempty"`
	Verdict string   `json:"verdict,omitempty"` // 从标题里取「同意/驳回/挂起」；取不出就空着
	Title   string   `json:"title"`
	Detail  string   `json:"detail,omitempty"`
	Domain  string   `json:"domain,omitempty"`
	Refs    []string `json:"refs,omitempty"`
}

// PrecedentView 是对外的形状。
type PrecedentView struct {
	Schema  string      `json:"schema"`
	Domain  string      `json:"domain,omitempty"`
	Q       string      `json:"q,omitempty"`
	Total   int         `json:"total"`
	Entries []Precedent `json:"entries"`
	Note    string      `json:"note"`
	Missing []string    `json:"missing,omitempty"`
}

// verdictOf 从标题尾部取结论词。取不出就空着 —— 不猜。
func verdictOf(title string) string {
	for _, w := range []string{"同意", "驳回", "挂起"} {
		if strings.Contains(title, w) {
			return w
		}
	}
	return ""
}

// precedentsByDomain 把时间线里的决策按域分组，每组最多 max 条、新的在前。
//
// 给待批条目内嵌先例用：**一次请求就能拿到"这条 + 它所在域的先例"**，
// 不用 agent 再发第二个请求。仍然不做相似度排序 —— 分组的键只有"同域"这一个事实。
func (s *Server) precedentsByDomain(doc timeline.Doc, max int) map[string][]Precedent {
	out := map[string][]Precedent{}
	if max <= 0 {
		max = 3
	}
	for _, e := range doc.Entries {
		if !strings.EqualFold(strings.TrimSpace(e.Kind), "decision") {
			continue
		}
		d := strings.TrimSpace(e.Domain)
		if d == "" {
			continue
		}
		out[d] = append(out[d], Precedent{ID: e.ID, Case: e.Case, At: e.At, By: e.By, To: e.To,
			Verdict: verdictOf(e.Title), Title: e.Title, Detail: e.Detail, Domain: e.Domain, Refs: e.Refs})
	}
	for d := range out {
		list := out[d]
		sort.SliceStable(list, func(i, j int) bool { return list[i].At > list[j].At })
		if len(list) > max {
			list = list[:max]
		}
		out[d] = list
	}
	return out
}

// handlePrecedents 查先例：按域过滤 + 可选关键词。
//
// 为什么参数这么少：调用方（一线 agent）手上只有"这条提案的域和一句话"，
// 让它自己构造复杂查询不现实。**给最简单的入口，把判断留给它。**
func (s *Server) handlePrecedents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	domain := strings.TrimSpace(q.Get("domain"))
	kw := strings.ToLower(strings.TrimSpace(q.Get("q")))
	limit := 20
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	view := PrecedentView{Schema: precedentSchema, Domain: domain, Q: kw, Entries: []Precedent{},
		Note: "只有人和 agent 自己记下来的决策（kind=decision）。不做相似度排序 —— 哪条算同类，由读的人判断。"}
	doc, err := timeline.Load(s.Vault)
	if err != nil {
		view.Missing = append(view.Missing, "时间线读不动："+s.scrub(firstLine(err.Error())))
		writeJSON(w, http.StatusOK, view)
		return
	}

	var out []Precedent
	for _, e := range doc.Entries {
		if !strings.EqualFold(strings.TrimSpace(e.Kind), "decision") {
			continue
		}
		if domain != "" && !strings.EqualFold(strings.TrimSpace(e.Domain), domain) {
			continue
		}
		if kw != "" {
			hay := strings.ToLower(e.Title + " " + e.Detail)
			if !strings.Contains(hay, kw) {
				continue
			}
		}
		out = append(out, Precedent{ID: e.ID, Case: e.Case, At: e.At, By: e.By, To: e.To,
			Verdict: verdictOf(e.Title), Title: e.Title, Detail: e.Detail,
			Domain: e.Domain, Refs: e.Refs})
	}
	// 新的排前面：先例的价值在于"最近的怎么收场"
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	view.Total = len(out)
	if len(out) > limit {
		out = out[:limit]
	}
	view.Entries = out
	if len(out) == 0 {
		view.Missing = append(view.Missing, "没有查到先例 —— 这可能是第一次办这类事，也可能只是没人在时间线里记过")
	}
	writeJSON(w, http.StatusOK, view)
}

package board

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"anc/internal/audit"
	"anc/internal/org"
)

// AuditSchema 是这一份视图的版本号（同 /api/timeline 的理由：字段增删一律改版本号）。
const AuditSchema = "anc.audit/v1"

// 每页默认 / 上限。上限不是门禁，是防「?limit=100000」把一次请求变成全库扫描。
const (
	auditDefaultLimit = 50
	auditMaxLimit     = 500
)

// AuditView 是 `/api/audit` 的形状 —— 「谁在什么时候、对谁、行使了什么（含被拒的）」。
//
// 先说清它**不是**什么：
//   - 不是时间线。那一页是**人 / agent 自己写下来的**留存；这一页是**系统记下来的**流水。
//     互补，不重叠 —— 人会漏记，机器会漏判，两个都要有。
//   - 不是「全部行使」。**「本可以行使但没行使」不可能自动归集**（它没发生）；
//     只有人补记的那一类才有。这条写进 Missing，别让读的人以为它是全量。
//   - 不含本机布局：`object` 在这里是**归一过的**（`<vault>/…` 或 `…/<末两段>`），
//     不是流水里的绝对路径 —— 看板是观测面（SPEC §2.3）。
type AuditView struct {
	Schema  string            `json:"schema"`
	Wired   bool              `json:"wired"`
	Bands   audit.Bands       `json:"bands"`
	Scopes  audit.Scopes      `json:"scopes"`
	Total   int               `json:"total"`
	Records []audit.EntryView `json:"records"`
	Bad     []string          `json:"bad"`
	Limit   int               `json:"limit"`
	Missing []string          `json:"missing"`
	Note    string            `json:"note"`
	Error   string            `json:"error,omitempty"`
}

// handleAudit 读 `audit/*.jsonl`，按**当前**域表算「向谁 / 跨不跨域」。
//
// 目录不存在 = 还没有任何行使记录 = 空流水，**不是错**（刚 init 的机器不该红）。
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	view := AuditView{
		Schema:  AuditSchema,
		Records: []audit.EntryView{},
		Bad:     []string{},
		Limit:   auditDefaultLimit,
		Missing: []string{
			"目标抽不出的行使（Bash 这类自由文本命令）：「对谁」这一栏永远是空的，按「判不出」显示 —— 不是「没发生」",
			"被拦下的动作：只有 harness 自己的记录里有，才归集得到；harness 没记的那一段不会自动出现",
			"本可以行使但没行使：**不可能自动归集**（它没发生）。只有人 / agent 补记的那一类才有（`anc audit add`）",
			"谁有权读这份流水：归授权层（议题 #32 域隔离 / #34 授权链）。本页不自己发明一套读权限",
			"留存期与淘汰：归议题 #27 / #25，三处是同一件事，不在这里单独定",
		},
		Note: "这是**系统记下来的**行使流水（与「时间线」页互补：那一页是人 / agent 自己写下来的）。" +
			"事实存的是当时的样子（目标是原样路径）；「这在哪个域」「算不算跨域」是按**当前**域表算的 —— " +
			"域表改了，解读跟着改，证据不变。只记不拦：审计判错了，代价应该只是一行话。",
	}

	q := r.URL.Query()
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			view.Error = "limit 要是个正整数"
			writeJSON(w, http.StatusOK, view)
			return
		}
		if n > auditMaxLimit {
			n = auditMaxLimit
		}
		view.Limit = n
	}

	doc, err := audit.Load(s.Vault)
	if err != nil {
		view.Error = "读不动 audit 目录：" + s.scrub(firstLine(err.Error()))
		writeJSON(w, http.StatusOK, view)
		return
	}
	// org 红档**不拦**：归集与读流水都不该被真相源的格式问题挡住（那件事由 /api/issues 报）。
	// 拿不到域表时一切落「未归属」，页面上如实说 —— 不编一个域出来。
	o, _ := org.Load(s.Vault)
	sc := audit.NewScope(s.Vault, o)
	views := audit.Views(doc.Records, sc)
	view.Wired = true
	// 四档与落点给的是**全量**（顶部那排卡片回答「整体什么样」）；
	// Total / Records 是**筛选后**的（下面那张表回答「我要看的那几条」）——
	// 与 /api/timeline 同一口径，别把两者混起来。
	view.Bands = audit.Tally(doc.Records)
	view.Scopes = audit.TallyScopes(views)
	view.Bad = orEmptyStrings(doc.Bad)

	if v := strings.TrimSpace(q.Get("result")); v != "" {
		band := strings.ToLower(v)
		views = keepViews(views, func(e audit.EntryView) bool { return e.Band == band })
	}
	if v := strings.TrimSpace(q.Get("actor")); v != "" {
		views = keepViews(views, func(e audit.EntryView) bool { return e.Actor == v })
	}
	if q.Get("cross") != "" {
		views = keepViews(views, func(e audit.EntryView) bool { return e.Known && e.Cross })
	}
	view.Total = len(views)

	// 最新的在上；截断前先把 object 归一、自由文本丢掉（看板不摊本机布局）。
	for i := range views {
		views[i].Object = s.relTo(views[i].Object)
		// `why` / `detail` 是**自由文本**：工具结果原话与命令行，里面必然夹着本机绝对路径
		// （实测：`File does not exist. … your current working directory is D:\…`）。
		// 看板是观测面（SPEC §2.3「不泄漏本机布局」），所以这里**不往看板带** ——
		// 原话留在流水里，要看用 `anc audit log`。只留一个「有没有原话」的标记。
		// scrub 自由文本永远做不干净；做不干净的安全措施比不做更糟 —— 它会给人「已安全」的错觉。
		views[i].Why = ""
		views[i].Detail = ""
	}
	reverseViews(views)
	if len(views) > view.Limit {
		views = views[:view.Limit]
	}
	view.Records = views
	writeJSON(w, http.StatusOK, view)
}

// relTo 把审计里的绝对路径归一成**不泄露本机布局**的写法：
// vault 内的 → `<vault>/…`；vault 外的 → `…/<末两段>`（保留「这是谁的文件」，
// 但不摊出盘符与用户名）。看板的纪律是「只出相对信息」（SPEC §2.3）。
func (s *Server) relTo(object string) string {
	o := strings.TrimSpace(object)
	if o == "" {
		return ""
	}
	slash := filepath.ToSlash(o)
	root := filepath.ToSlash(strings.TrimSpace(s.Vault))
	if root != "" {
		if slash == root {
			return "<vault>"
		}
		if strings.HasPrefix(slash, root+"/") {
			return "<vault>" + strings.TrimPrefix(slash, root)
		}
	}
	// 先丢掉纯盘符段（`D:`）—— 留着会拼出 `…/D:/foo` 这种既暴露盘符又不好看的写法。
	parts := make([]string, 0, 4)
	for _, seg := range strings.Split(slash, "/") {
		if seg == "" || (len(seg) == 2 && seg[1] == ':') {
			continue
		}
		parts = append(parts, seg)
	}
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	if len(parts) == 0 {
		return ""
	}
	return "…/" + strings.Join(parts, "/")
}

func keepViews(in []audit.EntryView, keep func(audit.EntryView) bool) []audit.EntryView {
	out := make([]audit.EntryView, 0, len(in))
	for _, e := range in {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

// reverseViews 原地上翻（最新的在上）。
func reverseViews(vs []audit.EntryView) {
	for i, j := 0, len(vs)-1; i < j; i, j = i+1, j-1 {
		vs[i], vs[j] = vs[j], vs[i]
	}
}

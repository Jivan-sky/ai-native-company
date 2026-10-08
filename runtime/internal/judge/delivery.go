package judge

import (
	"fmt"
	"sort"
	"strings"

	"anc/internal/org"
	"anc/internal/timeline"
)

// 交付作用域（delivery）—— 判据作用在「客户交付链路」这一层。
//
// 为什么要第二个作用域：会话作用域的事实来自 harness 的记录（turns / denied / failed…），
// 而 FDE 那几道门的证据**不在会话里，在 vault 里** —— 一线有没有当场说「这能帮到我」、
// 停止条件有没有双方认可、产能有没有扩，这些是**人记下来的**，落在 timeline/。
//
// 这一层的事实只有两个来源，都是真相源、都不是判断：
//
//	org      —— 人 / 岗 / 域 / 项目 / 授权（结构上的「有没有」）
//	timeline —— 留痕（一道门推进到哪、卡在哪、谁拍的板）
//
// 口径与 judge 是一套：判据是数据、只出结论不设门禁、每条结论都带来源与证据。

// GateCasePrefix 是「一道门」在 timeline 里的 case 前缀。
//
// 引擎不认识任何一道具体的门 —— 门的名单与判据都在**数据**里（BuiltinDelivery 是一份起点，
// 客户现场该改的正是它）。前缀只负责把门和别的 case 区分开，避免各处在字符串上手拼。
const GateCasePrefix = "gate:"

// GateCase 把门的 slug 拼成 timeline 的 case 名。
func GateCase(slug string) string { return GateCasePrefix + slug }

// deliveryGlobalMetrics 是交付层的**全局计数**指标（不带 case）。
var deliveryGlobalMetrics = map[string]bool{
	"records":  true, // timeline 里的留痕条数
	"cases":    true, // timeline 里出现过的 case 数
	"gates":    true, // 有留痕的门数
	"projects": true, // projects.md 的行数
	"domains":  true, // domains.md 的行数
	"charters": true, // charters/ 的立项书副本数
	"grants":   true, // grants/ 的授权条数
	"members":  true, // 启用中的成员数
}

// deliveryCaseMetrics 是**按某一道门统计**的指标（必须配 case）。
var deliveryCaseMetrics = map[string]bool{
	"evidence": true, // 这道门落了几条留痕（0 = 一道门的证据都没落痕）
	"done":     true, // 其中绿档（已完成 / 通过）
	"stuck":    true, // 其中红档（失败，或有卡点需要介入）
	"running":  true, // 其中黄档（运行中）
}

func isPerCaseMetric(m string) bool { return deliveryCaseMetrics[m] }

// String 把一条条件写成回显用的一行。带 case 的要把门名写出来 ——
// 不然 {evidence} 这类按门统计的指标看起来像是全局的。
func (c Cond) String() string {
	s := fmt.Sprintf("%s %s %d", c.Metric, c.Op, c.Value)
	if c.Case != "" {
		s += "（case " + c.Case + "）"
	}
	return s
}

// checkMetric 按**作用域**校验一个条件。写错作用域的指标永远不会命中，
// 而一条永远不命中的判据比一条写错的判据更难发现 —— 所以这里是拒，不是忽略。
func checkMetric(scope Scope, c Cond) error {
	if deliveryGlobalMetrics[c.Metric] {
		if scope != ScopeDelivery {
			return fmt.Errorf("指标 %q 只属于 delivery（交付层）作用域，这条判据是 %s", c.Metric, scope)
		}
		if c.Case != "" {
			return fmt.Errorf("指标 %q 是全局计数，不该配 case", c.Metric)
		}
		return nil
	}
	if isPerCaseMetric(c.Metric) {
		if scope != ScopeDelivery {
			return fmt.Errorf("指标 %q 只属于 delivery（交付层）作用域，这条判据是 %s", c.Metric, scope)
		}
		if strings.TrimSpace(c.Case) == "" {
			return fmt.Errorf("指标 %q 是按 case 统计的，必须配 case（例：case=%s）", c.Metric, GateCase("<门的 slug>"))
		}
		return nil
	}
	if scope == ScopeDelivery {
		return fmt.Errorf("delivery 作用域只认交付层指标（全局：%s；按门：%s），拿到 %q",
			deliveryMetricList(deliveryGlobalMetrics), deliveryMetricList(deliveryCaseMetrics), c.Metric)
	}
	if !sessionMetricNames[c.Metric] {
		return fmt.Errorf("指标 %q 不存在（指标必须来自事实层；写错的判据永远不会命中）", c.Metric)
	}
	if c.Case != "" {
		return fmt.Errorf("case 只在 delivery 作用域里有意义，%s 作用域的判据不该写它", scope)
	}
	return nil
}

func deliveryMetricList(m map[string]bool) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, " / ")
}

// DeliveryFacts 是交付层的事实：全局计数 + 按 case 分组的留痕三档计数。
//
// 三档用的是 timeline 自己的配色口径（绿 = 已完成，红 = 失败或有卡点，黄 = 运行中，灰 = 认不出的词），
// 所以「一道门过没过」和看板上那一行说的是同一件事，不会互相打脸。
type DeliveryFacts struct {
	Metrics map[string]int       // deliveryGlobalMetrics 那些键
	ByCase  map[string]CaseFacts // case 名 → 该 case 的三档计数
	Records []timeline.Entry     // 原始留痕（给结论做证据指针用）
	Bad     []string             // timeline 里读不懂的行（回显，不吞）
}

// CaseFacts 是某一个 case 的三档计数。
type CaseFacts struct {
	Evidence int
	Done     int
	Stuck    int
	Running  int
	Unknown  int
}

// VaultFacts 从真相源算交付层的事实。**只做计数与归类，不做判断** ——
// 判断全部在判据表里，这样换一套判法不用动这里。
func VaultFacts(o *org.Org, doc timeline.Doc) DeliveryFacts {
	f := DeliveryFacts{
		Metrics: map[string]int{},
		ByCase:  map[string]CaseFacts{},
		Records: doc.Entries,
		Bad:     doc.Bad,
	}
	f.Metrics["records"] = len(doc.Entries)
	seenCase := map[string]bool{}
	seenGate := map[string]bool{}
	for _, e := range doc.Entries {
		key := strings.TrimSpace(e.Case)
		if key == "" {
			key = strings.TrimSpace(e.ID) // 空 case = 这一条自己就是一个 case（timeline 的口径）
		}
		if key == "" {
			continue
		}
		seenCase[key] = true
		if strings.HasPrefix(key, GateCasePrefix) {
			seenGate[key] = true
		}
		cf := f.ByCase[key]
		cf.Evidence++
		switch timeline.Band(e.Status) {
		case timeline.BandGreen:
			cf.Done++
		case timeline.BandRed:
			cf.Stuck++
		case timeline.BandYellow:
			cf.Running++
		default:
			cf.Unknown++
		}
		f.ByCase[key] = cf
	}
	f.Metrics["cases"] = len(seenCase)
	f.Metrics["gates"] = len(seenGate)
	if o != nil {
		f.Metrics["members"] = len(o.Enabled())
		f.Metrics["projects"] = len(o.Projects)
		f.Metrics["domains"] = len(o.Domains)
		f.Metrics["charters"] = len(o.Charters)
		f.Metrics["grants"] = len(o.Grants)
	}
	return f
}

// gates 是出厂五道门。**这是一份起点，不是标准答案** —— 客户现场该改的正是这份数据。
//
// 每一道门都从两份已经写下来的东西里来（不是这里现编的）：
//
//	diagnosis —— 《ANC架构设计大框》§8.2 的 ANC-Diagnosis：全景图 + 用真实资料做的 Demo +
//	             组织/路线建议，结论 GO / HOLD / NO-GO；
//	proof     —— SPEC §8 一阶（证明）的门：一线当场说「这能帮到我」+ 停止条件双方认可；
//	deploy    —— 《大框》§8.2 的 ANC Deployment：把真实工作流正式接入；
//	settle    —— SPEC §8 二阶（沉淀）的门：员工不靠问人就能拿到上下文、权限、工具；
//	rebuild   —— SPEC §8 三阶（重构）的门：在没增加中层的前提下，产能扩了。
var gates = []Gate{
	{"diagnosis", "诊断门", "企业 AI 全景图 + 用真实资料做的 Demo + 组织/路线建议，结论 GO / HOLD / NO-GO"},
	{"proof", "证明门", "一线当场说『这能帮到我』+ 停止条件双方认可"},
	{"deploy", "装机门", "把真实工作流正式接入：老板目标 / 公司资料 / 任务 / 权限 / 责任 / 决策 / 结果都接上"},
	{"settle", "沉淀门", "员工不靠问人就能拿到完成任务所需的上下文、权限、工具"},
	{"rebuild", "重构门", "在没增加中层的前提下，产能扩了"},
}

// Gate 是出厂的一道门。slug 进 timeline 的 case 名（gate:<slug>），门名给人看。
type Gate struct{ Slug, Name, Criterion string }

// BuiltinGates 透出出厂门名单 —— 回显与判据表从**同一处数据**长出来，不各写一遍。
func BuiltinGates() []Gate { return append([]Gate(nil), gates...) }

// BuiltinDelivery 是出厂交付判据：五道门，每道两条 —— 「没落痕」与「有卡点」。
//
// 为什么每道门只出这两条：这两条恰好是**能从留痕本身看出来**的；
// 「一线那句话算不算数」「产能到底扩没扩」要人判 —— 判据只负责把「该判的没落痕」摆到人面前，
// 不替人下结论（同 SPEC §13 Q17：裁判权留给人工）。
func BuiltinDelivery() []Rule {
	names := make([]string, 0, len(gates))
	for _, g := range gates {
		names = append(names, g.Name)
	}
	out := []Rule{{
		ID: "delivery-no-records", Scope: ScopeDelivery, Level: LevelWarn,
		Title: "交付链路上一道门的证据都没落痕",
		Say: fmt.Sprintf("timeline 里有 {records} 条留痕、{cases} 个 case，五道门（%s）哪一道过了、卡在哪、谁拍的板 —— 现在全都没落痕。门不进 timeline，就还是人肉 checklist，而且下次换人接不上。",
			strings.Join(names, " / ")),
		When:         []Cond{{Metric: "records", Op: "==", Value: 0}},
		NoEvidenceOk: true, // 结论本身就是「一条都没有」
	}}
	for _, g := range gates {
		caseName := GateCase(g.Slug)
		out = append(out,
			Rule{
				ID: "delivery-" + g.Slug + "-unrecorded", Scope: ScopeDelivery, Level: LevelWarn,
				Title: g.Name + "没有落痕",
				Say: fmt.Sprintf("%s的判据是「%s」—— case 写 %s 的位置 {evidence} 条留痕，这道门现在只活在人脑里。",
					g.Name, g.Criterion, caseName),
				When:         []Cond{{Metric: "evidence", Op: "==", Value: 0, Case: caseName}},
				NoEvidenceOk: true, // 结论本身就是「没落痕」，要求带证据等于逼人写假证据
			},
			Rule{
				ID: "delivery-" + g.Slug + "-stuck", Scope: ScopeDelivery, Level: LevelWarn,
				Title: g.Name + "上有卡点",
				Say: fmt.Sprintf("%s有 {stuck} 条失败 / 卡点留痕（另有 {running} 条在跑）—— 按纪律：门没过不进下一阶；但也别拿日历凑，门过了就立刻进。",
					g.Name),
				When: []Cond{{Metric: "stuck", Op: ">=", Value: 1, Case: caseName}},
			})
	}
	return out
}

// JudgeDelivery 对交付链路跑一遍判据。
func JudgeDelivery(f DeliveryFacts, rules []Rule) []Finding {
	var out []Finding
	for _, r := range rules {
		if r.Scope != ScopeDelivery {
			continue
		}
		rf, ok := deliveryRuleFacts(f, r.When)
		if !ok || !match(r.When, rf.metrics) {
			continue
		}
		fd := Finding{
			Rule: r.ID, Level: levelOf(r), Scope: r.Scope, Source: "rule",
			Where: "交付链路（vault 全量）",
			Title: r.Title, Say: render(r.Say, rf),
		}
		fd.Evidence = deliveryEvidence(r.When, f, 5)
		if len(fd.Evidence) == 0 && !r.NoEvidenceOk {
			continue
		}
		out = append(out, fd)
	}
	return out
}

// deliveryRuleFacts 把一条判据要的指标摊平成一次求值：全局计数 + 它自己那一扇门的计数。
//
// 能这么摊平，是因为 Validate 拒掉了「一条判据里写两个 case」——
// 一条判据只可能指着一扇门，摊平之后 {stuck} 解成哪一扇门就是确定的。
func deliveryRuleFacts(f DeliveryFacts, conds []Cond) (facts, bool) {
	rf := facts{metrics: map[string]int{}, vars: map[string]string{}}
	for k, v := range f.Metrics {
		rf.metrics[k] = v
	}
	caseName, err := ruleCase(conds)
	if err != nil {
		return rf, false
	}
	if caseName != "" {
		cf := f.ByCase[caseName]
		rf.metrics["evidence"] = cf.Evidence
		rf.metrics["done"] = cf.Done
		rf.metrics["stuck"] = cf.Stuck
		rf.metrics["running"] = cf.Running
		rf.vars["case"] = caseName
	}
	return rf, true
}

// deliveryEvidence 把结论点回留痕原件（这一条判据关心的那一扇门、**那一类**）。
//
// 「只列相关的那类」是证据这条纪律的要点：一条「有卡点」的结论，证据里混进绿档留痕，
// 读的人就没法判断这条结论到底成不成立。关心的档位由判据自己写的指标推出来，不在引擎里写死。
//
// 与事实层的分工照旧：judge 不重新解释发生了什么，只把原件摆出来。
func deliveryEvidence(conds []Cond, f DeliveryFacts, limit int) []string {
	caseName, err := ruleCase(conds)
	if err != nil || caseName == "" {
		return nil
	}
	want := bandsOf(conds)
	var out []string
	var n int
	for _, e := range f.Records {
		if entryCase(e) != caseName {
			continue
		}
		if !want[timeline.Band(e.Status)] {
			continue
		}
		n++
		if len(out) >= limit {
			continue
		}
		status := strings.TrimSpace(e.Status)
		if status == "" {
			status = "未标态"
		}
		at := e.At
		if t, ok := e.Time(); ok {
			at = t.Local().Format("01-02 15:04")
		}
		out = append(out, fmt.Sprintf("%s %s：%s（%s，by %s）", at, caseName, e.Title, status, e.By))
	}
	if n > limit {
		out = append(out, fmt.Sprintf("…另有 %d 条，见 timeline/", n-limit))
	}
	return out
}

// bandsOf 从判据的条件推出它关心哪几档留痕。`evidence` 不挑档（它数的就是「有没有」），
// 其余各挑自己那一档。
func bandsOf(conds []Cond) map[string]bool {
	bands := map[string]bool{}
	for _, c := range conds {
		switch c.Metric {
		case "done":
			bands[timeline.BandGreen] = true
		case "stuck":
			bands[timeline.BandRed] = true
		case "running":
			bands[timeline.BandYellow] = true
		case "evidence":
			for _, b := range []string{timeline.BandGreen, timeline.BandRed, timeline.BandYellow, timeline.BandUnknown} {
				bands[b] = true
			}
		}
	}
	if len(bands) == 0 {
		for _, b := range []string{timeline.BandGreen, timeline.BandRed, timeline.BandYellow, timeline.BandUnknown} {
			bands[b] = true
		}
	}
	return bands
}

// entryCase 取一条留痕的归属 case（空 = 自己就是一条），与 VaultFacts 的口径同一处规则。
func entryCase(e timeline.Entry) string {
	if k := strings.TrimSpace(e.Case); k != "" {
		return k
	}
	return strings.TrimSpace(e.ID)
}

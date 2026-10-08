// Package judge 是留痕的**判断层**：把事实层抽出来的动作与失败，判成人看得懂的一句话。
//
// 与事实层的分工是一条硬边界：
//
//	trail —— 说「发生了什么」，每条都带记录原话，不解释；
//	judge —— 说「这算什么」，且**每条结论都必须带证据指针与来源**。
//
// **判据是数据，不是代码。** 引擎不认识任何一条具体判据：内置一份表，也可以整份换成
// 文件里的（`--rules`）。加一条判据 = 加一行数据，不用改引擎、不用重编译；
// 要换一套完全不同的判法，换的是那份数据。这样它就不是一次「写死」。
//
// **这里没有门禁。** judge 只出结论，不拦任何事、不改退出码、不自动动手。
// 它判错了，代价应该只是「多了一行话」，不是「一件本该发生的事没发生」。
//
// 两个作用域，一个引擎：
//
//	session / turn —— 会话层，事实来自 harness 的记录（`anc trail`）；
//	delivery       —— 交付层，事实来自真相源仓库（`anc gate`，见 delivery.go）。
//
// 新增**事实**（比如「工具调用耗时」）才需要动 fact 层与指标表 —— 这条边界是故意的：
// 判据可以随便长，事实必须来自记录、不能从判断里反推出来。
package judge

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"anc/internal/trail"
)

// Schema 是规则文件与 JSON 出口的版本。
const Schema = "anc.judge/v1"

// Scope 是一条判据作用在哪一层。
//
// session / turn 是**会话层**：事实来自 harness 的记录（turns / denied / failed…）。
// delivery 是**交付层**：事实来自真相源仓库（org 结构 + timeline 留痕）—— 见 delivery.go。
// 两层共用这一个引擎与这一套 schema：判据即数据的机制只有一套。
type Scope string

const (
	ScopeSession  Scope = "session"
	ScopeTurn     Scope = "turn"
	ScopeDelivery Scope = "delivery"
)

// Level 只是标签，不是门禁：warn = 值得看一眼，info = 记着就行。
type Level string

const (
	LevelInfo Level = "info"
	LevelWarn Level = "warn"
)

// KindDenied / KindFailed 与事实层 trail.Failure.Kind 是同一套口径。
const (
	KindDenied = "denied"
	KindFailed = "failed"
)

// Cond 是一条条件：对某个指标求值。全部条件满足（AND）才算命中。
type Cond struct {
	Metric string `json:"metric"`
	Op     string `json:"op"` // >= > == != <= <
	Value  int    `json:"value"`

	// Case 只在 delivery 作用域里有意义：把指标限制在 timeline 的**某一个 case** 上
	// （一道门就是一个 case，见 delivery.go）。引擎不认识任何一道具体的门 —— 门名跟着数据走。
	// 别的作用域写了它会被拒：同样宁可拒绝，也不要让它静默失效。
	Case string `json:"case,omitempty"`
}

// Rule 是一条判据。**整条都是数据** —— 引擎里没有任何 if 认识它。
//
// Say 用占位符取值：`{指标名}` 取数字，`{变量名}` 取人话（工具名单、原因、时间…）。
type Rule struct {
	ID    string `json:"id"`
	Scope Scope  `json:"scope"`
	Level Level  `json:"level"`
	Title string `json:"title"`
	Say   string `json:"say"`
	When  []Cond `json:"when"`

	// NoEvidenceOk 声明「这条判据的结论本身就是『没有证据』」（缺勤类判据：一段账读不到、
	// 一道门没落痕）。不写它，引擎要求结论必须点得回原件 —— 那道闸的理由是
	// 「说了但指不回原件，等于让人去信一句没根据的话」。
	//
	// 它是**数据**，不是引擎里的一个特判：引擎不认识任何一条具体的判据。
	NoEvidenceOk bool `json:"no_evidence_ok,omitempty"`
}

// Finding 是一条结论。Source 与 Evidence 是它和「猜测」的分界线：
// 说不清来源、点不回原件的结论，不该被写出来。
type Finding struct {
	Rule     string   `json:"rule"`
	Level    Level    `json:"level"`
	Scope    Scope    `json:"scope"`
	Where    string   `json:"where"` // 哪段会话、哪一轮
	Title    string   `json:"title"`
	Say      string   `json:"say"`
	Source   string   `json:"source"` // rule（将来会有 model —— 这条字段就是分界线）
	Evidence []string `json:"evidence,omitempty"`
}

// sessionMetricNames 是**会话作用域支持的全部指标**。规则写了不存在的指标会被拒
// （不静默当 0）—— 一条永远不会命中的判据，比一条写错的判据更难发现。
// 交付作用域的指标在 delivery.go，两套分开：写错作用域的指标一样要拒。
var sessionMetricNames = map[string]bool{
	"turns":      true, // 有几轮
	"denied":     true, // 被权限规则挡下的动作数
	"failed":     true, // 真执行失败的动作数
	"failures":   true, // 上面两者之和
	"subagents":  true, // 子任务数
	"unreadable": true, // 1 = 原生记录读不到（这时其它指标都不可信）
}

// varsForTurn / varsForSession 提供给人话用的变量名。规则里写 {工具名单} 这类。
var varNames = map[string]bool{
	"denied_tools": true,
	"failed_whys":  true,
	"failed_tools": true,
	"session":      true,
	"turn_time":    true,
	"prompt":       true,
}

// Builtin 是出厂判据。它**不是**标准答案，是一份起点：客户现场该改的是这份表。
func Builtin() []Rule {
	return []Rule{
		{
			ID: "turn-denied-repeat", Scope: ScopeTurn, Level: LevelWarn,
			Title: "这一轮的动作被权限规则反复挡下",
			Say:   "有 {denied} 个动作被权限规则挡下（{denied_tools}）：不是坏了，是这个动作没被授权。",
			When:  []Cond{{Metric: "denied", Op: ">=", Value: 2}},
		},
		{
			ID: "turn-tool-failed", Scope: ScopeTurn, Level: LevelWarn,
			Title: "这一轮有工具执行失败",
			Say:   "{failed} 个工具调用失败：{failed_whys}",
			When:  []Cond{{Metric: "failed", Op: ">=", Value: 1}},
		},
		{
			ID: "session-denied-heavy", Scope: ScopeSession, Level: LevelInfo,
			Title: "整段会话的权限缺口明显",
			Say:   "全段 {denied} 次动作被权限规则挡下（{denied_tools}）—— 要么补授权，要么确认它本就不该碰。",
			When:  []Cond{{Metric: "denied", Op: ">=", Value: 5}},
		},
		{
			ID: "session-unreadable", Scope: ScopeSession, Level: LevelWarn,
			Title: "这段账读不到",
			Say:   "原生记录没读到，下面任何结论都不成立 —— 先修归集，再判它。",
			When:  []Cond{{Metric: "unreadable", Op: ">=", Value: 1}},
			// 结论本身就是「读不到」：要求它带证据等于逼着写假证据。
			// 这条豁免以前写死在引擎里（`r.ID != "session-unreadable"`），现在回到数据里。
			NoEvidenceOk: true,
		},
	}
}

// Load 从文件读一份判据表（整份替换内置的，不合并 —— 合并会让「现在到底在用哪套」说不清）。
func Load(path string) ([]Rule, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Schema string `json:"schema"`
		Rules  []Rule `json:"rules"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("判据文件 %s 解析不了：%w", path, err)
	}
	if len(f.Rules) == 0 {
		return nil, fmt.Errorf("判据文件 %s 里没有 rules", path)
	}
	for i, r := range f.Rules {
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("判据文件 %s 第 %d 条：%w", path, i+1, err)
		}
	}
	return f.Rules, nil
}

// Validate 检查一条判据写没写对。**宁可拒绝，也不要让它静默失效。**
func (r Rule) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("缺 id")
	}
	switch r.Scope {
	case ScopeSession, ScopeTurn, ScopeDelivery:
	default:
		return fmt.Errorf("scope 只能是 session / turn / delivery，拿到 %q", r.Scope)
	}
	if r.Level == "" {
		r.Level = LevelInfo
	}
	if len(r.When) == 0 {
		return fmt.Errorf("%s：没有条件就该直接写死在事实里，别放在判据表里", r.ID)
	}
	caseName, err := ruleCase(r.When)
	if err != nil {
		return fmt.Errorf("%s：%w", r.ID, err)
	}
	for _, c := range r.When {
		if err := checkMetric(r.Scope, c); err != nil {
			return fmt.Errorf("%s：%w", r.ID, err)
		}
		switch c.Op {
		case ">=", ">", "==", "!=", "<=", "<":
		default:
			return fmt.Errorf("%s：不认识的比较符 %q", r.ID, c.Op)
		}
	}
	for _, ph := range placeholders(r.Say) {
		switch {
		case deliveryCaseMetrics[ph]:
			// 按门统计的指标：这条判据得指着某一扇门，否则 {指标} 解不出来。
			if caseName == "" {
				return fmt.Errorf("%s：say 里的 {%s} 是按 case 统计的指标，这条判据没有写 case", r.ID, ph)
			}
		case sessionMetricNames[ph], deliveryGlobalMetrics[ph], varNames[ph]:
		default:
			return fmt.Errorf("%s：say 里的占位符 {%s} 既不是指标也不是变量（会原样打出去）", r.ID, ph)
		}
	}
	return nil
}

// ruleCase 取一条判据里**唯一**的那个 case（没有则空）。一条判据里出现两个不同的 case
// 就拒：{指标} 这类占位符是按 case 解出来的，两个 case 会让它解成哪一个说不清。
func ruleCase(conds []Cond) (string, error) {
	found := ""
	for _, c := range conds {
		if c.Case == "" {
			continue
		}
		if found != "" && c.Case != found {
			return "", fmt.Errorf("一条判据里出现了两个 case（%q / %q）：{指标} 是按 case 解出来的，解成哪一个说不清", found, c.Case)
		}
		found = c.Case
	}
	return found, nil
}

// Judge 对一段会话跑一遍判据。找不到证据的结论**不发** ——
// 「说了但指不回原件」等于让人去信一个没根据的话。
func Judge(s trail.Session, rules []Rule) []Finding {
	var out []Finding
	sess := sessionFacts(s)
	for _, r := range rules {
		if r.Scope != ScopeSession || !match(r.When, sess.metrics) {
			continue
		}
		f := Finding{
			Rule: r.ID, Level: levelOf(r), Scope: r.Scope, Source: "rule",
			Where: fmt.Sprintf("会话 %s（全段）", short(s.ID)),
			Title: r.Title, Say: render(r.Say, sess),
		}
		f.Evidence = evidence(s.Failures, kindsOf(r.When), 5)
		if len(f.Evidence) == 0 && !r.NoEvidenceOk {
			continue // 没有证据就不发（除非这条判据自己声明「结论就是没有证据」）
		}
		out = append(out, f)
	}
	for i, t := range s.Turns {
		tf := turnFacts(s, t, i)
		for _, r := range rules {
			if r.Scope != ScopeTurn || !match(r.When, tf.metrics) {
				continue
			}
			f := Finding{
				Rule: r.ID, Level: levelOf(r), Scope: r.Scope, Source: "rule",
				Where: fmt.Sprintf("会话 %s 第 %d 轮（%s）", short(s.ID), i+1, t.At.Local().Format("01-02 15:04:05")),
				Title: r.Title, Say: render(r.Say, tf),
			}
			f.Evidence = evidence(t.Failures, kindsOf(r.When), 5)
			if len(f.Evidence) == 0 && !r.NoEvidenceOk {
				continue
			}
			out = append(out, f)
		}
	}
	return out
}

func levelOf(r Rule) Level {
	if r.Level == "" {
		return LevelInfo
	}
	return r.Level
}

// kindsOf 从判据的条件推出它关心哪一类失败。
//
// **证据必须只列这条判据相关的那类** —— 一条「被权限挡下」的结论，证据里混进
// 「文件不存在」，读的人就没法判断这条结论到底成不成立。而这个映射是从判据自己写的
// 指标推出来的，不用在引擎里写死「哪条判据看什么」。
func kindsOf(conds []Cond) map[string]bool {
	kinds := map[string]bool{}
	for _, c := range conds {
		switch c.Metric {
		case "denied":
			kinds[KindDenied] = true
		case "failed":
			kinds[KindFailed] = true
		case "failures":
			kinds[KindDenied] = true
			kinds[KindFailed] = true
		}
	}
	return kinds
}

// evidence 把失败动作点回记录原话。**上限之外的要数出来**，不能悄悄截断。
func evidence(fails []trail.Failure, kinds map[string]bool, limit int) []string {
	relevant := make([]trail.Failure, 0, len(fails))
	for _, f := range fails {
		if kinds[f.Kind] {
			relevant = append(relevant, f)
		}
	}
	var out []string
	for i, f := range relevant {
		if i >= limit {
			out = append(out, fmt.Sprintf("…另有 %d 条，见记录", len(relevant)-limit))
			break
		}
		tool := f.Tool
		if tool == "" {
			tool = "（工具名未知）"
		}
		out = append(out, fmt.Sprintf("%s：%s（%s）", kindWord(f.Kind), tool, f.Why))
	}
	return out
}

func kindWord(kind string) string {
	if kind == KindDenied {
		return "被权限挡下"
	}
	return "执行失败"
}

// facts 是一次求值的上下文：指标（数字）+ 变量（人话）。
type facts struct {
	metrics map[string]int
	vars    map[string]string
}

func sessionFacts(s trail.Session) facts {
	f := facts{metrics: map[string]int{}, vars: map[string]string{}}
	f.metrics["turns"] = len(s.Turns)
	f.metrics["subagents"] = len(s.Subagents)
	f.metrics["denied"], f.metrics["failed"] = countFails(s.Failures)
	f.metrics["failures"] = f.metrics["denied"] + f.metrics["failed"]
	if !s.Found {
		f.metrics["unreadable"] = 1
	}
	f.vars["session"] = short(s.ID)
	f.vars["denied_tools"] = toolsOf(s.Failures, "denied")
	f.vars["failed_whys"] = whysOf(s.Failures, "failed", 2)
	f.vars["failed_tools"] = toolsOf(s.Failures, "failed")
	return f
}

func turnFacts(s trail.Session, t trail.Turn, i int) facts {
	f := facts{metrics: map[string]int{}, vars: map[string]string{}}
	f.metrics["turns"] = 1
	f.metrics["denied"], f.metrics["failed"] = countFails(t.Failures)
	f.metrics["failures"] = f.metrics["denied"] + f.metrics["failed"]
	f.vars["session"] = short(s.ID)
	f.vars["turn_time"] = t.At.Local().Format("01-02 15:04:05")
	f.vars["prompt"] = t.Prompt
	f.vars["denied_tools"] = toolsOf(t.Failures, "denied")
	f.vars["failed_whys"] = whysOf(t.Failures, "failed", 2)
	f.vars["failed_tools"] = toolsOf(t.Failures, "failed")
	return f
}

func countFails(fails []trail.Failure) (denied, failed int) {
	for _, f := range fails {
		if f.Kind == "denied" {
			denied++
		} else {
			failed++
		}
	}
	return denied, failed
}

func toolsOf(fails []trail.Failure, kind string) string {
	seen := map[string]int{}
	for _, f := range fails {
		if f.Kind != kind {
			continue
		}
		name := f.Tool
		if name == "" {
			name = "工具名未知"
		}
		seen[name]++
	}
	if len(seen) == 0 {
		return "无"
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		if seen[n] > 1 {
			parts = append(parts, fmt.Sprintf("%s×%d", n, seen[n]))
		} else {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, "、")
}

func whysOf(fails []trail.Failure, kind string, limit int) string {
	var whys []string
	for _, f := range fails {
		if f.Kind != kind || f.Why == "" {
			continue
		}
		whys = append(whys, f.Why)
		if len(whys) >= limit {
			break
		}
	}
	if len(whys) == 0 {
		return "原因记录里没写"
	}
	return strings.Join(whys, "；")
}

func match(conds []Cond, metrics map[string]int) bool {
	for _, c := range conds {
		got := metrics[c.Metric]
		ok := false
		switch c.Op {
		case ">=":
			ok = got >= c.Value
		case ">":
			ok = got > c.Value
		case "==":
			ok = got == c.Value
		case "!=":
			ok = got != c.Value
		case "<=":
			ok = got <= c.Value
		case "<":
			ok = got < c.Value
		}
		if !ok {
			return false
		}
	}
	return true
}

func render(tpl string, f facts) string {
	var b strings.Builder
	for i := 0; i < len(tpl); {
		open := strings.IndexByte(tpl[i:], '{')
		if open < 0 {
			b.WriteString(tpl[i:])
			break
		}
		b.WriteString(tpl[i : i+open])
		end := strings.IndexByte(tpl[i+open:], '}')
		if end < 0 {
			b.WriteString(tpl[i+open:])
			break
		}
		name := tpl[i+open+1 : i+open+end]
		if v, ok := f.vars[name]; ok {
			b.WriteString(v)
		} else if n, ok := f.metrics[name]; ok {
			b.WriteString(fmt.Sprint(n))
		} else {
			b.WriteString("{" + name + "}")
		}
		i += open + end + 1
	}
	return b.String()
}

func placeholders(tpl string) []string {
	var out []string
	for i := 0; i < len(tpl); {
		open := strings.IndexByte(tpl[i:], '{')
		if open < 0 {
			break
		}
		end := strings.IndexByte(tpl[i+open:], '}')
		if end < 0 {
			break
		}
		out = append(out, tpl[i+open+1:i+open+end])
		i += open + end + 1
	}
	return out
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

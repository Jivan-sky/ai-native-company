package judge

import (
	"strings"
	"testing"
	"time"

	"anc/internal/timeline"
)

// entry 造一条留痕（只填判据用得到的字段）。
func entry(id, caseName, status, title string) timeline.Entry {
	return timeline.Entry{
		ID: id, Case: caseName, At: "2026-10-08T10:00:00+08:00",
		Status: status, Title: title, By: "fde",
	}
}

func factsWith(recs ...timeline.Entry) DeliveryFacts {
	return VaultFacts(nil, timeline.Doc{Entries: recs})
}

// 出厂五门是**数据**长出来的：每道门两条（没落痕 / 有卡点）+ 一条全局缺勤。
// 这条锁住的是形状 —— 加一道门只该是在那张表里加一行。
func TestBuiltinDeliveryCoversEveryGate(t *testing.T) {
	rules := BuiltinDelivery()
	if len(rules) != 1+2*len(BuiltinGates()) {
		t.Fatalf("出厂判据 %d 条，想要 1+2×%d 条", len(rules), len(BuiltinGates()))
	}
	for _, r := range rules {
		if err := r.Validate(); err != nil {
			t.Errorf("出厂判据自己没通过校验：%v", err)
		}
		if r.Scope != ScopeDelivery {
			t.Errorf("%s 的作用域是 %s，想要 delivery", r.ID, r.Scope)
		}
	}
	// 「没落痕」这两条必须声明豁免证据：结论本身就是「没有证据」。
	for _, r := range rules {
		if strings.HasSuffix(r.ID, "-unrecorded") && !r.NoEvidenceOk {
			t.Errorf("%s 结论就是「没落痕」，必须声明 NoEvidenceOk（否则引擎会把它丢掉）", r.ID)
		}
	}
}

// 一条留痕都没有：出全局缺勤那一条，五道门的「没落痕」也各出。
func TestJudgeDeliveryEmptyTimeline(t *testing.T) {
	f := JudgeDelivery(factsWith(), BuiltinDelivery())
	if !hasRule(f, "delivery-no-records") {
		t.Fatalf("空 timeline 应当报 delivery-no-records：%+v", f)
	}
	for _, g := range BuiltinGates() {
		if !hasRule(f, "delivery-"+g.Slug+"-unrecorded") {
			t.Errorf("%s 一条留痕都没有，应当报 %s-unrecorded", g.Name, g.Slug)
		}
		if hasRule(f, "delivery-"+g.Slug+"-stuck") {
			t.Errorf("%s 没有留痕却报「有卡点」", g.Name)
		}
	}
}

// 落了一条绿档留痕：这道门「没落痕」的那条不再报，「有卡点」也不报。
func TestJudgeDeliveryGateRecorded(t *testing.T) {
	f := JudgeDelivery(factsWith(entry("e1", GateCase("proof"), "done", "一线当场说这能帮到我")), BuiltinDelivery())
	if hasRule(f, "delivery-proof-unrecorded") {
		t.Fatal("证明门有留痕了，不该再报「没有落痕」")
	}
	if hasRule(f, "delivery-proof-stuck") {
		t.Fatal("绿档不该被报成卡点")
	}
	// 别的门不受影响。
	if !hasRule(f, "delivery-settle-unrecorded") {
		t.Fatal("沉淀门还是没落痕，应当照报")
	}
}

// 红档留痕 = 卡点：报出来，且证据**只列红档那几条** —— 混进绿档读的人就没法判断。
func TestJudgeDeliveryStuckEvidenceIsRelevantOnly(t *testing.T) {
	recs := []timeline.Entry{
		entry("e1", GateCase("proof"), "done", "薄切片跑通了"),
		entry("e2", GateCase("proof"), "blocked", "一线的人不在场，验不了"),
		entry("e3", GateCase("proof"), "failed", "真实单据拿不到"),
		entry("e4", GateCase("settle"), "blocked", "另一道门的事，不该混进来"),
	}
	f := JudgeDelivery(factsWith(recs...), BuiltinDelivery())
	fd := findRule(f, "delivery-proof-stuck")
	if fd == nil {
		t.Fatalf("有红档留痕却没报卡点：%+v", f)
	}
	if len(fd.Evidence) != 2 {
		t.Fatalf("证据 %d 条，想要 2 条（只有那两条红档）：%v", len(fd.Evidence), fd.Evidence)
	}
	for _, ev := range fd.Evidence {
		if !strings.Contains(ev, "gate:proof") {
			t.Errorf("证据里混进了别的门：%s", ev)
		}
		if strings.Contains(ev, "薄切片跑通了") {
			t.Errorf("证据里混进了绿档：%s", ev)
		}
	}
	// {stuck} 必须解成那一扇门的数，不是别的门的。
	if !strings.Contains(fd.Say, "2 条失败") {
		t.Errorf("say 没解对数目：%s", fd.Say)
	}
}

// 认不出的 status 词：照 timeline 的口径落灰档，**不算绿也不算红** ——
// 把灰当绿就会把「没人判过」看成「过了」。
func TestJudgeDeliveryUnknownStatusIsNotGreen(t *testing.T) {
	facts := factsWith(entry("e1", GateCase("proof"), "也许吧", "记不清了"))
	cf := facts.ByCase[GateCase("proof")]
	if cf.Unknown != 1 || cf.Done != 0 || cf.Stuck != 0 {
		t.Fatalf("灰档归属错了：%+v", cf)
	}
	f := JudgeDelivery(facts, BuiltinDelivery())
	if hasRule(f, "delivery-proof-stuck") {
		t.Fatal("灰档不该被当成卡点")
	}
	if hasRule(f, "delivery-proof-unrecorded") {
		t.Fatal("灰档也是一条留痕，不该报「一道都没落痕」")
	}
}

// 判据只能看**它作用域里**的事实：会话层的指标和交付层的指标不许互串。
// 串了就是一条永远不会命中的判据 —— 比写错更难发现，所以是拒不是忽略。
func TestValidateRejectsCrossScopeMetrics(t *testing.T) {
	cases := []struct {
		why  string
		rule Rule
	}{
		{"会话判据写了交付指标", Rule{ID: "a", Scope: ScopeTurn, Say: "{grants}", When: []Cond{{Metric: "grants", Op: ">=", Value: 1}}}},
		{"交付判据写了会话指标", Rule{ID: "a", Scope: ScopeDelivery, Say: "{denied}", When: []Cond{{Metric: "denied", Op: ">=", Value: 1}}}},
		{"按门统计的指标没写 case", Rule{ID: "a", Scope: ScopeDelivery, Say: "x", When: []Cond{{Metric: "stuck", Op: ">=", Value: 1}}}},
		{"全局指标配了 case", Rule{ID: "a", Scope: ScopeDelivery, Say: "x", When: []Cond{{Metric: "grants", Op: ">=", Value: 1, Case: GateCase("proof")}}}},
		{"会话判据写了 case", Rule{ID: "a", Scope: ScopeTurn, Say: "x", When: []Cond{{Metric: "denied", Op: ">=", Value: 1, Case: "x"}}}},
		{"一条判据两个 case", Rule{ID: "a", Scope: ScopeDelivery, Say: "x", When: []Cond{
			{Metric: "stuck", Op: ">=", Value: 1, Case: GateCase("proof")},
			{Metric: "evidence", Op: "==", Value: 0, Case: GateCase("settle")},
		}}},
		{"say 里用了按门统计的指标却没写 case", Rule{ID: "a", Scope: ScopeDelivery, Say: "{stuck}", When: []Cond{{Metric: "grants", Op: ">=", Value: 1}}}},
	}
	for _, c := range cases {
		if err := c.rule.Validate(); err == nil {
			t.Errorf("%s：应当被拒，实际过了", c.why)
		}
	}
}

// 缺勤类判据的豁免是**数据**（NoEvidenceOk），不是引擎里对某个 id 的特判 ——
// 任何一条自己声明了的判据都该被放行，而不是只有内定的那一条。
func TestNoEvidenceOkIsDataNotHardcode(t *testing.T) {
	rule := Rule{
		ID: "custom-absent-rule", Scope: ScopeTurn, Level: LevelWarn,
		Title: "自己声明豁免的判据", Say: "这一轮没有失败动作",
		When: []Cond{{Metric: "failed", Op: "==", Value: 0}}, NoEvidenceOk: true,
	}
	s := sess(turn(time.Now()))
	found := false
	for _, f := range Judge(s, []Rule{rule}) {
		if f.Rule == rule.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("声明了 NoEvidenceOk 的判据照样被「没有证据就不发」丢掉了")
	}
}

func hasRule(fs []Finding, id string) bool { return findRule(fs, id) != nil }

func findRule(fs []Finding, id string) *Finding {
	for i := range fs {
		if fs[i].Rule == id {
			return &fs[i]
		}
	}
	return nil
}

// 交付层的指标口径：从 org 与 timeline 数出来，一条不重不漏。
func TestVaultFactsCounts(t *testing.T) {
	doc := timeline.Doc{
		Entries: []timeline.Entry{
			entry("e1", GateCase("proof"), "done", "过"),
			entry("e2", GateCase("proof"), "running", "在跑"),
			entry("e3", "", "blocked", "自己一条（空 case）"),
		},
		Bad: []string{"2026-10.toml:3"},
	}
	f := VaultFacts(nil, doc)
	if f.Metrics["records"] != 3 || f.Metrics["cases"] != 2 || f.Metrics["gates"] != 1 {
		t.Fatalf("计数不对：%+v", f.Metrics)
	}
	if cf := f.ByCase[GateCase("proof")]; cf.Evidence != 2 || cf.Done != 1 || cf.Running != 1 {
		t.Fatalf("按门计数不对：%+v", cf)
	}
	// 空 case 的按自己的 id 成一个 case，与 timeline.Fold 的口径一致。
	if cf := f.ByCase["e3"]; cf.Evidence != 1 || cf.Stuck != 1 {
		t.Fatalf("空 case 的归属不对：%+v", cf)
	}
	if len(f.Bad) != 1 {
		t.Fatal("读不懂的行必须原样带出来（不吞）")
	}
}

// 没有 org 时不许 panic（VaultFacts 的 nil 分支）：判据层不该因为「org 读不出来」
// 就整个崩掉 —— 那是另一条链上的事。
func TestVaultFactsNilOrg(t *testing.T) {
	f := VaultFacts(nil, timeline.Doc{})
	if f.Metrics["grants"] != 0 || f.Metrics["members"] != 0 {
		t.Fatalf("nil org 的计数应当是 0：%+v", f.Metrics)
	}
	_ = JudgeDelivery(f, BuiltinDelivery())
}

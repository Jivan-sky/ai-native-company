package judge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anc/internal/trail"
)

func turn(at time.Time, fails ...trail.Failure) trail.Turn {
	return trail.Turn{At: at, Prompt: "问", Failures: fails}
}

func denied(tool string) trail.Failure {
	return trail.Failure{Kind: "denied", Tool: tool, Why: "Permission to use " + tool + " has been denied"}
}

func failed(tool, why string) trail.Failure {
	return trail.Failure{Kind: "failed", Tool: tool, Why: why}
}

func sess(turns ...trail.Turn) trail.Session {
	return trail.Session{Schema: trail.Schema, ID: "sess-1234", Found: true, Turns: turns}
}

// 内置那几条判据本身必须都是合法的 —— 不然出厂就是坏的。
func TestBuiltinRulesValidate(t *testing.T) {
	for _, r := range Builtin() {
		if err := r.Validate(); err != nil {
			t.Errorf("内置判据 %s 不合法：%v", r.ID, err)
		}
	}
}

// 判据是数据：改条件就改行为，引擎一行都不用动。这里用一份自定义判据验这件事。
func TestJudgeFollowsRuleData(t *testing.T) {
	rules := []Rule{{
		ID: "my-rule", Scope: ScopeTurn, Level: LevelWarn,
		Title: "我自己定的判据", Say: "挡了 {denied} 次（{denied_tools}）",
		When: []Cond{{"denied", ">=", 2}},
	}}
	s := sess(
		turn(time.Now(), denied("Bash")),                       // 1 次：不够
		turn(time.Now(), denied("Bash"), denied("PowerShell")), // 2 次：命中
	)
	fs := Judge(s, rules)
	if len(fs) != 1 {
		t.Fatalf("该只有第 2 轮命中，拿到 %d 条：%+v", len(fs), fs)
	}
	if !strings.Contains(fs[0].Say, "2 次") || !strings.Contains(fs[0].Say, "Bash、PowerShell") {
		t.Errorf("模板没按数据渲染：%q", fs[0].Say)
	}
	if !strings.Contains(fs[0].Where, "第 2 轮") {
		t.Errorf("该说清落在哪一轮：%q", fs[0].Where)
	}
	if fs[0].Source != "rule" {
		t.Errorf("来源必须是 rule（这条字段是「判据」与「猜测」的分界线）：%q", fs[0].Source)
	}
}

// 证据只列这条判据相关的那一类：一条「被权限挡下」的结论，证据里不该出现执行失败。
func TestEvidenceOnlyRelevantKind(t *testing.T) {
	rules := []Rule{{
		ID: "denied-only", Scope: ScopeTurn, Level: LevelWarn,
		Title: "被权限挡下", Say: "{denied} 次", When: []Cond{{"denied", ">=", 1}},
	}}
	s := sess(turn(time.Now(), denied("Bash"), failed("Read", "File does not exist")))
	fs := Judge(s, rules)
	if len(fs) != 1 {
		t.Fatalf("该命中 1 条，拿到 %d", len(fs))
	}
	for _, e := range fs[0].Evidence {
		if strings.Contains(e, "File does not exist") {
			t.Errorf("证据串台了：权限判据里混进了执行失败 —— %q", e)
		}
	}
	if len(fs[0].Evidence) != 1 {
		t.Errorf("该只有 1 条证据，拿到 %+v", fs[0].Evidence)
	}
}

// 没有证据就不发 —— 「说了但指不回原件」等于让人信一句没根据的话。
func TestNoEvidenceNoFinding(t *testing.T) {
	rules := []Rule{{
		ID: "needs-evidence", Scope: ScopeTurn, Level: LevelWarn,
		Title: "没有证据的结论", Say: "{}", When: []Cond{{"turns", ">=", 1}},
	}}
	s := sess(turn(time.Now()))
	if fs := Judge(s, rules); len(fs) != 0 {
		t.Fatalf("没有失败动作就不该有失败结论，拿到 %+v", fs)
	}
}

// 账读不到时：只发「读不到」这一条，别的判据不该在空事实上乱判。
func TestUnreadableSessionJudgesOnlyThat(t *testing.T) {
	s := trail.Session{Schema: trail.Schema, ID: "gone", Found: false, Problems: []string{"读不到"}}
	fs := Judge(s, Builtin())
	if len(fs) != 1 || fs[0].Rule != "session-unreadable" {
		t.Fatalf("该只命中 session-unreadable，拿到 %+v", fs)
	}
	if !strings.Contains(fs[0].Say, "先修归集") {
		t.Errorf("该说清下一步：%q", fs[0].Say)
	}
}

// 写错的判据要**被拒**，不能静默失效：永不命中的判据比写错更难发现。
func TestValidateRejectsBadRules(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
		want string
	}{
		{"没 id", Rule{Scope: ScopeTurn, Say: "x", When: []Cond{{"denied", ">=", 1}}}, "缺 id"},
		{"scope 乱写", Rule{ID: "a", Scope: "company", Say: "x", When: []Cond{{"denied", ">=", 1}}}, "scope"},
		{"没有条件", Rule{ID: "a", Scope: ScopeTurn, Say: "x"}, "没有条件"},
		{"指标不存在", Rule{ID: "a", Scope: ScopeTurn, Say: "x", When: []Cond{{"tpkens", ">=", 1}}}, "不存在"},
		{"比较符乱写", Rule{ID: "a", Scope: ScopeTurn, Say: "x", When: []Cond{{"denied", "≈", 1}}}, "比较符"},
		{"占位符不存在", Rule{ID: "a", Scope: ScopeTurn, Say: "{tool}", When: []Cond{{"denied", ">=", 1}}}, "占位符"},
	}
	for _, c := range cases {
		err := c.rule.Validate()
		if err == nil {
			t.Errorf("%s：该被拒，却过了", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：错误信息该提到 %q，拿到 %v", c.name, c.want, err)
		}
	}
}

// 判据文件是整份替换，不是合并 —— 合并会让「现在到底在用哪套」说不清。
func TestLoadReplacesBuiltin(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rules.json")
	body := `{"schema":"anc.judge/v1","rules":[
	  {"id":"only-one","scope":"session","level":"warn","title":"就这一条","say":"{turns} 轮","when":[{"metric":"turns","op":">=","value":1}]}
	]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rules, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].ID != "only-one" {
		t.Fatalf("该整份替换，拿到 %+v", rules)
	}
}

func TestLoadRejectsBadFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{坏"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil {
		t.Error("坏 JSON 该报错")
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"rules":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(empty); err == nil {
		t.Error("空 rules 该报错（空表会让判断层静默什么都不判）")
	}
	unknown := filepath.Join(dir, "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"rules":[{"id":"x","scope":"turn","say":"a","when":[{"metric":"nope","op":">=","value":1}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(unknown); err == nil {
		t.Error("未知指标该报错")
	}
}

// 模板渲染：指标取数字，变量取人话，未知的原样留着（Validate 会先拦）。
func TestRender(t *testing.T) {
	f := facts{
		metrics: map[string]int{"denied": 3},
		vars:    map[string]string{"denied_tools": "Bash、Read"},
	}
	got := render("{denied} 次（{denied_tools}）{没这个}", f)
	if got != "3 次（Bash、Read）{没这个}" {
		t.Errorf("渲染不对：%q", got)
	}
}

func TestMatch(t *testing.T) {
	metrics := map[string]int{"denied": 2, "failed": 0}
	cases := []struct {
		conds []Cond
		want  bool
	}{
		{[]Cond{{"denied", ">=", 2}}, true},
		{[]Cond{{"denied", ">=", 3}}, false},
		{[]Cond{{"denied", "==", 2}, {"failed", "==", 0}}, true},
		{[]Cond{{"denied", "==", 2}, {"failed", ">", 0}}, false},
		{[]Cond{{"failed", "!=", 1}}, true},
	}
	for i, c := range cases {
		if got := match(c.conds, metrics); got != c.want {
			t.Errorf("第 %d 例：想要 %v，拿到 %v", i+1, c.want, got)
		}
	}
}

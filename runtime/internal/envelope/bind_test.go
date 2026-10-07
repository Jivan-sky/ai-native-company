package envelope

import (
	"path/filepath"
	"sort"
	"testing"

	"anc/internal/org"
)

// 正例 vault：members alice(role manager) / bob / devbot，roles devbot|manager|ops，
// domains trade|logistics，projects trade-q3。
func fixtureOrg(t *testing.T, name string) *org.Org {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "orgs", name))
	if err != nil {
		t.Fatal(err)
	}
	o, err := org.Load(abs)
	if err != nil {
		t.Fatalf("加载 %s：%v", name, err)
	}
	return o
}

// ruleIDs 取出发现里的规则 id（排序），断言 id 不断言文案 —— 同 org 包的纪律。
func ruleIDs(issues []org.Issue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Rule)
	}
	sort.Strings(out)
	return out
}

func eqRules(t *testing.T, got []org.Issue, want ...string) {
	t.Helper()
	sort.Strings(want)
	g := ruleIDs(got)
	if len(g) != len(want) {
		t.Fatalf("发现不符：\n  实际 %v\n  期望 %v", g, want)
	}
	for i := range g {
		if g[i] != want[i] {
			t.Fatalf("发现不符：\n  实际 %v\n  期望 %v", g, want)
		}
	}
}

func TestBindHappyPath(t *testing.T) {
	o := fixtureOrg(t, "domains")
	got := Bind(o, sample(), o.Policy)
	if len(got) != 0 {
		t.Fatalf("干净信封不该有发现，实际 %v", ruleIDs(got))
	}
}

// 身份不许自报：who 不在 members/ 里就是「这个人不存在」。
func TestBindWhoUnknown(t *testing.T) {
	o := fixtureOrg(t, "domains")
	e := sample()
	e.Who = "nobody"
	eqRules(t, Bind(o, e, o.Policy), "envelope.who.unknown")
}

// SPEC §2.3：bot 发出的必须带 on_behalf_of。空与解不出分开报。
func TestBindOnBehalf(t *testing.T) {
	o := fixtureOrg(t, "domains")

	e := sample()
	e.OnBehalfOf = ""
	eqRules(t, Bind(o, e, o.Policy), "envelope.on_behalf_of.missing")

	e.OnBehalfOf = "member:nobody"
	eqRules(t, Bind(o, e, o.Policy), "envelope.on_behalf_of.unknown")

	e.OnBehalfOf = "bot:alice" // 认不出的前缀 = 解不出，不是「格式错」
	eqRules(t, Bind(o, e, o.Policy), "envelope.on_behalf_of.unknown")
}

// 三种前缀 + 裸名字（成员 → 岗位 → 域，顺序固定）都必须解得出。
func TestBindOnBehalfResolvesAllForms(t *testing.T) {
	o := fixtureOrg(t, "domains")
	for _, s := range []string{"member:alice", "role:manager", "domain:trade", "alice", "manager", "trade"} {
		e := sample()
		e.OnBehalfOf = s
		if got := Bind(o, e, o.Policy); len(got) != 0 {
			t.Errorf("on_behalf_of=%q 应当解得出，实际 %v", s, ruleIDs(got))
		}
	}
}

func TestBindScopeUnknown(t *testing.T) {
	o := fixtureOrg(t, "domains")
	e := sample()
	e.Scope = Scope{Domain: "nope", Project: "nope-q9"}
	eqRules(t, Bind(o, e, o.Policy), "envelope.scope.domain.unknown", "envelope.scope.project.unknown")
}

// 表为空（没建 domains.md / projects.md）就不报 —— 门禁不许长在别人的文档上。
func TestBindScopeSkippedWhenTablesEmpty(t *testing.T) {
	o := fixtureOrg(t, "one") // 这个 vault 没有域表 / 项目表
	if len(o.Domains) != 0 || len(o.Projects) != 0 {
		t.Fatalf("前提不成立：one 应当没有域表 / 项目表（domains=%d projects=%d）", len(o.Domains), len(o.Projects))
	}
	e := sample()
	e.Scope = Scope{Domain: "trade", Project: "trade-q3"}
	if got := Bind(o, e, o.Policy); len(got) != 0 {
		t.Fatalf("没有表时不该报 scope，实际 %v", ruleIDs(got))
	}
}

func TestBindKind(t *testing.T) {
	o := fixtureOrg(t, "domains")

	e := sample()
	e.Kind = ""
	eqRules(t, Bind(o, e, o.Policy), "envelope.kind.missing")

	e.Kind = "escalate" // 认不出的照收，只提醒
	eqRules(t, Bind(o, e, o.Policy), "envelope.kind.unknown")

	e.Kind = "ESCALATE"
	if got := Bind(o, e, o.Policy); len(got) != 1 || got[0].Rule != "envelope.kind.unknown" {
		t.Fatalf("大小写应当归一成「认不出」而非放行：%v", ruleIDs(got))
	}
}

// 「开关」这件事的证据：规则 id 必须**登记在表里**，否则 Policy.Level 的兜底是 fatal ——
// 拼错一个 id 就等于偷偷上了门禁。这条测试就是防这个。
func TestEnvelopeRulesRegisteredWarnByDefault(t *testing.T) {
	p := org.DefaultPolicy()
	ids := []string{
		"envelope.who.unknown",
		"envelope.on_behalf_of.missing",
		"envelope.on_behalf_of.unknown",
		"envelope.scope.domain.unknown",
		"envelope.scope.project.unknown",
		"envelope.kind.missing",
		"envelope.kind.unknown",
	}
	inTable := map[string]bool{}
	for _, r := range p.Effective() {
		inTable[r.ID] = true
	}
	for _, id := range ids {
		if !inTable[id] {
			t.Errorf("%s 没登记进规则表（anc org check --rules 看不见它）", id)
		}
		if got := p.Level(id); got != org.LevelWarn {
			t.Errorf("%s 默认档 = %s，期望 warn —— 接入面是新能力，先看见、后收紧", id, got)
		}
	}
	// 反过来：表里的 envelope.* 一条都不能是 locked（locked = 不许在 company.md 里关掉）。
	for _, r := range p.Effective() {
		if len(r.ID) > 9 && r.ID[:9] == "envelope." && r.Locked {
			t.Errorf("%s 被标了 locked —— envelope 组不该有红线，它是可开关的", r.ID)
		}
	}
}

// 开关真的能翻：company.md 的 policy 段把默认 warn 提到 fatal，绑定时就该出红档。
func TestPolicyOverridePromotesToFatal(t *testing.T) {
	d, err := org.Parse("---\npolicy:\n  envelope.who.unknown: fatal\n---\n")
	if err != nil {
		t.Fatal(err)
	}
	p := org.DefaultPolicy()
	if iss := p.Apply(d); len(iss) != 0 {
		t.Fatalf("覆盖被拒：%v", iss)
	}
	o := fixtureOrg(t, "domains")
	e := sample()
	e.Who = "nobody"
	got := Bind(o, e, p)
	eqRules(t, got, "envelope.who.unknown")
	if got[0].Level != org.LevelFatal {
		t.Fatalf("提档没生效：%s", got[0].Level)
	}
}

// 开关也能关：off 档直接一条发现都不出。
func TestPolicyOverrideTurnsOff(t *testing.T) {
	d, err := org.Parse("---\npolicy:\n  envelope.kind.unknown: off\n---\n")
	if err != nil {
		t.Fatal(err)
	}
	p := org.DefaultPolicy()
	if iss := p.Apply(d); len(iss) != 0 {
		t.Fatalf("覆盖被拒：%v", iss)
	}
	o := fixtureOrg(t, "domains")
	e := sample()
	e.Kind = "escalate"
	if got := Bind(o, e, p); len(got) != 0 {
		t.Fatalf("off 档应当一条都不出，实际 %v", ruleIDs(got))
	}
}

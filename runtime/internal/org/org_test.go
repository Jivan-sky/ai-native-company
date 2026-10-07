package org

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// 负例只断「规则 id」，不断文案 —— 文案会改，规则 id 是契约。
func ruleIDs(list []Issue) []string {
	out := make([]string, 0, len(list))
	for _, i := range list {
		out = append(out, i.Rule)
	}
	sort.Strings(out)
	return out
}

func assertSameSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s 集合不符\n  实际 %v\n  期望 %v", what, got, want)
	}
}

// fixture 取 runtime/testdata/orgs 下的负例/正例 vault。
func fixture(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "orgs", name))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// copyVault 把 fixture 拷进临时目录，供「改一处再加载」的用例。
func copyVault(t *testing.T, name string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	if err := os.CopyFS(dst, os.DirFS(fixture(t, name))); err != nil {
		t.Fatal(err)
	}
	return dst
}

// addPolicy 往 company.md 的 frontmatter 末尾插一段 policy。
func addPolicy(t *testing.T, vault string, lines ...string) {
	t.Helper()
	path := filepath.Join(vault, "company", "company.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	i := strings.Index(text, "\n---")
	if i < 0 {
		t.Fatalf("%s 里找不到 frontmatter 结束符", path)
	}
	head := text[:i] + "\npolicy:"
	for _, l := range lines {
		head += "\n  " + l
	}
	if err := os.WriteFile(path, []byte(head+text[i:]), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCleanFixtures(t *testing.T) {
	// domains 也在列：划了域的正例必须零发现 —— 否则没人愿意建这张表。
	for _, name := range []string{"one", "six", "disabled", "domains"} {
		o, err := Load(fixture(t, name))
		if err != nil {
			t.Fatalf("%s: 应当加载通过，实际 %v", name, err)
		}
		if o.Policy == nil {
			t.Fatalf("%s: 没拿到生效规则表", name)
		}
		if len(o.Warnings) != 0 {
			t.Fatalf("%s: 不该有非红档发现，实际 %v", name, IssueStrings(o.Warnings))
		}
	}
}

// disabled = 离职停用而不删档：停用的人不进渲染。
func TestDisabledMemberStillLoadsButNotEnabled(t *testing.T) {
	o, err := Load(fixture(t, "disabled"))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Members) != 3 || len(o.Enabled()) != 2 {
		t.Fatalf("期望 3 个成员里 2 个启用，实际 %d / %d", len(o.Members), len(o.Enabled()))
	}
}

func TestFatalRuleIDsPerFixture(t *testing.T) {
	cases := []struct {
		fixture string
		fatal   []string
		warns   []string
	}{
		{
			fixture: "broken-validate",
			fatal: []string{
				"company.admins.unknown",
				"company.devbot.count",
				"company.id.format",
				"feishu.wildcard",
				"member.feishu.app_id.duplicate",
				"member.role.missing",
				"role.bypass.not_devbot",
			},
			// open_id 格式是经验值，默认只告警 —— 这条正是「格式不该拦人」的落点。
			warns: []string{"member.feishu.open_id.format"},
		},
		{
			// 角色层私藏事实：白名单外 H2 段，默认红。
			fixture: "broken-section",
			fatal:   []string{"role.persona.section_unknown"},
		},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			_, err := Load(fixture(t, c.fixture))
			if err == nil {
				t.Fatal("应当被拒绝，实际通过了")
			}
			le, ok := err.(*LoadError)
			if !ok {
				t.Fatalf("错误类型 %T，期望 *LoadError（要能拿到全量报告）", err)
			}
			assertSameSet(t, "fatal", ruleIDs(le.Report.Fatal()), c.fatal)
			assertSameSet(t, "warn", ruleIDs(le.Report.Warns()), c.warns)
		})
	}
}

func TestPolicyOverrideLevels(t *testing.T) {
	t.Run("合法降级生效", func(t *testing.T) {
		v := copyVault(t, "broken-section")
		addPolicy(t, v, "role.persona.section_unknown: off")
		o, err := Load(v)
		if err != nil {
			t.Fatalf("降级后应当通过，实际 %v", err)
		}
		// 降级 ≠ 丢稿：那段正文必须原样保留下来，只是不再拦。
		r := o.Roles["manager"]
		if len(r.Extra) != 1 || !strings.Contains(r.Extra[0].Text, "校验负例") {
			t.Fatalf("被放行的段没有原样保留：%+v", r.Extra)
		}
	})

	t.Run("红线不许降级", func(t *testing.T) {
		v := copyVault(t, "one")
		addPolicy(t, v, "company.defaults.mode.bypass: warn")
		_, err := Load(v)
		assertRule(t, err, "policy.override.locked")
	})

	t.Run("规则 id 拼错要报", func(t *testing.T) {
		v := copyVault(t, "one")
		addPolicy(t, v, "member.feishu.open_id.formt: off")
		_, err := Load(v)
		assertRule(t, err, "policy.override.unknown")
	})

	t.Run("级别值非法要报", func(t *testing.T) {
		v := copyVault(t, "one")
		addPolicy(t, v, "role.allowed_tools.empty: 也许吧")
		_, err := Load(v)
		assertRule(t, err, "policy.override.unknown")
	})
}

// assertRule 断言错误报告里出现了某条规则。
func assertRule(t *testing.T, err error, rule string) {
	t.Helper()
	if err == nil {
		t.Fatalf("应当报 %s，实际通过了", rule)
	}
	le, ok := err.(*LoadError)
	if !ok {
		t.Fatalf("错误类型 %T，期望 *LoadError", err)
	}
	for _, id := range ruleIDs(le.Report.Fatal()) {
		if id == rule {
			return
		}
	}
	t.Fatalf("报告里没有 %s：%v", rule, IssueStrings(le.Report.Issues))
}

func TestParseRejectsUnsupportedYAML(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"tab 缩进", "---\nname: x\na:\n\tb: 1\n---\n", "不许用 tab"},
		{"多行块标量", "---\nname: |\n---\n", "不支持多行块标量"},
		{"两层嵌套", "---\nname: x\na:\n  b:\n    c: 1\n---\n", "不能再嵌套"},
		{"缩进但没有父键", "---\nname: x\n  b: 1\n---\n", "上面没有开启嵌套的键"},
		{"缺结束符", "---\nname: x\n", "缺 frontmatter 结束"},
		{"缺起始符", "name: x\n---\n", "缺 frontmatter 起始"},
		{"锚点键", "---\n&a: 1\n---\n", "键名含非法字符"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.src)
			if err == nil {
				t.Fatalf("应当拒绝，实际通过")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误信息里应有 %q，实际 %q", c.want, err.Error())
			}
		})
	}
}

func TestUnknownSectionKeepsBody(t *testing.T) {
	src := "## 职责\nA\n\n## 别的\nB\n\n## 风格\nC\n"
	got := splitH2(src)
	if len(got) != 3 {
		t.Fatalf("应当切出 3 段（一段都不许丢），实际 %d: %+v", len(got), got)
	}
	if got[1].Name != "别的" || got[1].Text != "B" {
		t.Fatalf("顺序或正文被改动：%+v", got)
	}
}

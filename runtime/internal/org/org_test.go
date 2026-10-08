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

// setDefaults 在 company.md 的 defaults: 段里插一行 —— 模拟「客户自己写这个键」。
func setDefaults(t *testing.T, vault, line string) {
	t.Helper()
	path := filepath.Join(vault, "company", "company.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	const anchor = "defaults:\n"
	i := strings.Index(text, anchor)
	if i < 0 {
		t.Fatalf("%s 里找不到 defaults: 段", path)
	}
	out := text[:i+len(anchor)] + "  " + line + "\n" + text[i+len(anchor):]
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 空闲重置（reset_on_idle_mins）：**0 = 关掉**，也是「没写」时的值。
// 2026-10-07 拍板 —— 换新会话由人显式发 /new，不靠计时器替人猜。
func TestResetOnIdleDefaultsToOff(t *testing.T) {
	o, err := Load(fixture(t, "one"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Company.Defaults.ResetOnIdleMins != 0 {
		t.Fatalf("没写这个键应当是 0（关掉），实际 %d", o.Company.Defaults.ResetOnIdleMins)
	}
}

// 写了就照写 —— 定制落在数据里，改一行就够，不用改代码。
func TestResetOnIdleFollowsData(t *testing.T) {
	v := copyVault(t, "one")
	setDefaults(t, v, "reset_on_idle_mins: 45")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if o.Company.Defaults.ResetOnIdleMins != 45 {
		t.Fatalf("数据写 45，读到 %d", o.Company.Defaults.ResetOnIdleMins)
	}
}

// 写负数要当场拦下：上游 cc-connect 的规矩是 reset_on_idle_mins >= 0，
// 写负它**拒绝启动** = 该 bot 直接下线。所以给红档，不静默、也不悄悄改成 0。
func TestResetOnIdleRejectsNegative(t *testing.T) {
	v := copyVault(t, "one")
	setDefaults(t, v, "reset_on_idle_mins: -5")
	_, err := Load(v)
	assertRule(t, err, "company.defaults.reset_on_idle.invalid")
}

// 顶层「本库落点」不许进路由表。路由表整行进 persona 段 3「数据来源」——
// 多进去一个不等于多一条线索，等于让 agent 照表去一个没有业务资料的地方翻。
//
// 这条盯的是**一类**问题，不是两个名字：charters 来自资产页（那里它已被单独统计，
// 进路由表还会重复计一次），timeline 来自留存记录（谁记谁读）。
func TestRoutingExcludesStructuralDirs(t *testing.T) {
	v := copyVault(t, "domains")
	// 模拟「有人记过一条留存」之后 vault 的样子：timeline/ 会真的出现在顶层。
	if err := os.MkdirAll(filepath.Join(v, "timeline"), 0o755); err != nil {
		t.Fatal(err)
	}
	o, err := Load(v)
	if err != nil {
		t.Fatalf("应当加载通过：%v", err)
	}
	got := map[string]bool{}
	for _, r := range o.Routing {
		got[r.Dir] = true
	}
	for _, bad := range []string{"charters", "timeline", "docs", "_originals", "roles", "members", "company", "templates", "scripts", "skills"} {
		if got[bad] {
			t.Errorf("%s 不该进路由表（结构目录 / 本库落点）", bad)
		}
	}
	// 业务数据目录一个都不能少 —— 修这个问题不许把真数据目录一起关掉。
	for _, want := range []string{"projects", "shipments", "clients"} {
		if !got[want] {
			t.Errorf("%s 是业务数据目录，应当进路由表（实际 %v）", want, o.Routing)
		}
	}
}

// 数据目录的说明会整行进 persona 段 3「数据来源」。取错了不是「少一条线索」，
// 是给 agent 指一条假路 —— 所以这里盯的是「什么不算说明」。
func TestRoutingSummaryPicksRealDescription(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"普通说明", "# 客户\n\n客户档案与联系人口径（一处维护）。\n", "客户档案与联系人口径（一处维护）。"},
		{"标题下面是空行", "# 客户\n\n", ""},
		{"只有标题", "# 客户\n", ""},
		{"列表项也算说明", "# 客户\n\n- 客户档案与合同原件\n", "客户档案与合同原件"},
		{"编号项", "1. 订舱与到货跟踪\n", "订舱与到货跟踪"},
		// 模板脚手架：`<...>` 是「还没写」，不是说明（存量 vault 里就有这种文件）。
		{"模板占位符整行", "# 物流\n\n- <这个目录放什么。这一行会进 persona 的「数据来源」路由表，写清楚它才找得着路>\n", ""},
		{"占位符在前真说明在后", "- <这里还没写>\n\n真说明。\n", "真说明。"},
		{"尖括号不是占位符", "- <A> 与 <B> 的对照表\n", "<A> 与 <B> 的对照表"},
		// 注释是写给自己看的，模板提示就藏在注释里。
		{"只有注释", "# 项目\n\n<!-- 在这里补一行：这个目录住什么资料。 -->\n", ""},
		{"跨行注释", "<!-- 提示第一行\n     提示第二行\n-->\n\n在跑项目的进展与卡点。\n", "在跑项目的进展与卡点。"},
		{"行内注释夹在说明前", "<!-- 提示 --> 在跑项目的进展与卡点。\n", "在跑项目的进展与卡点。"},
		{"CRLF", "# 客户\r\n\r\n客户档案。\r\n", "客户档案。"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := routingSummary(c.in); got != c.want {
				t.Fatalf("routingSummary 取到 %q，期望 %q", got, c.want)
			}
		})
	}
}

// 模板与扫描必须说同一件事：脚手架空着生成出来的目录，一条说明都不算。
// 这两个东西分家过一次（模板留占位符、扫描不认），这条用例防再分家。
func TestTemplatePlaceholderIsNotARoute(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "templates", "org", "data-dir-CLAUDE.md.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	got := routingSummary(strings.ReplaceAll(string(b), "{{DATA_DIR}}", "客户"))
	if got != "" {
		t.Fatalf("按模板新生成的目录说明取到 %q —— 模板占位符又变成一条假路", got)
	}
}

// 存量 vault 的真实形态：早期 `anc org init` 生成的 CLAUDE.md 里还留着占位符行。
// 扫描这一层就得当它是空气，否则修复只对新 vault 生效。
func TestRoutingSkipsLegacyPlaceholder(t *testing.T) {
	v := copyVault(t, "domains")
	body := "# 客户\n\n- <这个目录放什么。这一行会进 persona 的「数据来源」路由表，写清楚它才找得着路>\n"
	if err := os.WriteFile(filepath.Join(v, "clients", "CLAUDE.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	o, err := Load(v)
	if err != nil {
		t.Fatalf("应当加载通过：%v", err)
	}
	for _, r := range o.Routing {
		if r.Dir == "clients" {
			if r.Summary != "" {
				t.Fatalf("clients 的说明取到了占位符：%q", r.Summary)
			}
			return
		}
	}
	t.Fatalf("clients 应当仍在路由表里（只是还没写说明）：%v", o.Routing)
}

// 成员名拼不出合法凭据键名时（中文 / 带 - . 空格）：只告警、不拦。
//
// 为什么是 warn 不是 fatal：存量 vault 不该因为一条新规则突然渲染不出来 ——
// 拦的那一处交给装载（缺键本来就拦），这里只负责让人看见。
// 为什么要报：这个 bot 的凭据键名写不进 secrets.env，它拿不到凭据、静默起不来。
func TestMemberNameFormatWarnsWithoutBlocking(t *testing.T) {
	vault := copyVault(t, "one")
	// 改名要连着 company.md 的 admins 一起改：那条是红档（admins 指向不存在的成员），
	// 不跟着改就会被别的原因拦下，测不到本规则。
	renameMember(t, vault, "alice", "张三")
	o, err := Load(vault)
	if err != nil {
		t.Fatalf("warn 档不该拦下加载：%v", err)
	}
	var got []string
	for _, w := range o.Warnings {
		if w.Rule == "member.name.format" {
			got = append(got, w.Msg)
		}
	}
	if len(got) != 1 {
		t.Fatalf("member.name.format 发现 %d 条，想要 1 条：%v", len(got), got)
	}
	if !strings.Contains(got[0], "ANC_FEISHU_SECRET_张三") {
		t.Fatalf("告警没说清是哪个键名写不出来：%s", got[0])
	}
	if _, enabled := o.Member("张三"); !enabled {
		t.Fatal("改名后的成员必须仍在启用列表里（warn 不拦）")
	}
}

// 反过来：合法名（ASCII 字母数字）一个字节都不许报 —— 否则这条规则会变成噪音。
func TestMemberNameFormatQuietOnCleanFixtures(t *testing.T) {
	for _, name := range []string{"one", "six", "domains", "disabled"} {
		o, err := Load(fixture(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, w := range o.Warnings {
			if w.Rule == "member.name.format" {
				t.Errorf("%s: 干净 fixture 报了 %s", name, w.Msg)
			}
		}
	}
}

// renameMember 把成员 from 就地改成 to：persona.md 的 name，以及 company.md 里对它的引用。
func renameMember(t *testing.T, vault, from, to string) {
	t.Helper()
	for _, rel := range []string{
		filepath.Join("members", from, "persona.md"),
		filepath.Join("company", "company.md"),
	} {
		p := filepath.Join(vault, rel)
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.ReplaceAll(string(raw), from, to)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

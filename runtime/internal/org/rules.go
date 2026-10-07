package org

import (
	"fmt"
	"sort"
	"strings"
)

// Level 是规则的处置级别。
//
// 门禁唯一正当的理由是防「静默的不可逆损失」：让在跑的 bot 下线、对外答错、
// 覆盖人的手写文件。所以只有两档：
//
//	FATAL —— 阻止落盘。
//	WARN  —— 照常走，但必须回显给 owner 看。
//
// 「可逆的小事也拦下来」只是给 owner 添堵，所以默认级别一律按上面这条判据取。
type Level string

const (
	LevelFatal Level = "fatal"
	LevelWarn  Level = "warn"
	LevelOff   Level = "off"
)

// ParseLevel 解析 company.md 里写的级别。
func ParseLevel(s string) (Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "fatal", "error", "err":
		return LevelFatal, true
	case "warn", "warning":
		return LevelWarn, true
	case "off", "skip", "none":
		return LevelOff, true
	}
	return "", false
}

// Rule 是一条校验规则。
//
// 规则住在表里，不埋在实现里：关一条不用发版（在 company.md 的 policy 段覆盖），
// 审计时看这张表，而不是读一千行 Go。
type Rule struct {
	ID     string
	Level  Level
	Locked bool   // true = 不许在 company.md 里降级或关闭
	Why    string // 为什么定这一档
}

// DefaultRules 是出厂规则表，顺序即回显顺序。
var DefaultRules = []Rule{
	// —— 公司层 ——
	{"company.name.missing", LevelFatal, false, "缺公司名，persona 段 1 会渲染成空"},
	{"company.id.format", LevelFatal, false, "id 会进 launchd label 与 project 名前缀，非 ASCII 会炸服务管理"},
	{"company.platform.unsupported", LevelFatal, false, "v1 只支持 feishu，别的平台没有渲染路径"},
	{"company.defaults.model.missing", LevelFatal, false, "三级回退（member → role → company）没有终点，会渲染出空 model"},
	{"company.defaults.mode.bypass", LevelFatal, true, "SPEC §6-5 无例外红线：角色 bot 一律不授予 bypass"},
	{"company.devbot.count", LevelFatal, false, "SPEC §1：一个公司恰好一个启用中的 devbot"},
	{"company.admins.unknown", LevelFatal, false, "admins 渲染进 admin_from，指向不存在的成员等于特权命令无人可发"},
	{"company.admins.disabled", LevelWarn, false, "管理员已停用，admin_from 仍会渲染它；确认是有意保留"},

	// —— 成员层 ——
	{"member.name.duplicate", LevelFatal, false, "同名成员会生成同名 project，gateway 只留一个"},
	{"member.display_name.missing", LevelFatal, false, "persona 段 1 的「服务对象」会渲染成 —"},
	{"member.role.missing", LevelFatal, false, "role 不存在就没有 persona 基线，渲染直接失败"},
	{"member.model.unresolved", LevelFatal, false, "member 没写 model，role / company 也没写，无处回退"},
	{"member.feishu.open_id.missing", LevelFatal, false, "allow_from 靠它；缺了就没人能跟这个 bot 说话"},
	{"member.feishu.open_id.format", LevelWarn, false, "格式（ou 前缀）只是经验值，平台是可变层，不该拿它拦人"},
	{"member.feishu.open_id.duplicate", LevelFatal, false, "两个人共用一个 open_id，等于一个 bot 服务两个人"},
	{"member.feishu.app_id.missing", LevelFatal, false, "没有 app_id 建不起平台连接"},
	{"member.feishu.app_id.duplicate", LevelFatal, false, "同一个飞书应用被两个 project 抢连，消息归属不确定"},
	{"feishu.wildcard", LevelFatal, false, "SPEC §6-1：白名单默认关闭注册，星号等于对所有人开放"},

	// —— 角色层 ——
	{"role.bypass.not_devbot", LevelFatal, true, "SPEC §6-5 无例外红线：只有 devbot 可以有 bypassPermissions"},
	{"role.allowed_tools.empty", LevelWarn, false, "「空 allowed_tools = 全开」尚未实测；先告警，实测后再定档"},
	{"role.persona.section_unknown", LevelFatal, false, "canonical registry 纪律：角色层不许私藏事实（段名清单见 company.persona_sections）"},

	// —— persona 文本 ——
	{"persona.toml.quote_break", LevelFatal, true, "SPEC §171 生产事故：persona 内联进 TOML，裸三引号曾致全部 bot 下线"},
	{"persona.env.substitution", LevelFatal, false, "gateway 会替换美元大括号引用，persona 里这种引用会被吞掉或注入"},
	{"persona.slot.unreplaced", LevelWarn, false, "双花括号槽位没被替换就进了 persona，多半是模板没跑完"},
	{"persona.path.absolute", LevelWarn, false, "绝对路径写死本机布局，搬机器 / 换 vault 就打脸"},
	{"persona.fact.frozen", LevelWarn, false, "「当前仅有 / 目前只有」疑似把易变事实烤进 persona"},

	// —— 业务域（罗盘）——
	// 域表是新能力：没有这张表的存量 vault 不该被拦（LoadDomains 在文件缺席时一声不吭），
	// 但一旦建了表，表里的结构问题必须被看见 —— 所以整组默认 warn，一条红线都不设。
	// 想让某条变红，在 company.md 的 policy 段写 `规则id = fatal`（本组全部允许上调）。
	{"domain.table.missing", LevelWarn, false, "有 domains.md 但没找到带 slug 列的表 —— 表建歪了，域一个都读不出来"},
	{"domain.slug.format", LevelWarn, false, "slug 要进文件名与代码；中文/大写会撞 macOS 的 NFC/NFD 与跨平台差异"},
	{"domain.slug.duplicate", LevelWarn, false, "同 slug 两行 = 域身份有歧义，取哪一行只看行的先后"},
	{"domain.name.missing", LevelWarn, false, "缺显示名，persona 里只剩机器标识"},
	{"domain.what.missing", LevelWarn, false, "缺「这块业务是什么」—— agent 少一条判断口径的依据"},
	{"domain.who.missing", LevelWarn, false, "缺「谁在做」—— 这一域卡住时没人可找"},
	{"domain.data.missing", LevelWarn, false, "缺「数据在哪」—— 这一域没有作用域，授权无从落脚"},
	{"domain.who.unknown_role", LevelWarn, false, "who 不在 roles/ 里，渲染时解不出人，只会原样显示岗位名"},
	{"domain.data.unknown_dir", LevelWarn, false, "data 不是 vault 顶层数据目录，作用域指不到地方"},
	{"member.domains.unknown", LevelWarn, false, "成员写了未定义的域，多半是拼错 —— 等于悄悄少给他一块业务"},

	// —— 项目表（立项书汇总）——
	// 与域表同一档，理由也一样：立项书是别人写的、格式随公司/行业而变，ANC 只汇总不清点格式，
	// 所以整组 warn、一条红线都不设。想让某条变红，在 company.md 的 policy 段写 `规则id = fatal`。
	{"project.table.missing", LevelWarn, false, "有 projects.md 但没找到带 slug 列的表 —— 表建歪了，项目一个都读不出来"},
	{"project.slug.format", LevelWarn, false, "slug 要进文件名与代码；中文/大写会撞 macOS 的 NFC/NFD 与跨平台差异"},
	{"project.slug.duplicate", LevelWarn, false, "同 slug 两行 = 项目身份有歧义，取哪一行只看行的先后"},
	{"project.name.missing", LevelWarn, false, "缺项目名，看板上只剩机器标识"},
	{"project.domain.unknown", LevelWarn, false, "挂的域不在 domains.md 里 —— 多半是拼错；项目会分不到区块"},
	{"project.owner.unknown_role", LevelWarn, false, "owner 不在 roles/ 里，渲染时解不出人，只会原样显示岗位名"},

	// —— 策略层（策略自己也要被校验，否则拼错规则名 = 你以为关了其实没关）——
	{"policy.override.unknown", LevelFatal, true, "policy 段写了不存在的规则 id 或非法级别值"},
	{"policy.override.locked", LevelFatal, true, "试图降级 / 关闭无例外红线（SPEC §6-5 / §171）"},
}

// Policy 是叠加了公司级覆盖之后的生效规则表。
type Policy struct {
	levels    map[string]Level
	locked    map[string]bool
	why       map[string]string
	overrides map[string]Level
}

// DefaultPolicy 造一份出厂策略。
func DefaultPolicy() *Policy {
	p := &Policy{
		levels:    map[string]Level{},
		locked:    map[string]bool{},
		why:       map[string]string{},
		overrides: map[string]Level{},
	}
	for _, r := range DefaultRules {
		p.levels[r.ID] = r.Level
		p.why[r.ID] = r.Why
		if r.Locked {
			p.locked[r.ID] = true
		}
	}
	return p
}

// Level 取规则级别。未登记的 id 当 fatal —— 拼错规则名不许悄悄放行。
func (p *Policy) Level(id string) Level {
	if l, ok := p.levels[id]; ok {
		return l
	}
	return LevelFatal
}

// Issue 用本策略的级别造一条发现。
func (p *Policy) Issue(rule, where, format string, a ...any) Issue {
	return Issue{Rule: rule, Level: p.Level(rule), Where: where, Msg: fmt.Sprintf(format, a...)}
}

// Effective 返回生效规则表（顺序同 DefaultRules），供 `anc org check --rules` 回显。
func (p *Policy) Effective() []Rule {
	out := make([]Rule, 0, len(DefaultRules))
	for _, r := range DefaultRules {
		r.Level = p.Level(r.ID)
		out = append(out, r)
	}
	return out
}

// OverrideIDs 返回被 company.md 改过级别的规则 id（排序）。
func (p *Policy) OverrideIDs() []string {
	out := make([]string, 0, len(p.overrides))
	for id := range p.overrides {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Apply 把 company/company.md 里 `policy:` 段的覆盖叠加上去。
// 段内写法：一行一条 `规则id = fatal|warn|off`。
func (p *Policy) Apply(d *Doc) []Issue {
	var out []Issue
	keys := make([]string, 0, len(d.Meta))
	for k := range d.Meta {
		if strings.HasPrefix(k, "policy.") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		id := strings.TrimPrefix(k, "policy.")
		where := "company/company.md"
		if n := d.Line(k); n > 0 {
			where = fmt.Sprintf("company/company.md 第 %d 行", n)
		}
		if _, known := p.levels[id]; !known {
			out = append(out, p.Issue("policy.override.unknown", where,
				"policy 段里的 %q 不是已登记的规则 id（查 `anc org check --rules`）", id))
			continue
		}
		lv, ok := ParseLevel(fmt.Sprintf("%v", d.Meta[k]))
		if !ok {
			out = append(out, p.Issue("policy.override.unknown", where,
				"%s 的级别 %v 非法；只认 fatal / warn / off", id, d.Meta[k]))
			continue
		}
		if lv != LevelFatal && p.locked[id] {
			out = append(out, p.Issue("policy.override.locked", where,
				"%s 不许降级到 %s（%s）", id, lv, p.why[id]))
			continue
		}
		p.levels[id] = lv
		p.overrides[id] = lv
	}
	return out
}

// Issue 是一条校验发现。规则 id 稳定、文案会改 —— 所以测试断言 id，不断言文案。
type Issue struct {
	Rule  string
	Level Level
	Where string
	Msg   string
}

func (i Issue) String() string {
	if i.Where == "" {
		return fmt.Sprintf("[%s] %s", i.Rule, i.Msg)
	}
	return fmt.Sprintf("[%s] %s: %s", i.Rule, i.Where, i.Msg)
}

// Report 是一次校验的全量发现。
type Report struct {
	Issues []Issue
}

// Add 记录一条发现；off 档的直接丢弃。
// 逐字相同的重复发现只留一条：两个成员共用一个角色时，角色层的问题不该报两遍。
func (r *Report) Add(i Issue) {
	if i.Level == LevelOff || i.Rule == "" {
		return
	}
	for _, e := range r.Issues {
		if e == i {
			return
		}
	}
	r.Issues = append(r.Issues, i)
}

// Merge 并入另一份报告。
func (r *Report) Merge(o *Report) {
	for _, i := range o.Issues {
		r.Add(i)
	}
}

// ByLevel 取某一档的发现。
func (r *Report) ByLevel(l Level) []Issue {
	var out []Issue
	for _, i := range r.Issues {
		if i.Level == l {
			out = append(out, i)
		}
	}
	return out
}

// Fatal / Warns 按档取（Warns 是方法，与 Issue 的字段区分开）。
func (r *Report) Fatal() []Issue { return r.ByLevel(LevelFatal) }
func (r *Report) Warns() []Issue { return r.ByLevel(LevelWarn) }

// Err 把 fatal 档折成 error；没有 fatal 返回 nil。
func (r *Report) Err() error {
	f := r.Fatal()
	if len(f) == 0 {
		return nil
	}
	return fmt.Errorf("org 校验未通过（%d 项红）:\n  - %s", len(f), strings.Join(IssueStrings(f), "\n  - "))
}

// LoadError 携带全量报告。红档拦下，非红档也一并说出来 ——
// 一次报全，别让人修一个才看见下一个。
type LoadError struct {
	Report *Report
}

func (e *LoadError) Error() string {
	f := e.Report.Fatal()
	msg := fmt.Sprintf("org 校验未通过（%d 项红）:\n  - %s", len(f), strings.Join(IssueStrings(f), "\n  - "))
	if w := e.Report.Warns(); len(w) > 0 {
		msg += fmt.Sprintf("\n另有 %d 项非红档发现（不拦，但请过目）:\n  - %s", len(w), strings.Join(IssueStrings(w), "\n  - "))
	}
	return msg
}

// IssueStrings 把发现折成一行一条。
func IssueStrings(list []Issue) []string {
	out := make([]string, 0, len(list))
	for _, i := range list {
		out = append(out, i.String())
	}
	return out
}

// FatalOf 从一堆发现里挑出 fatal 档。
func FatalOf(list []Issue) []Issue {
	var out []Issue
	for _, i := range list {
		if i.Level == LevelFatal {
			out = append(out, i)
		}
	}
	return out
}

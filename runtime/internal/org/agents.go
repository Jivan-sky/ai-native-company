package org

import (
	"os"
	"path/filepath"
	"strings"
)

// Agent 是**业务 agent** —— SPEC §2.3 的第二类：代**岗位 / 流程**，不是代人。
//
// 为什么单独一张表，而不是塞进 members/：两类 agent 单位不同、失败代价也不同
// （成员 bot 答错，人还能兜；业务 bot 处理错单要赔钱）。members/ 的字段里有一半
// （display_name / open_id / allow_from）对业务 agent 没有意义；混在一张表里，
// 「人」和「岗位」就会被同一套必填项绑住。
//
// **这一层只声明「它存在、它挂哪块业务、它能用哪些工具」。** 它落在哪台机器、哪个
// OS 账号、哪个目录 —— 全是**本机事实**，不进 git（SPEC §4.5：git = org 真相，
// ~/.anc = 部署参数；vault_root 这类单机路径绝不写进 org 仓库）。
type Agent struct {
	Slug        string // ASCII 标识：进 project 名（<公司 id>-<slug>），也适合当 OS 账号名
	Name        string // 中文显示名
	Description string // **铭文**：这个 agent 干什么的 / 替谁干。
	// 为什么要有它：业务 agent 与成员 bot 接在同一张口上，光一个编号分辨不出「这是谁」——
	// 归属看 Domain，干什么的看这一格。人写的，不是agent自报。
	Domain  string // 挂哪块业务（domains.md 的 slug）—— 数据视野那把尺子以域为单位
	Harness string // 跑哪条腿：harness 表里的 agent type（claudecode / codex / …）；空 = 默认那家
	Tools   string // allowed_tools（逗号分隔）—— **非空是红线**，见 agent.tools.empty
	Model   string // 可选；空 = 走 company 默认
	Role    string // 代哪个**岗位**（roles/ 的 slug）。persona 的职责 / 风格 / 术语表从这一层来 ——
	// SPEC §2.3：成员 bot 代人，业务 agent 代岗位，两者共用同一套 persona 机制（Q23 已拍：表里加列，
	// 不另开 agents/<slug>/ 目录；等哪天一个 agent 的上下文一套 persona.md 装不下，再升到目录形态）
	AppID string // 平台绑定：这个业务 agent 自己的 app_id。它**不是**秘密，进 git；
	// app_secret 不进 git —— 键名按 AgentSecretKey 派生，明文住 secrets.env（同成员 bot 的做法）。
	// 留空 = 还没接线：这个 project 照渲染（与成员的 unwired 同档），由探针报**灰**，不假装绿。
	Line int // 在 agents.md 里的行号（报错定位用）
}

// AgentsFile 是业务 agent 表的唯一落点（与 domains.md / projects.md 同级）。
const AgentsFile = "agents.md"

// knownAgentColumns 是认得的列名；不认得的列忽略（表格可以多列备注，不影响解析）。
var knownAgentColumns = map[string]bool{
	"slug": true, "name": true, "domain": true,
	"harness": true, "tools": true, "model": true,
	"description": true,
	"role":        true, "app_id": true,
}

// LoadAgents 读 agents.md。文件不存在 = 这家公司还没有业务 agent（向后兼容，不报错）。
//
// 只查「表本身」：slug 合不合法 / 重不重、必填项缺不缺。domain 与 harness 指向的
// 东西存不存在，属于跨表校验（见 validateAgents 与渲染层）。
func LoadAgents(root string, p *Policy) ([]Agent, []Issue, error) {
	b, err := os.ReadFile(filepath.Join(root, AgentsFile))
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	head, cols := findTable(lines, knownAgentColumns, "slug")
	if head < 0 {
		return nil, []Issue{p.Issue("agent.table.missing", AgentsFile,
			"没有找到带 slug 列的表：业务 agent 表必须是一张 markdown 表（第一行表头，第二行分隔）")}, nil
	}

	var (
		out    []Agent
		issues []Issue
		seen   = map[string]int{}
	)
	for i := head + 2; i < len(lines); i++ {
		cells := tableCells(lines[i])
		if cells == nil {
			break
		}
		a := Agent{Line: i + 1}
		get := rowReader(cols, cells)
		a.Slug, a.Name, a.Domain = get("slug"), get("name"), get("domain")
		a.Description = get("description")
		a.Harness, a.Tools, a.Model = get("harness"), get("tools"), get("model")
		a.Role, a.AppID = get("role"), get("app_id")
		where := agentWhere(a.Line)

		if a.Slug == "" {
			if a.Name != "" || a.Domain != "" || a.Tools != "" {
				issues = append(issues, p.Issue("agent.slug.format", where, "缺 slug（机器标识）；这一行其余内容将被忽略"))
			}
			continue
		}
		if !reID.MatchString(a.Slug) {
			issues = append(issues, p.Issue("agent.slug.format", where,
				"slug=%q 必须是 ASCII 小写标识（%s）—— 它要进 project 名，通常还要当 OS 账号名", a.Slug, reID))
		}
		if prev, dup := seen[a.Slug]; dup {
			issues = append(issues, p.Issue("agent.slug.duplicate", where, "slug=%q 与第 %d 行重复", a.Slug, prev))
		} else {
			seen[a.Slug] = a.Line
		}
		if a.Name == "" {
			issues = append(issues, p.Issue("agent.name.missing", where, "slug=%q 缺 name（显示名）", a.Slug))
		}
		if a.Domain == "" {
			issues = append(issues, p.Issue("agent.domain.missing", where,
				"业务 agent %q 没写 domain —— 数据视野那把尺子（VisibleDomains）以域为单位，没域等于没视野", a.Slug))
		}
		if a.Role == "" {
			issues = append(issues, p.Issue("agent.role.missing", where,
				"业务 agent %q 没写 role —— 它代哪个岗位？persona 的职责 / 风格 / 术语表是从 roles/<role>/ 那一层取的，"+
					"没它这一段就空着（SPEC §2.3：成员 bot 代人，业务 agent 代岗位）", a.Slug))
		}
		if SplitList(a.Tools) == nil {
			issues = append(issues, p.Issue("agent.tools.empty", where,
				"业务 agent %q 的 tools 是空的 —— 实测（上游 schema 790 行）：dontAsk 下未预授权的工具一律自动拒绝，"+
					"空白名单的 bot 连得上却干不了活（SPEC §4.7 ③：allowed_tools 非空；只有 devbot 例外）", a.Slug))
		}
		out = append(out, a)
	}
	return out, issues, nil
}

func agentWhere(line int) string { return AgentsFile + " 第 " + itoa(line) + " 行" }

// AgentWhere 是 agents.md 第 n 行的定位串。导出给渲染器复用，
// 让「表里的问题」与「注入之后的问题」报的是同一个位置（同 DomainWhere 的用法）。
func AgentWhere(line int) string { return agentWhere(line) }

// SplitList 把逗号分隔的列表拆开；空 / 只有空白 → nil。
func SplitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// AgentTools 是业务 agent 的 allowed_tools；渲染与校验共用这一处拆分。
func (o *Org) AgentTools(a Agent) []string { return SplitList(a.Tools) }

// Agent 按 slug 取业务 agent。
func (o *Org) Agent(slug string) (Agent, bool) {
	for _, a := range o.Agents {
		if a.Slug == slug {
			return a, true
		}
	}
	return Agent{}, false
}

// validateAgents 做跨表校验：domain 指向的域存不存在、role 指向的岗位存不存在、
// project 名会不会和某个成员 bot 撞。只发现事实，档次由规则表定（实现里不许硬编码档次）。
func (o *Org) validateAgents(p *Policy, rep *Report) {
	seen := map[string]bool{}
	for _, d := range o.Domains {
		seen[d.Slug] = true
	}
	for _, a := range o.Agents {
		if a.Domain != "" && !seen[a.Domain] {
			rep.Add(p.Issue("agent.domain.unknown", agentWhere(a.Line),
				"domain=%q 不在 %s 里（现有：%s）—— 这块业务不存在，它的数据视野指不到地方",
				a.Domain, DomainsFile, o.domainSlugs()))
		}
		if a.Role != "" {
			if _, ok := o.Roles[a.Role]; !ok {
				rep.Add(p.Issue("agent.role.unknown", agentWhere(a.Line),
					"role=%q 不在 roles/ 里（现有：%s）—— 多半是拼错；这个 agent 的 persona 取不到职责那一段",
					a.Role, o.roleSlugs()))
			}
		}
		// project 名是 <公司 id>-<slug>，成员 bot 也是 <公司 id>-<成员名> —— 两边同名 = 同一份配置里
		// 两个 project 抢一个名字，上游只会留一个，而「留下的那个是谁」取决于顺序。
		// 这不是学术问题：members/ 里本来就有 ASCII 名（devbot）。
		if _, ok := o.Member(a.Slug); ok {
			rep.Add(p.Issue("agent.slug.collides_member", agentWhere(a.Line),
				"slug=%q 与 members/ 里的成员同名 —— 两边的 project 名会撞成同一个，配置里只留得下一个", a.Slug))
		}
	}
}

package org

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Project 是 projects.md 里的一行 —— ANC 自己的「项目汇总」，不是立项书。
//
// 为什么要有这张表：立项书的格式随公司 / 行业 / PMO 而变，而且它在各人自己的 agent
// 那边生成，到 ANC 时就已经存在。ANC 只做两件事：
//
//   - 约定落点：立项书放哪，由 Source 指出去（真源可以是飞书文档、客户自己的库）；
//   - 汇总成这张表：一行一个项目，由 agent 抽取后写入 —— 于是「谁在做哪个项目」
//     不必挨个问人，看板也能分得出区块。
//
// 「活的那份」（进度、风险、这周干了什么）不在这里：那是 agent 每天写的东西，
// 表里只留指针与锚点。抽取里「这个项目挂哪个域」属于**权柄**（提案 + 人确认），
// 不是知识；但表本身不拦，只把「挂的域不存在」报出来给人看。
type Project struct {
	Slug   string // ASCII 标识：机器用它，人不用
	Name   string // 项目显示名
	Domain string // 挂哪块业务（domains.md 的 slug）；空 = 待确认
	Owner  string // 卡住找谁：填**岗位**（roles/ 下的 role），人名渲染时从 members/ 现算
	Period string // 周期：自由文本（「2026-08-01 → 2026-09-24」「进行中」都行），**不解析**
	Source string // 真源在哪（飞书 URL / doc id / 本地路径）—— 是指针，不是说明
	Line   int    // 在 projects.md 里的行号（报错定位用）
}

// ProjectsFile 是项目表的唯一落点（与 domains.md 同级）。
const ProjectsFile = "projects.md"

// knownProjectColumns 是认得的列名；不认得的列忽略（表格可以多列备注，不影响解析）。
var knownProjectColumns = map[string]bool{
	"slug": true, "name": true, "domain": true,
	"owner": true, "period": true, "source": true,
}

// LoadProjects 读 projects.md。文件不存在 = 还没建项目表（向后兼容，不报错）。
//
// 只查「表本身」：slug 合不合法 / 重不重、name 缺不缺。domain 与 owner 指向的东西
// 存不存在，属于跨表校验（validateProjects）。
//
// **period 一个字符都不解析**：客户那边它可能是「8/1–8/15」，可能是「进行中」，
// 可能是几个里程碑串起来的。把它解析成日期，就是 ANC 开始规定别人的文档 ——
// 而那正是我们说好不做的事（SPEC §1：ANC 只接入，不替客户规定）。
//
// 同理，四个锚点（哪个项目 / 挂哪块业务 / 卡住找谁 / 周期多久）一个都不设必填：
// 抽不到就先空着 = 待确认。门禁不许长在别人的文档上。
func LoadProjects(root string, p *Policy) ([]Project, []Issue, error) {
	b, err := os.ReadFile(filepath.Join(root, ProjectsFile))
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	head, cols := findTable(lines, knownProjectColumns, "slug")
	if head < 0 {
		return nil, []Issue{p.Issue("project.table.missing", ProjectsFile,
			"没有找到带 slug 列的表：项目表必须是一张 markdown 表（第一行表头，第二行分隔）")}, nil
	}

	var (
		out    []Project
		issues []Issue
		seen   = map[string]int{}
	)
	for i := head + 2; i < len(lines); i++ {
		cells := tableCells(lines[i])
		if cells == nil {
			break
		}
		pr := Project{Line: i + 1}
		get := rowReader(cols, cells)
		pr.Slug, pr.Name = get("slug"), get("name")
		pr.Domain, pr.Owner = get("domain"), get("owner")
		pr.Period, pr.Source = get("period"), get("source")
		where := projectWhere(pr.Line)

		if pr.Slug == "" {
			if pr.Name != "" {
				issues = append(issues, p.Issue("project.slug.format", where,
					"缺 slug（机器标识）；这一行其余内容将被忽略"))
			}
			continue
		}
		if !reID.MatchString(pr.Slug) {
			issues = append(issues, p.Issue("project.slug.format", where,
				"slug=%q 必须是 ASCII 小写标识（%s）—— 它要进文件名与代码，中文会撞 macOS 的 NFC/NFD 与跨平台差异", pr.Slug, reID))
		}
		if prev, dup := seen[pr.Slug]; dup {
			issues = append(issues, p.Issue("project.slug.duplicate", where, "slug=%q 与第 %d 行重复", pr.Slug, prev))
		} else {
			seen[pr.Slug] = pr.Line
		}
		if pr.Name == "" {
			issues = append(issues, p.Issue("project.name.missing", where,
				"slug=%q 缺 name（项目显示名）—— 看板上只剩机器标识", pr.Slug))
		}
		out = append(out, pr)
	}
	return out, issues, nil
}

func projectWhere(line int) string {
	return ProjectsFile + " 第 " + itoa(line) + " 行"
}

// ProjectWhere 是 projects.md 第 n 行的定位串。导出是为了让别的消费面（看板、未来的
// 授权）报的是同一个位置 —— 同一个问题两处各写一遍定位串，迟早有一处对不上。
func ProjectWhere(line int) string { return projectWhere(line) }

// Project 按 slug 取项目。
func (o *Org) Project(slug string) (Project, bool) {
	for _, p := range o.Projects {
		if p.Slug == slug {
			return p, true
		}
	}
	return Project{}, false
}

// validateProjects 做跨表校验：domain 是不是已定义的域、owner 是不是真岗位。
// 只发现事实，档次由规则表定（实现里不许硬编码档次）。
func (o *Org) validateProjects(p *Policy, rep *Report) {
	for _, pr := range o.Projects {
		where := projectWhere(pr.Line)
		if pr.Domain != "" {
			if _, ok := o.Domain(pr.Domain); !ok {
				rep.Add(p.Issue("project.domain.unknown", where,
					"domain=%q 不在 %s 里（现有：%s）—— 项目挂空，看板上分不到区块",
					pr.Domain, DomainsFile, o.domainSlugs()))
			}
		}
		if pr.Owner != "" {
			if _, ok := o.Roles[pr.Owner]; !ok {
				rep.Add(p.Issue("project.owner.unknown_role", where,
					"owner=%q 不在 roles/ 里（现有：%s）—— 渲染时解不出人，只会原样显示岗位名",
					pr.Owner, strings.Join(o.roleNames(), " / ")))
			}
		}
	}
}

// domainSlugs 给报错文案用（排序，保证同输入同输出）。
func (o *Org) domainSlugs() string {
	out := make([]string, 0, len(o.Domains))
	for _, d := range o.Domains {
		out = append(out, d.Slug)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return "还没有任何域"
	}
	return strings.Join(out, " / ")
}

package org

import (
	"path/filepath"
	"testing"
)

// 项目表是「谁在做哪个项目」的机器落点：先保证读得出来。
func TestLoadProjectsTable(t *testing.T) {
	o, err := Load(fixture(t, "domains"))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Projects) != 1 {
		t.Fatalf("期望 1 个项目，实际 %d：%+v", len(o.Projects), o.Projects)
	}
	pr, ok := o.Project("trade-q3")
	if !ok {
		t.Fatal("取不到 trade-q3")
	}
	if pr.Name != "Q3 结算改造" || pr.Domain != "trade" || pr.Owner != "manager" {
		t.Fatalf("项目字段没解对：%+v", pr)
	}
	if pr.Source == "" {
		t.Fatalf("source（真源指针）没读出来：%+v", pr)
	}
	if pr.Line == 0 {
		t.Fatal("没记住行号 —— 报错就没法定位")
	}
}

// **这条是「不定义别人的文档」的第一条验收**：period 是自由文本，原文照搬。
// 客户写「进行中」「8/1–8/15」「几个里程碑串起来」都合法，ANC 不许解析、不许报错。
func TestProjectPeriodIsFreeText(t *testing.T) {
	cases := []string{"进行中", "8/1–8/15", "一期 2026-08 至 2026-10，二期待定", ""}
	for _, raw := range cases {
		v := copyVault(t, "domains")
		writeAt(t, filepath.Join(v, ProjectsFile),
			"| slug | name | domain | owner | period |\n|---|---|---|---|---|\n| x | 甲 | trade | manager | "+raw+" |\n")
		o, err := Load(v)
		if err != nil {
			t.Fatalf("period=%q 被拦了：%v", raw, err)
		}
		if len(o.Projects) != 1 || o.Projects[0].Period != raw {
			t.Fatalf("period=%q 没被原文保留：%+v", raw, o.Projects)
		}
	}
}

// 列按表头认，不按位置猜 —— 换个列序照样读对（表格是可以手改的）。
func TestProjectColumnsFollowHeader(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, ProjectsFile),
		"| owner | slug | domain | name |\n|---|---|---|---|\n| manager | trade-q3 | trade | Q3 结算改造 |\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Projects) != 1 {
		t.Fatalf("期望 1 个项目，实际 %+v", o.Projects)
	}
	pr := o.Projects[0]
	if pr.Slug != "trade-q3" || pr.Owner != "manager" || pr.Domain != "trade" || pr.Name != "Q3 结算改造" {
		t.Fatalf("列序改了就读错：%+v", pr)
	}
}

// 跨表校验：挂的域、卡住找谁 —— 两条都只是 warn。域错误是权柄问题（提案 + 人确认），
// 不是格式问题；ANC 只把它报出来给人看，不拦。
func TestProjectCrossChecksWarnOnly(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, ProjectsFile),
		"| slug | name | domain | owner |\n|---|---|---|---|\n| x | 甲 | 不存在的域 | 不存在的岗 |\n")
	o, err := Load(v)
	if err != nil {
		t.Fatalf("跨表问题默认只告警，不该拦：%v", err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{
		"project.domain.unknown", "project.owner.unknown_role",
	})
}

// 四个锚点一个都不设必填：抽不到就先空着 = 待确认。只有「连 slug 都没有」才报，
// 且报的这一条也只管「这一行会被忽略」，不拦住加载。
func TestProjectAnchorsAreOptional(t *testing.T) {
	v := copyVault(t, "one")
	writeAt(t, filepath.Join(v, ProjectsFile),
		"| slug | name | domain | owner | period | source |\n|---|---|---|---|---|---|\n|  | 只有名字 |  |  |  |  |\n")
	o, err := Load(v)
	if err != nil {
		t.Fatalf("锚点缺了不该拦：%v", err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"project.slug.format"})
	if len(o.Projects) != 0 {
		t.Fatalf("没 slug 的行不该进表：%+v", o.Projects)
	}
}

// 同 slug 两行 = 项目身份有歧义。
func TestProjectSlugDuplicate(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, ProjectsFile),
		"| slug | name | domain |\n|---|---|---|\n| x | 甲 | trade |\n| x | 乙 | trade |\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range o.Warnings {
		if w.Rule == "project.slug.duplicate" {
			return
		}
	}
	t.Fatalf("没报 slug 重复：%v", IssueStrings(o.Warnings))
}

// 文件不在 = 还没建项目表，不是错。存量 vault 一个发现都不该多。
func TestProjectsAbsentIsFine(t *testing.T) {
	o, err := Load(fixture(t, "one"))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Projects) != 0 || len(o.Warnings) != 0 {
		t.Fatalf("one 没有 projects.md，不该有项目也不该有发现：%+v / %v", o.Projects, IssueStrings(o.Warnings))
	}
}

// 有文件但表建歪：报 table.missing，仍然不拦。
func TestProjectFileWithoutTable(t *testing.T) {
	v := copyVault(t, "one")
	writeAt(t, filepath.Join(v, ProjectsFile), "# 项目表\n\n（还没建表）\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"project.table.missing"})
}

// 默认 warn 允许上调成 fatal：客户想卡死时就该能卡死（与域表同一条路径）。
func TestProjectRuleRaisableToFatal(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, ProjectsFile),
		"| slug | name | domain |\n|---|---|---|\n| Bad-Slug | 甲 | trade |\n")
	addPolicy(t, v, "project.slug.format: fatal")
	_, err := Load(v)
	assertRule(t, err, "project.slug.format")
}

// 指纹要覆盖项目表：改了 owner，重渲染判据必须变（否则漂移检测看不见项目归属的变化）。
func TestProjectParticipatesInInputsHash(t *testing.T) {
	host := Host{VaultRoot: "/vault", HomesRoot: "/homes", DataDir: "/data"}
	v := copyVault(t, "domains")
	before, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	writeAt(t, filepath.Join(v, ProjectsFile),
		"| slug | name | domain | owner |\n|---|---|---|---|\n| trade-q3 | Q3 结算改造 | trade | ops |\n")
	after, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if before.InputsHash(host) == after.InputsHash(host) {
		t.Fatal("改了项目表但指纹没变 —— 漂移检测会漏掉项目归属")
	}
}

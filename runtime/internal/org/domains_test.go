package org

import (
	"os"
	"path/filepath"
	"testing"
)

// writeAt 往 vault 里落一个文件（测试内部用；建目录 + 写，失败直接 Fatal）。
func writeAt(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 域表是「罗盘」的落点：先保证读得出来。
func TestLoadDomainsTable(t *testing.T) {
	o, err := Load(fixture(t, "domains"))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Domains) != 2 {
		t.Fatalf("期望 2 个域，实际 %d：%+v", len(o.Domains), o.Domains)
	}
	d, ok := o.Domain("trade")
	if !ok {
		t.Fatal("取不到 trade 域")
	}
	if d.Name != "大宗贸易" || d.What == "" || d.Who != "manager" || d.Data != "projects" || d.Terms == "" {
		t.Fatalf("域字段没解对：%+v", d)
	}
	if d.Line == 0 {
		t.Fatal("没记住行号 —— 报错就没法定位")
	}
	m, _ := o.Member("alice")
	if len(m.Domains) != 1 || m.Domains[0] != "trade" {
		t.Fatalf("成员的 domains 没读进来：%+v", m.Domains)
	}
}

// 列按表头认，不按位置猜 —— 换个列序照样读对（表格是可以手改的）。
func TestDomainColumnsFollowHeader(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, DomainsFile),
		"| what | who | slug | data |\n|---|---|---|---|\n| 签合同 | manager | trade | projects |\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Domains) != 1 {
		t.Fatalf("期望 1 个域，实际 %+v", o.Domains)
	}
	g := o.Domains[0]
	if g.Slug != "trade" || g.What != "签合同" || g.Who != "manager" || g.Data != "projects" {
		t.Fatalf("列序改了就读错：%+v", g)
	}
}

// 跨表校验：岗位、目录、成员写的域 —— 三条都只是 warn。域表是新能力，先观察不设红线。
func TestDomainCrossChecksWarnOnly(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, DomainsFile),
		"| slug | name | what | who | data |\n|---|---|---|---|---|\n| trade | 大宗贸易 | 签合同 | 不存在的岗 | 不存在的目录 |\n")
	o, err := Load(v)
	if err != nil {
		t.Fatalf("跨表问题默认只告警，不该拦：%v", err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{
		"domain.who.unknown_role", "domain.data.unknown_dir", "member.domains.unknown",
	})
}

// 同 slug 两行 = 域身份有歧义。
func TestDomainSlugDuplicate(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, DomainsFile),
		"| slug | name | what | who | data |\n|---|---|---|---|---|\n| trade | 甲 | x | manager | projects |\n| trade | 乙 | y | manager | projects |\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range o.Warnings {
		if w.Rule == "domain.slug.duplicate" {
			return
		}
	}
	t.Fatalf("没报 slug 重复：%v", IssueStrings(o.Warnings))
}

// 文件不在 = 没划域，不是错。存量 vault 一个发现都不该多。
func TestDomainTableAbsentIsFine(t *testing.T) {
	o, err := Load(fixture(t, "one"))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Domains) != 0 || len(o.Warnings) != 0 {
		t.Fatalf("one 没有 domains.md，不该有域也不该有发现：%+v / %v", o.Domains, IssueStrings(o.Warnings))
	}
}

// 有文件但表建歪：报 table.missing，仍然不拦。
func TestDomainFileWithoutTable(t *testing.T) {
	v := copyVault(t, "one")
	writeAt(t, filepath.Join(v, DomainsFile), "# 业务域\n\n（表还没建）\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"domain.table.missing"})
}

// 默认 warn 允许上调成 fatal：域就这么点东西，公司想卡死时就该能卡死。
func TestDomainRuleRaisableToFatal(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, DomainsFile),
		"| slug | name | what | who | data |\n|---|---|---|---|---|\n| Trade | 大写 slug | 签合同 | manager | projects |\n")
	addPolicy(t, v, "domain.slug.format: fatal")
	_, err := Load(v)
	assertRule(t, err, "domain.slug.format")
}

// 指纹要覆盖域表：改了 who，重渲染判据必须变（否则漂移检测看不见域表的变化）。
func TestDomainParticipatesInInputsHash(t *testing.T) {
	host := Host{VaultRoot: "/vault", HomesRoot: "/homes", DataDir: "/data"}
	v := copyVault(t, "domains")
	before, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	writeAt(t, filepath.Join(v, DomainsFile),
		"| slug | name | what | who | data |\n|---|---|---|---|---|\n| trade | 大宗贸易 | 签合同 | ops | projects |\n| logistics | 物流 | 订舱 | ops | shipments |\n")
	after, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if before.InputsHash(host) == after.InputsHash(host) {
		t.Fatal("改了域表但指纹没变 —— 漂移检测会漏掉域表")
	}
}

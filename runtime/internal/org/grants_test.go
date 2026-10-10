package org

import (
	"path/filepath"
	"testing"
)

// OK 是最小正例：SPEC §6 那七个字段一个不少。
const grantOK = `---
grant:
  from: member:alice
  to: role:ops
  action: read
  object: shipments
  ttl: 30d
  reason: 物流看板要对读数
---

正文随便写，ANC 不解析它。
`

func writeGrant(t *testing.T, vault, slug, text string) {
	t.Helper()
	writeAt(t, filepath.Join(vault, GrantsDir, slug+".md"), text)
}

// 正例：一个 grant 一个文件，字段原样读出来，没发现就是没发现。
func TestScanGrants(t *testing.T) {
	v := copyVault(t, "domains")
	writeGrant(t, v, "read-shipments", grantOK)
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Grants) != 1 {
		t.Fatalf("期望 1 条授权，实际 %d：%+v", len(o.Grants), o.Grants)
	}
	g := o.Grants[0]
	if g.Slug != "read-shipments" || g.Path != "grants/read-shipments.md" {
		t.Fatalf("slug / path 不对：%q / %q", g.Slug, g.Path)
	}
	if g.From != "member:alice" || g.To != "role:ops" || g.Action != "read" ||
		g.Object != "shipments" || g.TTL != "30d" || g.Reason != "物流看板要对读数" {
		t.Fatalf("字段没读全：%+v", g)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{})
}

// 分层是给人看的：递归扫，顺序按路径定（快照 / diff 才有确定字节）。
func TestScanGrantsRecursesAndSorts(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, GrantsDir, "trade", "read.md"), grantOK)
	writeAt(t, filepath.Join(v, GrantsDir, "logistics", "write.md"), grantOK)
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Grants) != 2 {
		t.Fatalf("递归没扫到：%+v", o.Grants)
	}
	if o.Grants[0].Path != "grants/logistics/write.md" || o.Grants[1].Path != "grants/trade/read.md" {
		t.Fatalf("顺序不对：%q / %q", o.Grants[0].Path, o.Grants[1].Path)
	}
}

// 四个必填字段缺一个报一个：缺字段是**写得不对**，不是「没给这条权」。
// **期限不在这四个里面**（2026-10-10 改口径：可选、不写 = 永久），见下一条用例。
func TestGrantMissingFieldsWarn(t *testing.T) {
	v := copyVault(t, "domains")
	writeGrant(t, v, "half", "---\ngrant:\n  from: member:alice\n---\n")
	o, err := Load(v)
	if err != nil {
		t.Fatalf("缺字段不该拦（整组默认 warn）：%v", err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{
		"grant.action.missing", "grant.object.missing", "grant.to.missing",
	})
}

// 期限可选：不写 = 永久。所以「另四个字段齐了」就是一条干净授权，出厂零发现。
func TestGrantWithoutTTLIsCleanByDefault(t *testing.T) {
	v := copyVault(t, "domains")
	writeGrant(t, v, "forever", "---\ngrant:\n  from: member:alice\n  to: role:ops\n  action: read\n  object: shipments\n---\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Grants) != 1 {
		t.Fatalf("期望 1 条授权，实际 %d：%+v", len(o.Grants), o.Grants)
	}
	if o.Grants[0].TTL != "" {
		t.Fatalf("没写期限就该是空（= 永久），实际 %q", o.Grants[0].TTL)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{})
}

// 出厂档 off ≠ 这条规则没了：客户想要「授权必带期限」，在 company.md 里开一下就到。
func TestGrantTTLMissingCanBeTurnedOn(t *testing.T) {
	v := copyVault(t, "domains")
	writeGrant(t, v, "forever", "---\ngrant:\n  from: member:alice\n  to: role:ops\n  action: read\n  object: shipments\n---\n")
	addPolicy(t, v, "grant.ttl.missing: warn")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"grant.ttl.missing"})
}

// from 解不出 actor = 这条链是断的。域不能当 from —— 域是客体，不会授权。
func TestGrantFromMustBeActor(t *testing.T) {
	for _, tc := range []struct {
		name, from string
		want       []string
	}{
		{"member:alice", "member:alice", nil},
		{"role:ops", "role:ops", nil},
		{"裸名字", "alice", nil},
		{"裸名字优先解成员", "devbot", nil},
		{"不存在的人", "member:nobody", []string{"grant.from.unknown"}},
		{"域不能当授权者", "domain:trade", []string{"grant.from.unknown"}},
		{"认不出的前缀", "team:alice", []string{"grant.from.unknown"}},
	} {
		v := copyVault(t, "domains")
		writeGrant(t, v, "g", "---\ngrant:\n  from: "+tc.from+"\n  to: role:ops\n  action: read\n  object: shipments\n  ttl: 30d\n---\n")
		o, err := Load(v)
		if err != nil {
			t.Fatalf("%s：不该拦：%v", tc.name, err)
		}
		assertSameSet(t, tc.name, ruleIDs(o.Warnings), tc.want)
	}
}

// 认不出的 action 照收、只报（词表不锁死，同 envelope.kind）。
func TestGrantActionUnknownWarnsOnly(t *testing.T) {
	v := copyVault(t, "domains")
	writeGrant(t, v, "g", "---\ngrant:\n  from: member:alice\n  to: role:ops\n  action: bless\n  object: shipments\n  ttl: 30d\n---\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"grant.action.unknown"})
	if len(o.Grants) != 1 || o.Grants[0].Action != "bless" {
		t.Fatalf("认不出的 action 应当照收：%+v", o.Grants)
	}
}

// 落点里没有 frontmatter 的 markdown：宁可报错，不静默丢。
func TestGrantFileWithoutFrontmatterWarns(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, GrantsDir, "随手记.md"), "没写 frontmatter 的一坨。\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"grant.file.unparsable", "grant.slug.format"})
	if len(o.Grants) != 0 {
		t.Fatalf("读不出的文件不该变成条目：%+v", o.Grants)
	}
}

// 一个文件里塞两条：第二段读不到 —— 必须报，否则就是一条授权静默消失。
func TestGrantSecondBlockWarns(t *testing.T) {
	v := copyVault(t, "domains")
	writeGrant(t, v, "two", grantOK+"\n---\ngrant.from: member:bob\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"grant.file.multiple"})
}

// 正文里出现横线是常态（分隔线），不该被当成第二段。
func TestGrantBodyDividerIsNotASecondBlock(t *testing.T) {
	v := copyVault(t, "domains")
	writeGrant(t, v, "one", grantOK+"\n---\n\n上面是分隔线，不是第二段。\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{})
}

// 落点自己的说明文件不是授权条目（与 charters/ 同一约定）。
func TestGrantNotesFilesIgnored(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, GrantsDir, "CLAUDE.md"), "# grants\n\n落点说明。\n")
	writeAt(t, filepath.Join(v, GrantsDir, "README.md"), "随手放的说明。\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Grants) != 0 {
		t.Fatalf("说明文件被当成了授权：%+v", o.Grants)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{})
}

// 目录不在 = 零条授权 ≠ 错（SPEC §6 不变量 1）。
func TestGrantsAbsentIsFine(t *testing.T) {
	o, err := Load(fixture(t, "one"))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Grants) != 0 || len(o.Warnings) != 0 {
		t.Fatalf("one 没有 grants/，不该有条目也不该有发现：%+v / %v", o.Grants, IssueStrings(o.Warnings))
	}
}

// 授权表是控制面，不是业务资料：它不许进 persona 段 3 的路由表。
func TestGrantsNotInRouting(t *testing.T) {
	v := copyVault(t, "domains")
	writeGrant(t, v, "read-shipments", grantOK)
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range o.Routing {
		if r.Dir == GrantsDir {
			t.Fatal("grants/ 进了路由表 —— agent 会照表去一个没有业务资料的地方翻")
		}
	}
}

// 指纹要覆盖授权：多了 / 少了 / 改了一条，重渲染判据必须变。
func TestGrantParticipatesInInputsHash(t *testing.T) {
	host := Host{VaultRoot: "/vault", HomesRoot: "/homes", DataDir: "/data"}
	v := copyVault(t, "domains")
	before, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	writeGrant(t, v, "read-shipments", grantOK)
	after, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if before.InputsHash(host) == after.InputsHash(host) {
		t.Fatal("多了一条授权但指纹没变 —— 漂移检测会漏掉授权面")
	}
}

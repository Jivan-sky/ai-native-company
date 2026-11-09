package org

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 不带 domains 的成员。其余字段照抄夹具，保证红档只可能来自「没划域」这一条。
const personaNoDomains = `---
name: %s
display_name: Bob Li
role: ops
reports_to: alice
feishu:
  app_id: cli_demo_%s
  open_id: ou_demo_%s
  extra_allow_from: []
  allow_chat: []
model: ""
admin: %s
disabled: %s
---
- 关注面：东南亚港口
`

func personaWithoutDomains(name, admin, disabled string) string {
	return fmt.Sprintf(personaNoDomains, name, name, name, admin, disabled)
}

// 一个启用中的成员、一个域都没划，而公司**有域表** —— 这个人在这家公司里没有一块业务。
//
// 2026-11-09 之前这只是一句 note（`context pack` 里那句「还没给你划域」），`anc org check` 照样绿：
// 跑首次基线时 12 条 need 全灭，同一次校验报 OK。这类「校验绿、人却用不了」的假绿必须变成红档。
func TestMemberWithoutDomainsIsFatal(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, "members", "bob", "persona.md"), personaWithoutDomains("bob", "false", "false"))
	_, err := Load(v)
	assertRule(t, err, "member.domains.empty")
}

// 豁免只有两类：停用的人（本来就不在跑）与 devbot（下一支用例）。
// **管理员不豁免**：权限层给了全量视野，但工作上下文（ctxpack）只按 domains 装配 ——
// 一个没划域的管理员打开自己的上下文同样是空的。判据跟着交付走，不跟着权限走。
func TestDomainRuleExemptsDisabled(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, "members", "bob", "persona.md"), personaWithoutDomains("bob", "false", "true"))
	if _, err := Load(v); err != nil {
		t.Fatalf("停用的人应当被豁免，实际被拦：%v", err)
	}
}

func TestDomainRuleDoesNotExemptAdmin(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, "members", "bob", "persona.md"), personaWithoutDomains("bob", "true", "false"))
	_, err := Load(v)
	assertRule(t, err, "member.domains.empty")
}

// 豁免：devbot 是系统自动生成的那个，干的是装配与运维，不是业务 —— 与 allowed_tools 那条同一口径。
func TestDomainRuleExemptsDevbot(t *testing.T) {
	v := copyVault(t, "domains")
	// 夹具里的 devbot 本来就没写 domains；这条断言它不因此变红。
	o, err := Load(v)
	if err != nil {
		t.Fatalf("devbot 不该因为没划域被拦：%v", err)
	}
	if _, ok := o.Member("devbot"); !ok {
		t.Fatal("夹具里应当有 devbot")
	}
}

// 没有域表 = 还没划域，成员当然划不了 —— 这时不该报在每个人头上（那是另一件事）。
func TestDomainRuleSilentWhenNoDomainTable(t *testing.T) {
	v := copyVault(t, "domains")
	if err := os.Remove(filepath.Join(v, DomainsFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(v); err != nil {
		t.Fatalf("没有域表时不该拦：%v", err)
	}
}

// 存量 vault 的出口：公司可以在 policy 段把这条降档（同 member.name.format 的做法）。
// 降档不等于消失 —— warn 必须回显出来。
func TestDomainRuleCanBeDowngraded(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, "members", "bob", "persona.md"), personaWithoutDomains("bob", "false", "false"))
	addPolicy(t, v, "member.domains.empty: warn")
	o, err := Load(v)
	if err != nil {
		t.Fatalf("降档之后不该再拦：%v", err)
	}
	for _, i := range o.Warnings {
		if i.Rule == "member.domains.empty" {
			return
		}
	}
	t.Fatalf("降档不等于消失：warn 必须回显出来，实际 %v", ruleIDs(o.Warnings))
}

// 岗位不是主体：一条 `to: role:xxx` 的授权谁都不命中，渲染也不展开它。
// 2026-11-09 之前它一声不吭 —— 照 README 示例写的人会以为给了权限。
func TestGrantToRoleWarns(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, "grants", "role-read.md"), `---
grant:
  from: member:alice
  to: role:ops
  action: read
  object: projects
---
一条写给岗位的授权（不会被展开成任何人）。
`)
	o, err := Load(v)
	if err != nil {
		t.Fatalf("授权表问题不该拦加载：%v", err)
	}
	for _, i := range o.Warnings {
		if i.Rule == "grant.to.role" {
			return
		}
	}
	t.Fatalf("to: role:... 必须被点出来（否则「以为给了、其实没给」），实际 %v", ruleIDs(o.Warnings))
}

package org

import (
	"path/filepath"
	"strings"
	"testing"
)

// agentRow 造一行**字段齐全**的业务 agent 表行（表头由 agentVault 给足八列）。
// 用例只改它关心的那一格 —— 其余保持干净，断言里就不会混进一串无关的 warn。
func agentRow(slug, name, domain, harness, tools, model, role, appID string) string {
	return "| " + strings.Join([]string{slug, name, domain, harness, tools, model, role, appID}, " | ") + " |\n"
}

// agentVault 造一个「domains 域表 + 一张业务 agent 表」的 vault。
// 业务 agent 表的落点只有 agents.md 一处，用例里也只在那一处改。
func agentVault(t *testing.T, rows string) string {
	t.Helper()
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, AgentsFile),
		"| slug | name | domain | harness | tools | model | role | app_id |\n"+
			"|---|---|---|---|---|---|---|---|\n"+rows)
	return v
}

// agents.md 是 SPEC §2.3 第二类的落点：先保证读得出来，字段能被渲染与校验两层共用。
func TestLoadAgentsTable(t *testing.T) {
	v := agentVault(t, agentRow("order-bot", "订单处理", "trade", "codex", "read,bash", "claude-sonnet-5", "manager", "cli_demo_order"))
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Agents) != 1 {
		t.Fatalf("期望 1 个业务 agent，实际 %d：%+v", len(o.Agents), o.Agents)
	}
	a, ok := o.Agent("order-bot")
	if !ok {
		t.Fatal("取不到 order-bot")
	}
	if a.Name != "订单处理" || a.Domain != "trade" || a.Harness != "codex" || a.Model != "claude-sonnet-5" {
		t.Fatalf("字段没解对：%+v", a)
	}
	if a.Role != "manager" || a.AppID != "cli_demo_order" {
		t.Fatalf("role / app_id 没解对：%+v", a)
	}
	if a.Line == 0 {
		t.Fatal("没记住行号 —— 报错就没法定位")
	}
	if got := o.AgentTools(a); len(got) != 2 || got[0] != "read" || got[1] != "bash" {
		t.Fatalf("tools 没拆对：%v", got)
	}
}

// 列按表头认，不按位置猜 —— 业务 agent 表是人手维护的，换列序要照样读对。
func TestAgentColumnsFollowHeader(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, AgentsFile),
		"| tools | domain | slug | role |\n|---|---|---|---|\n| read,write | trade | order-bot | manager |\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := o.Agent("order-bot")
	if !ok {
		t.Fatalf("列序改了就读不到：%+v", o.Agents)
	}
	if a.Domain != "trade" || a.Tools != "read,write" || a.Role != "manager" {
		t.Fatalf("列序改了就读错：%+v", a)
	}
}

// 跨表校验：domain 指向的域不在 domains.md 里 —— 只告警，不拦。
func TestAgentCrossChecksWarnOnly(t *testing.T) {
	v := agentVault(t, agentRow("order-bot", "订单处理", "不存在的域", "codex", "read", "", "manager", "cli_demo_order"))
	o, err := Load(v)
	if err != nil {
		t.Fatalf("跨表问题默认只告警，不该拦：%v", err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"agent.domain.unknown"})
	// 定位串要指到 agents.md 的那一行 —— 现场靠它去改表。
	for _, s := range IssueStrings(o.Warnings) {
		if !strings.Contains(s, AgentsFile) {
			t.Fatalf("定位串里没有 %s：%v", AgentsFile, IssueStrings(o.Warnings))
		}
	}
}

// role 指不到岗位 / 根本没写：都只告警 —— 但 persona 的职责那一段会空着，所以要说出来。
func TestAgentRoleChecks(t *testing.T) {
	t.Run("role 不在 roles/ 里", func(t *testing.T) {
		v := agentVault(t, agentRow("order-bot", "订单处理", "trade", "codex", "read", "", "不存在的岗", "cli_demo_order"))
		o, err := Load(v)
		if err != nil {
			t.Fatal(err)
		}
		assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"agent.role.unknown"})
	})
	t.Run("role 空着", func(t *testing.T) {
		v := agentVault(t, agentRow("order-bot", "订单处理", "trade", "codex", "read", "", "", "cli_demo_order"))
		o, err := Load(v)
		if err != nil {
			t.Fatal(err)
		}
		assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"agent.role.missing"})
	})
}

// project 名两边同源（<公司 id>-<名字>）：业务 agent 与成员同名 = 配置里两个 project 抢一个名字。
// 这不是学术问题 —— members/ 里本来就有 ASCII 名（devbot）。
func TestAgentSlugCollidesMemberWarns(t *testing.T) {
	v := agentVault(t, agentRow("devbot", "开发助手", "trade", "codex", "read", "", "manager", "cli_demo_dev"))
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"agent.slug.collides_member"})
}

// 同 slug 两行 = 业务 agent 身份有歧义。
func TestAgentSlugDuplicate(t *testing.T) {
	v := agentVault(t,
		agentRow("order-bot", "甲", "trade", "codex", "read", "", "manager", "cli_a")+
			agentRow("order-bot", "乙", "trade", "codex", "read", "", "manager", "cli_b"))
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range o.Warnings {
		if w.Rule == "agent.slug.duplicate" {
			return
		}
	}
	t.Fatalf("没报 slug 重复：%v", IssueStrings(o.Warnings))
}

// 文件不在 = 这家公司还没有业务 agent，不是错。存量 vault 一个发现都不该多。
func TestAgentTableAbsentIsFine(t *testing.T) {
	o, err := Load(fixture(t, "one"))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Agents) != 0 || len(o.Warnings) != 0 {
		t.Fatalf("one 没有 agents.md，不该有业务 agent 也不该有发现：%+v / %v", o.Agents, IssueStrings(o.Warnings))
	}
}

// 有文件但表建歪：报 table.missing，仍然不拦。
func TestAgentFileWithoutTable(t *testing.T) {
	v := copyVault(t, "one")
	writeAt(t, filepath.Join(v, AgentsFile), "# 业务 agent\n\n（表还没建）\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"agent.table.missing"})
}

// 空白名单 = 连得上却干不了活（实测：dontAsk 下未预授权的工具一律自动拒绝）。
// SPEC §4.7 ③ 的红线，默认拦死；但**不锁死** —— 想先观察的公司可以降档。
func TestAgentToolsEmptyIsFatal(t *testing.T) {
	tools := agentRow("order-bot", "订单处理", "trade", "codex", "", "", "manager", "cli_demo_order")

	v := agentVault(t, tools)
	_, err := Load(v)
	assertRule(t, err, "agent.tools.empty")

	// 降档后放行：同一条不变量换档不改实现（档次只由 company.md 的 policy 定）。
	v2 := agentVault(t, tools)
	addPolicy(t, v2, "agent.tools.empty: warn")
	o, err := Load(v2)
	if err != nil {
		t.Fatalf("降档后不该拦：%v", err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"agent.tools.empty"})
}

// 默认 warn 允许上调成 fatal：slug 会当 OS 账号名，公司想卡死时就该能卡死。
func TestAgentRuleRaisableToFatal(t *testing.T) {
	v := agentVault(t, agentRow("Order-Bot", "订单处理", "trade", "codex", "read", "", "manager", "cli_demo_order"))
	addPolicy(t, v, "agent.slug.format: fatal")
	_, err := Load(v)
	assertRule(t, err, "agent.slug.format")
}

// 指纹要覆盖业务 agent 表：改 model 或 app_id，重渲染判据都必须变（否则漂移检测看不见）。
func TestAgentParticipatesInInputsHash(t *testing.T) {
	host := Host{VaultRoot: "/vault", HomesRoot: "/homes", DataDir: "/data"}
	v := agentVault(t, agentRow("order-bot", "订单处理", "trade", "codex", "read", "m1", "manager", "cli_a"))
	before, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{
		agentRow("order-bot", "订单处理", "trade", "codex", "read", "m2", "manager", "cli_a"), // 换 model
		agentRow("order-bot", "订单处理", "trade", "codex", "read", "m1", "manager", "cli_b"), // 换平台绑定
		agentRow("order-bot", "订单处理", "trade", "codex", "read", "m1", "ops", "cli_a"),     // 换岗位
	} {
		writeAt(t, filepath.Join(v, AgentsFile),
			"| slug | name | domain | harness | tools | model | role | app_id |\n"+
				"|---|---|---|---|---|---|---|---|\n"+row)
		after, err := Load(v)
		if err != nil {
			t.Fatal(err)
		}
		if before.InputsHash(host) == after.InputsHash(host) {
			t.Fatalf("改了业务 agent 表但指纹没变（%s）—— 漂移检测会漏掉", row)
		}
	}
}

// AgentHomes 是**本机事实**（这台机器上这个 agent 落在哪）。没配 = 还没落下来，
// 如实报缺；不许拿 HomesRoot 拼一个看着像的路径顶上（会在别人家目录里建夹子）。
func TestAgentHomeFromHostFacts(t *testing.T) {
	var none Host
	if _, ok := none.AgentHome("order-bot"); ok {
		t.Fatal("没配 AgentHomes 时不该给出目录")
	}
	blank := Host{AgentHomes: map[string]string{"order-bot": "   "}}
	if _, ok := blank.AgentHome("order-bot"); ok {
		t.Fatal("配成空白等于没配")
	}
	h := Host{AgentHomes: map[string]string{"order-bot": "/home/bizbot/vault"}}
	d, ok := h.AgentHome("order-bot")
	if !ok || d != "/home/bizbot/vault" {
		t.Fatalf("没取对 cwd：%q %v", d, ok)
	}
}

// 主机事实同样进指纹：这台机器上业务 agent 的落点变了，渲染结果就该被判为漂移。
func TestAgentHomeParticipatesInInputsHash(t *testing.T) {
	o, err := Load(agentVault(t, agentRow("order-bot", "订单处理", "trade", "codex", "read", "", "manager", "cli_a")))
	if err != nil {
		t.Fatal(err)
	}
	bare := Host{VaultRoot: "/vault", HomesRoot: "/homes", DataDir: "/data"}
	landed := bare
	landed.AgentHomes = map[string]string{"order-bot": "/home/bizbot/vault"}
	if o.InputsHash(bare) == o.InputsHash(landed) {
		t.Fatal("业务 agent 的落点变了但指纹没变 —— 跨机同步时会看不出来")
	}
}

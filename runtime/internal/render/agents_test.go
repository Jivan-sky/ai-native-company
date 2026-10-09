package render

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"anc/internal/org"
)

// agentOrg 造一个「domains 那个 vault + 一张业务 agent 表」的 org。
// 拷到临时目录是必须的：agents.md 按用例写（这一层只测「表怎么进配置」）。
func agentOrg(t *testing.T, rows string) *org.Org {
	t.Helper()
	v := copyOrgFixture(t, "domains")
	text := "| slug | name | domain | harness | tools | model | role | app_id |\n" +
		"|---|---|---|---|---|---|---|---|\n" + rows
	if err := os.WriteFile(filepath.Join(v, org.AgentsFile), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := org.Load(v)
	if err != nil {
		t.Fatalf("加载带业务 agent 的 vault: %v", err)
	}
	return o
}

// hostWith 是**本机事实**那一层：业务 agent 在这台机器上的 cwd（不进 git）。
func hostWith(homes map[string]string) org.Host {
	return org.Host{VaultRoot: "/vault", HomesRoot: "/homes", DataDir: "/data", AgentHomes: homes}
}

const orderBotRow = "| order-bot | 订单处理 | trade | codex | Read,Grep |  | manager | cli_demo_order |\n"

// 一个业务 agent 就是一个 project（SPEC §4.7 ③）：名字 / 腿 / cwd / 工具 / 平台绑定 / 收件人一次核齐。
// 同时钉住最要紧的一条：它是**加一个 project**，不是把成员 bot 换掉。
func TestBuildBusinessAgentProject(t *testing.T) {
	o := agentOrg(t, orderBotRow)
	plan, err := Build(o, Options{
		Host: hostWith(map[string]string{"order-bot": "/home/order-bot"}), Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.Projects, "demo-order-bot") {
		t.Fatalf("业务 agent 没进 project 列表：%v", plan.Projects)
	}
	if !slices.Contains(plan.Projects, "demo-alice") {
		t.Fatalf("成员 bot 被挤掉了：%v", plan.Projects)
	}
	for _, want := range []string{
		"name = \"demo-order-bot\"",
		"\n[projects.agent]\ntype = \"codex\"\n", // harness 列能选另一条腿（§4.7 H2/H3 的消费方）
		"work_dir = \"/home/order-bot\"",         // 本机事实那一格
		"allowed_tools = [\"Read\", \"Grep\"]",
		"app_id = \"cli_demo_order\"",
		"app_secret = \"${" + AgentSecretKey("order-bot") + "}\"",
		"allow_from = \"ou_demo_alice\"", // trade 的 who 是 manager（alice）；admins 也是 alice
	} {
		if !strings.Contains(plan.Text, want) {
			t.Fatalf("产物里没有 %q：\n%s", want, plan.Text)
		}
	}
	// 凭据账：产物要的键里有它的 —— 装载前的体检吃这一份账，不从产物文本回读。
	if !slices.Contains(plan.SecretKeys, AgentSecretKey("order-bot")) {
		t.Fatalf("凭据账里没有业务 agent 的键：%v", plan.SecretKeys)
	}
	// trail 要把 project 对回 cwd（目录名 = work_dir 里每个非字母数字换 '-'）。
	if d := WorkDirs(plan.Text)["demo-order-bot"]; d != "/home/order-bot" {
		t.Fatalf("WorkDirs 没把业务 agent 对上 cwd：%q", d)
	}
}

// 收件人按**岗位**取人（不是「admins 兜底」）：logistics 的 who 是 ops → bob，再加 admins（alice）。
func TestBuildAgentRecipientsComeFromDomainRole(t *testing.T) {
	o := agentOrg(t, "| ship-bot | 订舱 | logistics |  | Bash,Read | claude-sonnet-5 | ops | cli_demo_ship |\n")
	plan, err := Build(o, Options{
		Host: hostWith(map[string]string{"ship-bot": "/home/ship-bot"}), Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Text, "allow_from = \"ou_demo_bob,ou_demo_alice\"\n") {
		t.Fatalf("收件人不是「who 岗位的人 + admins」：\n%s", plan.Text)
	}
	// harness 空 = 走默认那条腿，不是「没有 type」那一段。
	want := "name = \"demo-ship-bot\"\nreset_on_idle_mins = 0\n\n[projects.agent]\ntype = \"" + DefaultHarness + "\"\n"
	if !strings.Contains(plan.Text, want) {
		t.Fatalf("harness 空着时没走默认腿：\n%s", plan.Text)
	}
}

// 本机没配 cwd = 它还没落到这台机器上：**不渲染**，也不许猜一个目录 —— 但要说出来。
func TestBuildAgentWithoutHomeIsSkipped(t *testing.T) {
	o := agentOrg(t, orderBotRow)
	plan, err := Build(o, Options{Host: hostWith(nil), Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Text, "demo-order-bot") {
		t.Fatal("没配 cwd 却把它渲染出来了 —— 那只能落在猜的目录上")
	}
	if !warnContains(plan.Warns, "--agent-home order-bot") {
		t.Fatalf("没如实报缺：%v", plan.Warns)
	}
}

// role 取不到 = 岗位拼错了：不渲染（persona 的职责那一段取不到），如实报缺。
func TestBuildAgentUnknownRoleIsSkipped(t *testing.T) {
	o := agentOrg(t, "| order-bot | 订单处理 | trade | codex | Read |  | 不存在的岗 | cli_demo_order |\n")
	plan, err := Build(o, Options{
		Host: hostWith(map[string]string{"order-bot": "/home/order-bot"}), Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Text, "demo-order-bot") {
		t.Fatal("role 取不到还是渲染了")
	}
	if !warnContains(plan.Warns, "role=\"不存在的岗\"") {
		t.Fatalf("没如实报缺：%v", plan.Warns)
	}
}

// 没写 app_id = 声明了还没接线：与成员的 unwired 同档 —— 照渲染（灰），不假装绿。
func TestBuildUnwiredAgentIsGray(t *testing.T) {
	o := agentOrg(t, "| order-bot | 订单处理 | trade | codex | Read |  | manager |  |\n")
	plan, err := Build(o, Options{
		Host: hostWith(map[string]string{"order-bot": "/home/order-bot"}), Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.Projects, "demo-order-bot") {
		t.Fatalf("没接线的业务 agent 也该照渲染（灰档）：%v", plan.Projects)
	}
	if !slices.Contains(UnwiredProjects(o), "demo-order-bot") {
		t.Fatalf("没接线的业务 agent 没进灰档：%v", UnwiredProjects(o))
	}
}

// 键名是**一处派生**：改了它，现场 secrets.env 里那把凭据就对不上号 —— 所以钉字面量。
func TestAgentSecretKeyStable(t *testing.T) {
	if got := AgentSecretKey("order-bot"); got != "ANC_FEISHU_SECRET_ORDERx00002dBOT" {
		t.Fatalf("键名派生变了：%q", got)
	}
	if AgentSecretKey("a") == AgentSecretKey("b") {
		t.Fatal("不同 slug 派生出同一个键名")
	}
}

func warnContains(warns []string, sub string) bool {
	for _, w := range warns {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

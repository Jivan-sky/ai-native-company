package org

import (
	"strings"
	"testing"
)

// identityOrg 是一棵**手搓**的真相源：不落盘，只为了把「平台那三样各解到谁」钉住。
// 里面刻意留着几种「解不出」的样本（没域的 agent / 域不在表里 / 同岗位没人 / 停用的人），
// 它们比正例重要 —— 平台身份猜错的代价是后面每一条留痕都记在错的人头上。
func identityOrg() *Org {
	return &Org{
		Company: Company{ID: "demo", Admins: []string{"alice"}},
		Roles: map[string]Role{
			"manager": {Title: "经理"},
			"ops":     {Title: "运营"},
		},
		Members: []Member{
			{Name: "alice", DisplayName: "Alice Wang", Role: "manager", Domains: []string{"trade"},
				Feishu: Feishu{AppID: "cli_demo_alice", OpenID: "ou_demo_alice"}},
			{Name: "bob", DisplayName: "Bob Li", Role: "ops", Domains: []string{"logistics"},
				Feishu: Feishu{AppID: "cli_demo_bob", OpenID: "ou_demo_bob"}},
			{Name: "carol", Role: "manager"},            // 同岗位第二个人
			{Name: "dave", Role: "ops", Disabled: true}, // 停用：不该进归属
		},
		Domains: []Domain{
			{Slug: "trade", Name: "大宗贸易", Who: "manager"},
			{Slug: "logistics", Name: "物流", Who: "ops"},
			{Slug: "empty", Name: "没人管的域", Who: "nobody"}, // who 岗位上没有启用成员
		},
		Agents: []Agent{
			{Slug: "tradebot", Name: "贸易助手", Domain: "trade", Role: "manager",
				Description: "替 manager 岗管进出口合同", AppID: "cli_tradebot"},
			{Slug: "lonelybot", Name: "没人管的", Domain: "empty", Role: "nobody"},
			{Slug: "nodomain", Name: "没域的", Role: "ops"},
			{Slug: "ghostdomain", Name: "域不在表里", Domain: "nope", Role: "ops"},
		},
	}
}

func TestByOpenIDLandsOnThePerson(t *testing.T) {
	o := identityOrg()
	got, ok := o.ByOpenID("ou_demo_alice")
	if !ok {
		t.Fatal("ou_demo_alice 该解得出")
	}
	if got.Kind != KindMember || got.Ref != "alice" || got.Name != "Alice Wang" {
		t.Fatalf("该解出成员 alice（显示名 Alice Wang），得到 %+v", got)
	}
	if got.Role != "manager" || len(got.Domains) != 1 || got.Domains[0] != "trade" {
		t.Errorf("岗位 / 域该跟着人走，得到 role=%q domains=%v", got.Role, got.Domains)
	}

	// 自报的身份不算：认不出就是认不出，不许猜一个最像的顶上。
	for _, bad := range []string{"", "   ", "ou_someone_else"} {
		if id, ok := o.ByOpenID(bad); ok {
			t.Errorf("open_id=%q 该解不出，却得到 %+v", bad, id)
		}
	}
}

func TestByAppIDFindsBothKindsOfBot(t *testing.T) {
	o := identityOrg()
	if got, ok := o.ByAppID("cli_demo_bob"); !ok || got.Kind != KindMember || got.Ref != "bob" {
		t.Errorf("成员 bot 的 app_id 该解到人，得到 %+v（ok=%v）", got, ok)
	}
	got, ok := o.ByAppID("cli_tradebot")
	if !ok || got.Kind != KindAgent || got.Ref != "tradebot" {
		t.Fatalf("业务 agent 的 app_id 该解到 agent，得到 %+v（ok=%v）", got, ok)
	}
	if got.Inscription == "" {
		t.Error("业务 agent 该带上铭文（干什么的 / 替谁干）")
	}
	if _, ok := o.ByAppID("cli_nobody"); ok {
		t.Error("没接线的 app_id 该解不出")
	}
}

func TestByProjectTakesPrefixedAndBare(t *testing.T) {
	o := identityOrg()
	cases := []struct{ in, kind, ref string }{
		{"demo-alice", KindMember, "alice"},      // 归集器 / 网关递过来的是这种
		{"demo-tradebot", KindAgent, "tradebot"}, // 业务 agent 同一个拼法
		{"alice", KindMember, "alice"},           // 裸编号：人在命令行里就这么写
		{"tradebot", KindAgent, "tradebot"},
	}
	for _, c := range cases {
		got, ok := o.ByProject(c.in)
		if !ok || got.Kind != c.kind || got.Ref != c.ref {
			t.Errorf("ByProject(%q) 该是 %s/%s，得到 %+v（ok=%v）", c.in, c.kind, c.ref, got, ok)
		}
	}
	// 前缀要对齐本公司 id：别人家的同名 project 不许被认成本公司的人。
	for _, bad := range []string{"other-alice", "demo-", "", "demo-nobody"} {
		if got, ok := o.ByProject(bad); ok {
			t.Errorf("ByProject(%q) 该解不出，却得到 %+v", bad, got)
		}
	}
}

func TestOwnerOfAgentIsTheDomainWhoNotTheAdminFallback(t *testing.T) {
	o := identityOrg()
	bot, ok := o.ByProject("demo-tradebot")
	if !ok {
		t.Fatal("tradebot 该解得出")
	}
	who, why := o.Owner(bot)
	if who != "alice、carol" {
		t.Fatalf("该解到域 who 岗位上启用中的两个人，得到 %q（%s）", who, why)
	}
	if !strings.Contains(why, "who") || !strings.Contains(why, "manager") {
		t.Errorf("归属要带上判据原话（哪块业务、哪个岗位），得到 %q", why)
	}
	if strings.Contains(who, "dave") {
		t.Error("停用成员不该出现在归属里")
	}

	// 人没有归属人：他属于他自己 —— 这一格不许拿域 / 岗位糊上去。
	if w, y := o.Owner(mustIdentity(t, o, "alice")); w != "" || y == "" {
		t.Errorf("成员该没有归属人并说明原因，得到 %q / %q", w, y)
	}

	// 三种解不出：岗位上没人 / 没写域 / 域不在表里 —— 都要如实说，且**都不落到 admins**。
	for _, slug := range []string{"lonelybot", "nodomain", "ghostdomain"} {
		id, ok := o.Identity(slug)
		if !ok {
			t.Fatalf("%s 该解得出（解不出的是它的归属）", slug)
		}
		if w, y := o.Owner(id); w != "" || y == "" {
			t.Errorf("%s 该解不出归属并说明原因，得到 %q / %q", slug, w, y)
		}
	}
}

func mustIdentity(t *testing.T, o *Org, ref string) Identity {
	t.Helper()
	id, ok := o.Identity(ref)
	if !ok {
		t.Fatalf("%s 该解得出", ref)
	}
	return id
}

func TestProjectNameHasOneDefinition(t *testing.T) {
	if got := ProjectName("demo", "alice"); got != "demo-alice" {
		t.Fatalf("拼法该是 <公司 id>-<名字>，得到 %q", got)
	}
}

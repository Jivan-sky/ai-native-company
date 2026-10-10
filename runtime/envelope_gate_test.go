package main

import (
	"os"
	"path/filepath"
	"testing"

	"anc/internal/audit"
	"anc/internal/envelope"
	"anc/internal/org"
)

// ---------- 授权执行层第一刀：跨域信封进不进得来 ----------
//
// 这一组用例盯的是**业务判据**，不是实现细节：
//   ① 自己域内 / 没写域 → 不通 grant（不该因为「授权表是空的」被误伤）
//   ② 跨域 + 没有 grant → 拒（出厂档就是拦）
//   ③ 跨域 + 有 grant → 通（而且要认出三种 to 的写法、两种 object 的写法）
//   ④ 主体按 on_behalf_of 判（bot 不自有权限，SPEC §2.3）
//   ⑤ 一条事实只报一处：主体解不出不由这一刀报
//   ⑥ 开关是数据：company.md 里能把它关掉
//   ⑦ 拒也留痕 / 进了门的跨域也留痕 / 域内不进 audit

func writeGrantFile(t *testing.T, vault, slug, body string) {
	t.Helper()
	dir := filepath.Join(vault, "grants")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// grantFile 拼一份最小授权：四个必填字段 + 一句 reason，正文不写。
func grantFile(from, to, action, object string) string {
	return "---\ngrant:\n  from: " + from + "\n  to: " + to +
		"\n  action: " + action + "\n  object: " + object + "\n  reason: 用例\n---\n"
}

// gateRules 跑一次判权，返回规则 id 列表。
func gateRules(t *testing.T, vault string, e envelope.Envelope) []string {
	t.Helper()
	o, err := org.Load(vault)
	if err != nil {
		t.Fatalf("真相源加载失败：%v", err)
	}
	issues, _ := envelope.Gate(o, e, o.Policy)
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Rule)
	}
	return out
}

func onlyRule(t *testing.T, got []string, want string) {
	t.Helper()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("期望恰好一条 %s，实际 %v", want, got)
	}
}

// ① 没写 scope：说的是自己那一摊，不通 grant。授权表空不空都与它无关。
func TestGateSkipsWhenNoScope(t *testing.T) {
	vault := copyFixture(t, "domains")
	if got := gateRules(t, vault, envelope.Envelope{Who: "alice"}); len(got) != 0 {
		t.Fatalf("没写 scope 不该判权，实际 %v", got)
	}
	if got := gateRules(t, vault, envelope.Envelope{Who: "alice", Scope: envelope.Scope{Project: "trade-q3"}}); len(got) != 0 {
		t.Fatalf("只有 project 没有 domain 也不该判权，实际 %v", got)
	}
}

// ① 写的是自己负责的域：域内不需要 grant（这正是「跨域才要授权」那条的另一面）。
func TestGateSkipsOwnDomain(t *testing.T) {
	vault := copyFixture(t, "domains")
	if got := gateRules(t, vault, envelope.Envelope{Who: "alice", Scope: envelope.Scope{Domain: "trade"}}); len(got) != 0 {
		t.Fatalf("alice 的自己的域（trade）不该要 grant，实际 %v", got)
	}
}

// ② 跨域 + grants/ 里一条都没有 → 拒。出厂档就是拦（与那批「先看见」的规则不同）。
func TestGateRejectsCrossDomainWithoutGrant(t *testing.T) {
	vault := copyFixture(t, "domains")
	got := gateRules(t, vault, envelope.Envelope{Who: "alice", Scope: envelope.Scope{Domain: "logistics"}})
	onlyRule(t, got, "grant.match.missing")

	o, err := org.Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	if lv := o.Policy.Level("grant.match.missing"); lv != org.LevelFatal {
		t.Fatalf("出厂档该是 fatal（SPEC §6 不变量 1「默认拒」），实际 %s", lv)
	}
}

// ③ 跨域 + 有覆盖 → 通。三种 to 写法各来一遍；客体两种写法各来一遍。
func TestGateAcceptsCoveredCrossDomain(t *testing.T) {
	cases := []struct {
		name  string
		grant string
	}{
		{"to 写 member:", grantFile("member:bob", "member:alice", "read", "logistics")},
		{"to 写裸名字", grantFile("member:bob", "alice", "read", "logistics")},
		{"to 写 role:", grantFile("member:bob", "role:manager", "read", "logistics")},
		{"object 带 domain: 前缀", grantFile("member:bob", "member:alice", "read", "domain:logistics")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vault := copyFixture(t, "domains")
			writeGrantFile(t, vault, "cross", c.grant)
			got := gateRules(t, vault, envelope.Envelope{
				Who: "alice", OnBehalfOf: "member:alice",
				Scope: envelope.Scope{Domain: "logistics"},
			})
			if len(got) != 0 {
				t.Fatalf("有覆盖就该通，实际 %v", got)
			}
		})
	}
}

// ③ 授权是给**谁**的要对得上：同一条 grant 换成给别人，就不该通。
func TestGateGrantMustNameTheSubject(t *testing.T) {
	vault := copyFixture(t, "domains")
	writeGrantFile(t, vault, "for-bob", grantFile("member:alice", "member:bob", "read", "logistics"))
	got := gateRules(t, vault, envelope.Envelope{Who: "alice", Scope: envelope.Scope{Domain: "logistics"}})
	onlyRule(t, got, "grant.match.missing")
}

// ③ 客体要对得上：授权给的是别的域，不算覆盖。
func TestGateGrantMustNameTheDomain(t *testing.T) {
	vault := copyFixture(t, "domains")
	writeGrantFile(t, vault, "other-domain", grantFile("member:bob", "member:alice", "read", "trade"))
	got := gateRules(t, vault, envelope.Envelope{Who: "alice", Scope: envelope.Scope{Domain: "logistics"}})
	onlyRule(t, got, "grant.match.missing")
}

// ④ 主体按 on_behalf_of 判（SPEC §2.3：bot 不自有权限，它的权是所代理对象的投影）。
// 这里 who=bob（bob 的域是 logistics），on_behalf_of=member:alice → 按 alice 判 → trade 是她的域 → 通。
// 若按 who 判，就会误报一条拒。
func TestGateJudgesByOnBehalfOfFirst(t *testing.T) {
	vault := copyFixture(t, "domains")
	got := gateRules(t, vault, envelope.Envelope{
		Who: "bob", OnBehalfOf: "member:alice",
		Scope: envelope.Scope{Domain: "trade"},
	})
	if len(got) != 0 {
		t.Fatalf("应当按 on_behalf_of（alice）判，trade 是她的域，实际 %v", got)
	}
}

// ⑤ 一条事实只报一处：主体解不出是 Bind 的活（envelope.who.unknown），
// 这一刀不该跟着喊第二声 —— 否则同一次错在回话里出现两遍，人分不清哪个是要修的。
func TestGateStaysSilentWhenSubjectUnknown(t *testing.T) {
	vault := copyFixture(t, "domains")
	got := gateRules(t, vault, envelope.Envelope{Who: "ghost", Scope: envelope.Scope{Domain: "logistics"}})
	if len(got) != 0 {
		t.Fatalf("主体解不出不该由这一刀报，实际 %v", got)
	}
}

// ⑥ 开关是数据：出厂拦，但客户能先「看见不先拦」—— 门禁是数据，不是代码里的 if。
func TestGateLevelIsData(t *testing.T) {
	vault := copyFixture(t, "domains")
	addPolicyToCompany(t, vault, "grant.match.missing: warn")
	got := gateRules(t, vault, envelope.Envelope{Who: "alice", Scope: envelope.Scope{Domain: "logistics"}})
	onlyRule(t, got, "grant.match.missing")

	o, err := org.Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	if lv := o.Policy.Level("grant.match.missing"); lv != org.LevelWarn {
		t.Fatalf("company.md 覆盖后该是 warn，实际 %s", lv)
	}
}

// ---------- 接入面（serve）：门 + 留痕 ----------

func auditRecords(t *testing.T, vault string) []audit.Record {
	t.Helper()
	d, err := audit.Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Bad) != 0 {
		t.Fatalf("审计里有读不懂的行：%v", d.Bad)
	}
	return d.Records
}

// ⑦ 跨域无授权：拒收（回 isError=true）＋**信封日志不写**＋**audit 留一条 denied**。
// 最后那条是这一格的判据：被拒的信封进不了信封日志，那道痕必须另落一处，
// 否则「谁想递什么、为什么被拒」一个字都不剩。且它要能答出「跨域」——
// 靠 Actor 与 Object（domain:<slug>），不存派生值。
func TestIngressDeniesCrossDomainAndLeavesTrace(t *testing.T) {
	vault := copyFixture(t, "domains")
	data := t.TempDir()
	g := &envelopeIngress{Vault: vault, DataDir: data}
	text, isErr := g.sendEnvelope(map[string]any{
		"who": "alice", "on_behalf_of": "member:alice", "kind": "ask",
		"body": "问一块不是我的业务", "scope_domain": "logistics",
	})
	if !isErr {
		t.Fatalf("跨域无授权应当拒收：%s", text)
	}
	if len(logFiles(t, data)) != 0 {
		t.Fatalf("被拒的信不该进信封日志：%v", logFiles(t, data))
	}
	rs := auditRecords(t, vault)
	if len(rs) != 1 {
		t.Fatalf("拒也要留一条痕，实际 %d 条", len(rs))
	}
	r := rs[0]
	if r.Result != audit.ResultDenied || r.Actor != "alice" || r.Object != "domain:logistics" ||
		r.Tool != "anc_send_envelope" || r.OnBehalfOf != "member:alice" {
		t.Fatalf("这一条痕没答全「谁 / 向谁 / 结果」：%+v", r)
	}
	if r.Why == "" {
		t.Fatalf("拒的理由要原样记下来：%+v", r)
	}
}

// ⑦ 跨域且被授权：照收、进信封日志、audit 留一条 ok（**进去了也记** —— 审计不是只记拒绝）。
func TestIngressAllowsGrantedCrossDomainAndLogsOk(t *testing.T) {
	vault := copyFixture(t, "domains")
	writeGrantFile(t, vault, "cross", grantFile("member:bob", "member:alice", "read", "logistics"))
	data := t.TempDir()
	g := &envelopeIngress{Vault: vault, DataDir: data}
	text, isErr := g.sendEnvelope(map[string]any{
		"who": "alice", "on_behalf_of": "member:alice", "kind": "ask",
		"body": "问一块我拿到授权的业务", "scope_domain": "logistics",
	})
	if isErr {
		t.Fatalf("有 grant 覆盖应当照收：%s", text)
	}
	if names := logFiles(t, data); len(names) != 1 {
		t.Fatalf("照收就该落信封日志：%v", names)
	}
	rs := auditRecords(t, vault)
	if len(rs) != 1 || rs[0].Result != audit.ResultOK {
		t.Fatalf("跨域进了门也要留痕（result=ok），实际 %+v", rs)
	}
}

// ⑦ 域内信封：不通 grant，也就不是一次「跨域 / 跨通道」行使 —— 信封日志记它就够了，不进 audit。
func TestIngressOwnDomainLeavesNoAudit(t *testing.T) {
	vault := copyFixture(t, "domains")
	data := t.TempDir()
	g := &envelopeIngress{Vault: vault, DataDir: data}
	if text, isErr := g.sendEnvelope(map[string]any{
		"who": "alice", "kind": "report", "body": "汇报", "scope_domain": "trade",
	}); isErr {
		t.Fatalf("域内信封不该被拒：%s", text)
	}
	if names := logFiles(t, data); len(names) != 1 {
		t.Fatalf("域内信封该进信封日志：%v", names)
	}
	rs := auditRecords(t, vault)
	if len(rs) != 0 {
		t.Fatalf("域内信封不进 audit（它不是跨域行使），实际 %+v", rs)
	}
}

// ⑧ 提案（kind=proposal）是**请求**不是行使：不判授权，但仍算一次跨域请求（好留痕）。
//
// 起因是一次真对拍：bob 的 bot 想替他要 ops 域的写权，信封被自家门禁判成「跨域行使」拒收 ——
// 可**提案人还没拿到权，当然没有 grant**，于是提案永远提不出来。提案里没有任何业务数据，
// 它只是一句请求；门禁拦的是「把数据拿出去 / 写进去」。
func TestGateProposalIsARequestNotAnExercise(t *testing.T) {
	vault := copyFixture(t, "domains")
	o, err := org.Load(vault)
	if err != nil {
		t.Fatalf("真相源加载失败：%v", err)
	}
	issues, v := envelope.Gate(o, envelope.Envelope{
		Who: "alice", OnBehalfOf: "member:alice", Kind: envelope.KindProposal,
		Scope: envelope.Scope{Domain: "logistics"},
	}, o.Policy)
	if len(issues) != 0 {
		t.Fatalf("提案不该被授权那一组拦（还没有权的人也要提得出请求）：%v", issues)
	}
	if !v.CrossDomain || !v.Requesting {
		t.Fatalf("提案要算「一次跨域请求」——不然留不下痕：%+v", v)
	}
	if v.Grant != "" {
		t.Fatalf("提案没命中任何 grant，不该编一个：%+v", v)
	}
	if v.Subject != "alice" || v.Domain != "logistics" {
		t.Fatalf("主体 / 客体要照实带出来：%+v", v)
	}
	// 同一封信换个 kind（ask）就照旧拒 —— 证明放宽的只是「请求」这一类，不是把门拆了。
	if got := gateRules(t, vault, envelope.Envelope{
		Who: "alice", Kind: envelope.KindAsk, Scope: envelope.Scope{Domain: "logistics"},
	}); len(got) != 1 || got[0] != "grant.match.missing" {
		t.Fatalf("ask 仍要按跨域判，实际 %v", got)
	}
}

// ⑧ 提案：跨域也照收（不判授权）—— 进信封日志 + 留一条 ok 痕，理由写明「这是请求」。
func TestIngressAcceptsCrossDomainProposalAndLogs(t *testing.T) {
	vault := copyFixture(t, "domains")
	data := t.TempDir()
	g := &envelopeIngress{Vault: vault, DataDir: data}
	text, isErr := g.sendEnvelope(map[string]any{
		"who": "alice", "on_behalf_of": "member:alice", "kind": "proposal",
		"body": "想要 logistics 的写权", "scope_domain": "logistics",
	})
	if isErr {
		t.Fatalf("提案不该被拒：%s", text)
	}
	if names := logFiles(t, data); len(names) != 1 {
		t.Fatalf("收下的提案该进信封日志：%v", names)
	}
	rs := auditRecords(t, vault)
	if len(rs) != 1 {
		t.Fatalf("跨域提案要留一条痕，实际 %d 条", len(rs))
	}
	r := rs[0]
	if r.Result != audit.ResultOK || r.Actor != "alice" || r.Object != "domain:logistics" {
		t.Fatalf("这一条痕没答全「谁 / 向谁 / 结果」：%+v", r)
	}
	if r.Why == "" {
		t.Fatalf("理由要写明这是「请求」而不是「行使」：%+v", r)
	}
}

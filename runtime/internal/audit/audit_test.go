package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/org"
)

// scopeFor 造一张「业务域 → 10-knowledge、运营 → 20-ops」的映射，
// alice 在 business 域、bob 在 ops 域 —— 判「跨域」要靠它。
func scopeFor(vault string) *Scope {
	return NewScope(vault, &org.Org{
		Company: org.Company{ID: "demo"},
		Members: []org.Member{
			{Name: "alice", Domains: []string{"business"}},
			{Name: "bob", Domains: []string{"ops"}},
			{Name: "carol"}, // 没配域：跨域判不出来，必须落 Known=false
		},
		Domains: []org.Domain{
			{Slug: "business", Name: "主营业务", Data: "10-knowledge"},
			{Slug: "ops", Name: "运营", Data: "20-ops"},
		},
	})
}

func rec(actor, object string) Record {
	return Record{ID: "x", At: "2026-10-08T12:00:00+08:00", Actor: actor,
		Action: ActionRead, Object: object, Result: ResultOK}
}

func TestShardNameKeepsUsableAndCleansTheRest(t *testing.T) {
	// 名字能不能当文件名，尺子只有一条：三平台都得建得出来。
	cases := map[string]string{
		"alice":            "alice",
		"白嘉伟":              "白嘉伟",
		"alice-2":          "alice-2",
		"a/b\\c:d*e?f\"g":  "a_b_c_d_e_f_g",
		"  spaced  ":       "spaced",
		"trailing.dots...": "trailing.dots",
		"tab\there":        "tab_here",
	}
	for in, want := range cases {
		if got := ShardName(in); got != want {
			t.Errorf("ShardName(%q) = %q，想要 %q", in, got, want)
		}
	}
}

func TestAppendNeedsAllFive(t *testing.T) {
	v := t.TempDir()
	// 五样里少一样都不收 —— 缺哪样这条记录就答不了「谁/何时/对谁/类别/结果」。
	bad := []Record{
		{At: "2026-10-08T12:00:00+08:00", Actor: "a", Action: ActionRead, Result: ResultOK},
		{ID: "x", At: "2026-10-08T12:00:00+08:00", Action: ActionRead, Result: ResultOK},
		{ID: "x", At: "2026-10-08T12:00:00+08:00", Actor: "a", Result: ResultOK},
		{ID: "x", At: "2026-10-08T12:00:00+08:00", Actor: "a", Action: ActionRead},
		{ID: "x", Actor: "a", Action: ActionRead, Result: ResultOK},
	}
	for i, r := range bad {
		if _, err := Append(v, r); err == nil {
			t.Errorf("第 %d 条缺字段却收下了：%+v", i, r)
		}
	}
	// 目标（Object）**不**必填：为空的意思是「抽不出目标」，是个真实状态（Bash 那类）。
	if _, err := Append(v, Record{ID: "x", At: "2026-10-08T12:00:00+08:00",
		Actor: "a", Action: ActionInvoke, Result: ResultDenied}); err != nil {
		t.Fatalf("Object 为空应当收下（它是「抽不出」，不是「缺字段」）：%v", err)
	}
}

func TestAppendShardsByActorAndRoundTrips(t *testing.T) {
	v := t.TempDir()
	p1, err := Append(v, Record{ID: "1", At: "2026-10-08T10:00:00+08:00", Actor: "alice",
		Action: ActionRead, Object: "/v/10-knowledge/a.md", Result: ResultOK, Why: "原话"})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Append(v, Record{ID: "2", At: "2026-10-08T11:00:00+08:00", Actor: "白嘉伟",
		Action: ActionWrite, Object: "/v/20-ops/b.md", Result: ResultDenied})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p1) != "2026-10.alice.jsonl" {
		t.Errorf("分片名 = %s，想要 2026-10.alice.jsonl", filepath.Base(p1))
	}
	if filepath.Base(p2) != "2026-10.白嘉伟.jsonl" {
		t.Errorf("中文名应当能当分片名，得到 %s", filepath.Base(p2))
	}
	if p1 == p2 {
		t.Error("两个行使者写进了同一个分片 —— 并发就不必靠锁了，这里不该撞")
	}

	doc, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Len() != 2 || len(doc.Bad) != 0 {
		t.Fatalf("读到 %d 条 / 坏行 %d，想要 2 / 0", doc.Len(), len(doc.Bad))
	}
	// 按时间升序。
	if doc.Records[0].ID != "1" || doc.Records[1].ID != "2" {
		t.Errorf("顺序不对：%s, %s", doc.Records[0].ID, doc.Records[1].ID)
	}
	if doc.Records[0].Why != "原话" {
		t.Error("why 没原样带回")
	}
	if ids := doc.IDs(); !ids["1"] || !ids["2"] {
		t.Error("IDs() 该给出两个 id（归集重跑去重靠它）")
	}
}

// 读不懂的行必须**列出来**，不许静默跳过 —— 少一行就可能让一次行使看起来没发生。
func TestLoadReportsBadLinesInsteadOfSkipping(t *testing.T) {
	v := t.TempDir()
	if _, err := Append(v, Record{ID: "ok", At: "2026-10-08T10:00:00+08:00",
		Actor: "alice", Action: ActionRead, Result: ResultOK}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(v, DirName, "2026-10.alice.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{ 这不是 json\n")
	f.WriteString(`{"at":"2026-10-08T10:00:00+08:00","actor":"alice"}` + "\n") // 缺 id
	f.Close()

	doc, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Len() != 1 {
		t.Errorf("好行应当照读，得到 %d 条", doc.Len())
	}
	if len(doc.Bad) != 2 {
		t.Fatalf("坏行应当逐条报出来，得到 %v", doc.Bad)
	}
	if !strings.Contains(doc.Bad[1], "缺 id") {
		t.Errorf("缺 id 的那行要说清为什么，得到 %q", doc.Bad[1])
	}
}

func TestLoadMissingDirIsEmptyNotError(t *testing.T) {
	doc, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("目录不在 = 还没有行使，不该报错：%v", err)
	}
	if doc.Len() != 0 || len(doc.Bad) != 0 {
		t.Errorf("想要空，得到 %d 条 / %d 坏行", doc.Len(), len(doc.Bad))
	}
}

func TestDeriveZones(t *testing.T) {
	v := "/vault"
	sc := scopeFor(v)
	cases := []struct {
		name, actor, object  string
		wantZone             string
		wantActee            string
		wantKnown, wantCross bool
	}{
		{"自己域内", "alice", v + "/10-knowledge/a.md", ZoneDomain, "business", true, false},
		{"跨域", "alice", v + "/20-ops/b.md", ZoneDomain, "ops", true, true},
		{"vault 内非域目录", "alice", v + "/members/alice/persona.md", ZoneVault, "", false, false},
		{"vault 外", "alice", "/home/sjw/.claude/settings.json", ZoneOutside, "", false, false},
		{"目标抽不出", "alice", "", ZoneUnknown, "", false, false},
		{"行使者没配域：跨域判不出来", "carol", v + "/10-knowledge/a.md", ZoneDomain, "business", false, false},
		{"project 名也解得出来", "demo-alice", v + "/20-ops/b.md", ZoneDomain, "ops", true, true},
	}
	for _, c := range cases {
		d := Derive(rec(c.actor, c.object), sc)
		if d.Zone != c.wantZone || d.Actee != c.wantActee || d.Known != c.wantKnown || d.Cross != c.wantCross {
			t.Errorf("%s：得到 zone=%s actee=%s known=%v cross=%v，想要 zone=%s actee=%s known=%v cross=%v",
				c.name, d.Zone, d.Actee, d.Known, d.Cross, c.wantZone, c.wantActee, c.wantKnown, c.wantCross)
		}
	}
}

func TestTallyScopesCountsCrossOutOfDomain(t *testing.T) {
	sc := scopeFor("/vault")
	vs := Views([]Record{
		rec("alice", "/vault/10-knowledge/a.md"), // 域内，不跨
		rec("alice", "/vault/20-ops/b.md"),       // 域内，跨
		rec("alice", "/vault/members/x.md"),      // vault 内非域
		rec("alice", "/etc/passwd"),              // 域外
		rec("alice", ""),                         // 判不出
	}, sc)
	got := TallyScopes(vs)
	want := Scopes{Domain: 2, Vault: 1, Outside: 1, Unknown: 1, Cross: 1}
	if got != want {
		t.Errorf("得到 %+v，想要 %+v", got, want)
	}
}

func TestResultBandKeepsUnknownWords(t *testing.T) {
	// 词表不锁死：认不出的原样保留、落 unknown 档，不报错、不吞。
	for _, s := range []string{"ok", "DENIED ", "failed"} {
		if ResultBand(s) == ResultUnknown {
			t.Errorf("%q 该认出来", s)
		}
	}
	if ResultBand("errored") != ResultUnknown {
		t.Error("认不出的词要落 unknown 档")
	}
}

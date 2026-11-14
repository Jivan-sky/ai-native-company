package bench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/ctxpack"
	"anc/internal/org"
)

// tester 是装配结果的最小构造器：测判分时只要那几格，别把整个 vault 拖进来。
func packOf(fill func(*ctxpack.Out)) ctxpack.Out {
	p := ctxpack.Out{Who: "alice", Identity: &ctxpack.IdentityOut{Kind: "member", Ref: "alice", Name: "Alice", Role: "manager"}}
	fill(&p)
	return p
}

func TestGradeDomainNeedsDataPointer(t *testing.T) {
	task := Task{ID: "t1", As: []string{"alice"}, Needs: []Need{{Kind: KindDomain, Value: "trade"}}}

	ok := packOf(func(p *ctxpack.Out) {
		p.Domains = []ctxpack.DomainOut{{Slug: "trade", Name: "大宗贸易", Data: "10-knowledge"}}
	})
	if v := Grade(ok, nil, nil, task); !v.AllDelivered() {
		t.Fatalf("整行都在、data 也指得到，应该到手：%+v", v)
	}

	// 只有名字没有 data：知道这块业务存在，不知道资料在哪 —— 还是要问人。
	half := packOf(func(p *ctxpack.Out) {
		p.Domains = []ctxpack.DomainOut{{Slug: "trade", Name: "大宗贸易"}}
	})
	v := Grade(half, nil, nil, task)
	if v.Delivered != 0 {
		t.Fatalf("没有 data 指针不该算到手：%+v", v)
	}
	if v.Asks != 1 {
		t.Fatalf("没到手就该记一次「要问人」：%+v", v)
	}
}

func TestGradeProjectAndSkill(t *testing.T) {
	task := Task{ID: "t1", As: []string{"alice"}, Needs: []Need{
		{Kind: KindProject, Value: "trade-q3"},
		{Kind: KindSkill, Value: "anc-distill"},
	}}
	p := packOf(func(p *ctxpack.Out) {
		p.Projects = []ctxpack.ProjectOut{{Slug: "trade-q3"}}
		// 登记了但正文不在 = 拿不到它。
		p.Skills = []ctxpack.SkillOut{{Name: "anc-distill", Missing: true}}
	})
	v := Grade(p, nil, nil, task)
	if v.Delivered != 1 {
		t.Fatalf("项目到手、技能正文缺失，应该 1/2：%+v", v)
	}
	if v.Needs[1].Delivered {
		t.Fatal("missing=true 的技能不该算到手 —— 那是「知道有、拿不到」")
	}
}

func TestGradeGrant(t *testing.T) {
	// A4 之后：判定要真看那条授权成不成立（撤权 / 到期 / 谁签的）。
	// 所以这里不再"手搓一个视图就行"，得给一份能成立的组织事实。
	vault := t.TempDir()
	writeAt(t, vault, "authority.md", "---\nauthority:\n  version: 1\n  issuer: member:alice\n---\n")
	o := &org.Org{Root: vault, Grants: []org.Grant{{
		Slug: "g1", Path: "grants/g1.md",
		From: "member:bob", To: "member:alice", Action: "read", Object: "domain:10-knowledge",
		IssuedBy: "member:alice",
	}}}
	p := packOf(func(p *ctxpack.Out) {
		p.Grants = &ctxpack.GrantsOut{Total: 1, Shown: 1, Rows: []ctxpack.GrantRowOut{
			{Slug: "g1", Action: "read", Object: "domain:10-knowledge"},
		}}
	})
	cases := []struct {
		value string
		want  bool
	}{
		{"read", true},                     // 只要动作覆盖
		{"read:domain:10-knowledge", true}, // 动作 + 客体都对
		{"read:20-ops", false},             // 对象不对
		{"write", false},                   // 没有写授权
	}
	for _, c := range cases {
		v := Grade(p, o, nil, Task{ID: "t", As: []string{"alice"}, Needs: []Need{{Kind: KindGrant, Value: c.value}}})
		if v.Needs[0].Delivered != c.want {
			t.Errorf("%s：想 %v，得到 %v（%s）", c.value, c.want, v.Needs[0].Delivered, v.Needs[0].Where)
		}
	}
}

// A4 的关键一条：**撤了权的授权，基准也不能说"拿得到"**。
// 以前基准只看装配视图里有没有那一行，撤权之后照报绿灯 —— 于是"基准说拿得到、真到门口被拒"长期共存。
func TestGradeGrantRespectsRevocation(t *testing.T) {
	vault := t.TempDir()
	writeAt(t, vault, "authority.md",
		"---\nauthority:\n  version: 2\n  issuer: member:alice\n  revoked: grants/g1.md@1\n---\n")
	o := &org.Org{Root: vault, Grants: []org.Grant{{
		Slug: "g1", Path: "grants/g1.md",
		From: "member:bob", To: "member:alice", Action: "read", Object: "domain:10-knowledge",
		IssuedBy: "member:alice",
	}}}
	p := packOf(func(p *ctxpack.Out) {
		p.Grants = &ctxpack.GrantsOut{Total: 1, Shown: 1, Rows: []ctxpack.GrantRowOut{
			{Slug: "g1", Action: "read", Object: "domain:10-knowledge"},
		}}
	})
	v := Grade(p, o, nil, Task{ID: "t", As: []string{"alice"}, Needs: []Need{{Kind: KindGrant, Value: "read"}}})
	if v.Needs[0].Delivered {
		t.Fatalf("撤了权不该算到手：%+v", v.Needs[0])
	}
	if !strings.Contains(v.Needs[0].Where, "撤回") {
		t.Errorf("理由要说清是撤回，拿到 %q", v.Needs[0].Where)
	}
}

func writeAt(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGradeToolIsCaseInsensitive(t *testing.T) {
	task := Task{ID: "t", As: []string{"alice"}, Needs: []Need{{Kind: KindTool, Value: "grep"}}}
	if v := Grade(packOf(func(*ctxpack.Out) {}), nil, []string{"Read", " Grep "}, task); !v.AllDelivered() {
		t.Fatalf("allowed_tools 里写了 Grep，大小写与空格不该判错：%+v", v)
	}
	// dontAsk 下没预授权 = 调不动，等于没到手。
	if v := Grade(packOf(func(*ctxpack.Out) {}), nil, []string{"Read"}, task); v.Delivered != 0 {
		t.Fatalf("没预授权不该算到手：%+v", v)
	}
}

func TestGradeRefusedSaysWhoToAsk(t *testing.T) {
	task := Task{ID: "t", As: []string{"alice"}, Needs: []Need{
		{Kind: KindDomain, Value: "logistics"},
		{Kind: KindProject, Value: "trade-q3"},
	}}
	p := packOf(func(p *ctxpack.Out) {
		p.Refused = &ctxpack.Refusal{Reason: "「logistics」不在你的可见范围", Ask: "Carol（运营）"}
	})
	v := Grade(p, nil, nil, task)
	if v.Asks != 2 || v.Delivered != 0 {
		t.Fatalf("拒答时全部算没到手：%+v", v)
	}
	for _, n := range v.Needs {
		if n.Ask != "Carol（运营）" {
			t.Fatalf("拒答把「该找谁」给出来了，就该原样带出来：%+v", n)
		}
	}
}

func TestWhoToAskFromDirectory(t *testing.T) {
	// 跨域拿不到，但目录告诉了你找谁 —— 这叫「有指路的问人」，和「没人知道该问谁」不是一回事。
	p := packOf(func(p *ctxpack.Out) {
		p.Directory = []ctxpack.BriefOut{{Slug: "logistics", Name: "物流", Who: "Carol（运营）"}}
	})
	v := Grade(p, nil, nil, Task{ID: "t", As: []string{"alice"}, Needs: []Need{{Kind: KindDomain, Value: "logistics"}}})
	if v.Needs[0].Ask != "Carol（运营）" {
		t.Fatalf("别域目录里的 who 应该被带出来：%+v", v.Needs[0])
	}
}

func TestLoadSuiteAcceptsBothShapes(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 一个「单个任务」文件、一个「任务集」文件 —— 两种形态都收，按文件名排序拼起来。
	write("b.json", `{"id":"t2","as":["bob"],"needs":[{"kind":"tool","value":"Read"}]}`)
	write("a.json", `{"tasks":[{"id":"t1","as":["alice"],"needs":[{"kind":"domain","value":"trade"}]}]}`)
	write("ignore.txt", "不是 JSON，应该被跳过")

	s, err := LoadSuite(dir)
	if err != nil {
		t.Fatalf("两种形态都该读得动：%v", err)
	}
	if len(s.Tasks) != 2 || s.Tasks[0].ID != "t1" || s.Tasks[1].ID != "t2" {
		t.Fatalf("按文件名排序后应该是 t1、t2：%+v", s.Tasks)
	}
}

func TestLoadSuiteRejectsGarbage(t *testing.T) {
	cases := map[string]string{
		"拼错的种类":    `{"id":"t","as":["a"],"needs":[{"kind":"grantz","value":"read"}]}`,
		"没有视角":     `{"id":"t","as":[],"needs":[{"kind":"grant","value":"read"}]}`,
		"没有 needs": `{"id":"t","as":["a"],"needs":[]}`,
	}
	for name, body := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "x.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSuite(dir); err == nil {
			t.Errorf("%s：应当报错（拼错/漏写等于「你以为测了其实没测」）", name)
		}
	}
}

func TestReportAggregatesPassK(t *testing.T) {
	r := Report{Schema: Schema}
	// 两个视角，只有第一个全到手 → 这个任务不算过。
	r.Add(TaskResult{ID: "t1", Origin: "external:x", Views: []View{
		{Who: "alice", Delivered: 2, Total: 2},
		{Who: "bob", Delivered: 1, Total: 2, Asks: 1},
	}, PassK: false})
	// 全到手 → 过。
	r.Add(TaskResult{ID: "t2", Origin: "synthetic", Views: []View{
		{Who: "alice", Delivered: 1, Total: 1},
	}, PassK: true})

	if r.Views != 3 || r.Needs != 5 || r.Delivered != 4 || r.Asks != 1 {
		t.Fatalf("计数不对：%+v", r)
	}
	if r.PassKRate != 0.5 {
		t.Fatalf("pass^k 率应该是 1/2：%v", r.PassKRate)
	}
	if r.External != 1 || r.Synthetic != 1 {
		t.Fatalf("外部 / 自造要分开计（自造的不算外部证据）：%+v", r)
	}
}

func TestFilterViews(t *testing.T) {
	as := []string{"a", "b", "c"}
	if got := FilterViews(as, 0); len(got) != 3 {
		t.Fatalf("k=0 应该全跑：%v", got)
	}
	if got := FilterViews(as, 2); len(got) != 2 || got[1] != "b" {
		t.Fatalf("k=2 应该取前两个：%v", got)
	}
	if got := FilterViews(as, 9); len(got) != 3 {
		t.Fatalf("k 大于视角数时不该 panic：%v", got)
	}
}

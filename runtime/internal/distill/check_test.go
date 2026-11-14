package distill

import (
	"strings"
	"testing"

	"anc/internal/timeline"
)

func goodDraft(name, caseKey string) string {
	// A6：description 是宿主发现技能的唯一依据，完整草稿必须有（中文触发语 + 英文触发语）。
	return "---\nname: " + name + "\nstatus: draft\ncase: " + caseKey + "\nhead_event: c1\n" +
		"description: 月末核对账单时使用；reconcile month-end statements when closing the books\n---\n\n# 对账\n\n" +
		"## 何时用\n月底对账时。\n\n## 输入\n账单 CSV。\n\n## 步骤\n1. 对齐单号。\n\n## 验证\n差异为零。\n"
}

func checkInput(name, draft string) CheckInput {
	return CheckInput{
		Name: name, DraftExists: true, Draft: draft,
		RolesWired: true, Roles: []RoleSkills{{Role: "manager", Skills: []string{name}}},
		TimelineWired: true,
		Entries: []timeline.Entry{
			row("c1", "case-md", "2026-10-18T10:00:00+08:00", "done", "对账", "trade", "做完了"),
		},
	}
}

func itemOf(r CheckReport, id string) *CheckItem {
	for i := range r.Items {
		if r.Items[i].ID == id {
			return &r.Items[i]
		}
	}
	return nil
}

func TestCheckPassesCompleteDraft(t *testing.T) {
	r := Check(checkInput("reconcile", goodDraft("reconcile", "case-md")))
	if !r.Ok || r.Fails != 0 || r.Warns != 0 {
		t.Fatalf("完整草稿应直接通过：ok=%v fails=%d warns=%d items=%+v", r.Ok, r.Fails, r.Warns, r.Items)
	}
	if it := itemOf(r, "case.provenance"); it == nil || it.Level != LevelOK {
		t.Fatalf("来源 case 应判绿：%+v", it)
	}
}

func TestCheckBlocksMissingSections(t *testing.T) {
	draft := strings.Replace(goodDraft("reconcile", "case-md"), "## 验证\n差异为零。\n", "", 1)
	r := Check(checkInput("reconcile", draft))
	if r.Ok || r.Fails != 1 {
		t.Fatalf("缺「验证」应拦发布：ok=%v fails=%d", r.Ok, r.Fails)
	}
	if it := itemOf(r, "draft.section.验证"); it == nil || it.Level != LevelFail {
		t.Fatalf("没点名缺「验证」：%+v", it)
	}
	empty := strings.Replace(goodDraft("reconcile", "case-md"), "## 步骤\n1. 对齐单号。\n", "## 步骤\n   \n", 1)
	r = Check(checkInput("reconcile", empty))
	if it := itemOf(r, "draft.section.步骤"); it == nil || it.Level != LevelFail {
		t.Fatalf("空的「步骤」应判 fail：%+v", it)
	}
}

func TestCheckBlocksPlaceholderAndSecret(t *testing.T) {
	draft := strings.Replace(goodDraft("reconcile", "case-md"), "1. 对齐单号。", "1. TODO 对齐单号。", 1)
	if r := Check(checkInput("reconcile", draft)); r.Ok || itemOf(r, "draft.placeholder").Level != LevelFail {
		t.Fatalf("TODO 应拦发布：%+v", r.Items)
	}
	draft = strings.Replace(goodDraft("reconcile", "case-md"), "账单 CSV。", "账单 CSV。\napi_key: sk-test-123456", 1)
	r := Check(checkInput("reconcile", draft))
	if r.Ok || itemOf(r, "draft.secret").Level != LevelFail {
		t.Fatalf("明文密钥应拦发布：%+v", r.Items)
	}
	if strings.Contains(itemOf(r, "draft.secret").Detail, "sk-test-123456") {
		t.Fatal("检查结果里不许回显明文")
	}
}

func TestCheckWarnsButDoesNotBlock(t *testing.T) {
	in := checkInput("reconcile", goodDraft("reconcile", "case-md"))
	in.Roles = []RoleSkills{{Role: "manager", Skills: []string{"now"}}}
	in.Draft = strings.Replace(in.Draft, "账单 CSV。", "账单 CSV。\n见 D:\\work\\a.md", 1)
	r := Check(in)
	if !r.Ok || r.Fails != 0 {
		t.Fatalf("只有 warn 时不该拦发布：ok=%v fails=%d", r.Ok, r.Fails)
	}
	for _, id := range []string{"roles.declared", "draft.absolute_path"} {
		if it := itemOf(r, id); it == nil || it.Level != LevelWarn {
			t.Fatalf("%s 应是 warn：%+v", id, it)
		}
	}
}

func TestCheckBlocksNameMismatchAndCollision(t *testing.T) {
	r := Check(checkInput("reconcile", goodDraft("other-name", "case-md")))
	if r.Ok || itemOf(r, "draft.name_matches").Level != LevelFail {
		t.Fatalf("frontmatter name 不一致应拦：%+v", r.Items)
	}
	in := checkInput("reconcile", goodDraft("reconcile", "case-md"))
	in.OfficialExists = true
	r = Check(in)
	if r.Ok || itemOf(r, "official.collision").Level != LevelFail {
		t.Fatalf("正式落点已存在应拦：%+v", r.Items)
	}
}

func TestCheckInvalidNameStopsEarly(t *testing.T) {
	r := Check(CheckInput{Name: "Bad Name", DraftExists: true, Draft: "x"})
	if r.Ok || r.Fails != 1 || len(r.Items) != 1 {
		t.Fatalf("非法名字应只回一条 fail：%+v", r.Items)
	}
}

// B2：草稿绑的是**具体哪一条结案事件**。这个 case 重开又结案之后，
// 旧草稿的 case 仍然是绿的 —— 但它依据的那个"完成"已经不是当前态了。
func TestCheckFlagsStaleHeadEvent(t *testing.T) {
	entries := []timeline.Entry{
		row("d1", "case-md", "2026-10-18T09:00:00+08:00", "done", "对账", "trade", "第一次做完"),
		withPrev(row("r1", "case-md", "2026-10-18T10:00:00+08:00", "running", "对账", "trade",
			"发现漏了"), "d1"),
		withPrev(row("d2", "case-md", "2026-10-18T11:00:00+08:00", "done", "对账", "trade",
			"补完再结"), "r1"),
	}
	in := CheckInput{
		Name: "reconcile", DraftExists: true, Draft: goodDraft("reconcile", "case-md"),
		RolesWired: true, Roles: []RoleSkills{{Role: "manager", Skills: []string{"reconcile"}}},
		TimelineWired: true, Entries: entries,
	}
	// 草稿写的是 c1（不在这个 case 里）→ 也算对不上
	if it := itemOf(Check(in), "case.head_event"); it == nil || it.Level == LevelOK {
		t.Fatalf("依据对不上时必须报警：%+v", it)
	}

	// 把草稿绑到已经过期的 d1（现在当前态是 d2）
	stale := strings.Replace(goodDraft("reconcile", "case-md"), "head_event: c1", "head_event: d1", 1)
	in.Draft = stale
	it := itemOf(Check(in), "case.head_event")
	if it == nil || it.Level == LevelOK {
		t.Fatalf("绑在旧结案事件上必须报「依据过期」：%+v", it)
	}
	if !strings.Contains(it.Detail, "d1") || !strings.Contains(it.Detail, "d2") {
		t.Errorf("要说清旧的是哪条、现在的是哪条：%s", it.Detail)
	}

	// 绑到当前态 d2 → 通过
	fresh := strings.Replace(goodDraft("reconcile", "case-md"), "head_event: c1", "head_event: d2", 1)
	in.Draft = fresh
	if it := itemOf(Check(in), "case.head_event"); it == nil || it.Level != LevelOK {
		t.Fatalf("绑在当前结案事件上该通过：%+v", it)
	}
}

// A6-1：「只补了四段、没写 description」必须被拦下 —— 这条以前是**通过**的。
// 宿主靠 description 决定什么时候选这个技能；没有它，发布出去等于隐身。
func TestCheckBlocksMissingDescription(t *testing.T) {
	in := checkInput("reconcile", strings.Replace(goodDraft("reconcile", "case-md"),
		"description: 月末核对账单时使用；reconcile month-end statements when closing the books\n", "", 1))
	r := Check(in)
	it := itemOf(r, "draft.description")
	if it == nil || it.Level != LevelFail {
		t.Fatalf("缺 description 必须判失败：%+v", it)
	}
	if r.Ok {
		t.Error("有失败项时 Ok 不能是 true")
	}
}

// A6-2：折行描述（`>` 块标量）必须与单行读成同一句 ——
// 否则校验器读一句、宿主读另一句，"我们校验通过"就成了自欺。
func TestDescriptionFoldedScalarIsSameText(t *testing.T) {
	// 折行的规则是「相邻行之间补一个空格」—— 所以单行写法要写成同样带空格的形态，
	// 两者才该读成同一句。这条测的是"读法一致"，不是"排版好看"。
	oneLine := "月末核对账单时使用； reconcile month-end statements when closing the books"
	folded := "description: >\n  月末核对账单时使用；\n  reconcile month-end statements\n  when closing the books\n"
	fm, problems, ok := parseFrontmatter("---\nname: x\n" + folded + "---\n")
	if !ok || len(problems) > 0 {
		t.Fatalf("折行写法的 frontmatter 该能读：ok=%v problems=%v", ok, problems)
	}
	if got := fm["description"]; got != oneLine {
		t.Fatalf("折行该折成同一句：\n 得到 %q\n 期望 %q", got, oneLine)
	}
	if why := descriptionProblem(fm["description"]); why != "" {
		t.Errorf("折行读出来的描述该合格：%s", why)
	}
}

// 重复键：宿主只会认一个，我们不猜是哪个 —— 报出来。
func TestFrontmatterDuplicateKeyReported(t *testing.T) {
	_, problems, ok := parseFrontmatter("---\nname: a\nname: b\n---\n")
	if !ok {
		t.Fatal("该认出 frontmatter")
	}
	if len(problems) == 0 {
		t.Fatal("重复键要报问题")
	}
	if !strings.Contains(problems[0], "name") {
		t.Errorf("要指出是哪个字段：%v", problems)
	}
}

// 只有中文、没有英文触发语（或反之）→ 不合格：两套宿主各读一套。
func TestDescriptionNeedsBothLanguages(t *testing.T) {
	if why := descriptionProblem("月末核对账单时使用，用来对齐单号与差异"); why == "" {
		t.Error("只有中文该判不合格")
	}
	if why := descriptionProblem("reconcile month-end statements and diff the numbers"); why == "" {
		t.Error("只有英文该判不合格")
	}
	if why := descriptionProblem(""); why == "" {
		t.Error("空描述该判不合格")
	}
}

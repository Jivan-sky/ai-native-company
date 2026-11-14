package distill

import (
	"strings"
	"testing"

	"anc/internal/timeline"
)

func row(id, caseID, at, status, title, domain, detail string, refs ...string) timeline.Entry {
	return timeline.Entry{
		ID: id, Case: caseID, At: at, Status: status, By: "cs-owner",
		Title: title, Domain: domain, Detail: detail, Refs: refs,
	}
}

// withPrev 给一条记录接上前序（B1 之后，「靠时间猜的闭环」不算闭环）。
func withPrev(e timeline.Entry, prev string) timeline.Entry {
	e.Prev = prev
	return e
}

func firstLineOf(list []string, prefix string) string {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return s
		}
	}
	return ""
}

// 候选只认「最后一条是绿档」：同一 case 先 done 后 running，就不算闭环。
// 排序、信号、同名统计都按现算事实走。
func TestCandidatesOnlyClosedCases(t *testing.T) {
	in := Input{
		TimelineWired: true, SkillsWired: true,
		Entries: []timeline.Entry{
			row("a1", "case-a", "2026-10-18T10:00:00+08:00", "running", "对账", "trade", "第一步"),
			withPrev(row("a1b", "case-a", "2026-10-18T11:00:00+08:00", "done", "对账", "trade",
				"第二步完成", "refs/a.md"), "a1"),
			row("b1", "case-b", "2026-10-18T12:00:00+08:00", "blocked", "等 ETA", "trade", "卡在外部"),
			row("c1", "case-c", "2026-10-18T13:00:00+08:00", "done", "对账", "ops", "", "refs/b.md", "refs/c.md"),
			row("d1", "case-d", "2026-10-18T14:00:00+08:00", "done", "单独的事", "", ""),
			row("e1", "case-e", "2026-10-18T09:00:00+08:00", "done", "回滚过的判断", "trade", "先成了"),
			row("e2", "case-e", "2026-10-18T15:00:00+08:00", "running", "回滚过的判断", "trade", "又开了"),
		},
	}
	r := Build(in)
	if r.Cases != 5 || r.Green != 3 || r.CandidateTotal != 3 {
		t.Fatalf("cases=%d green=%d candidate_total=%d，期望 5/3/3", r.Cases, r.Green, r.CandidateTotal)
	}
	if len(r.Candidates) != 3 {
		t.Fatalf("候选 %d 条，期望 3", len(r.Candidates))
	}
	if got := []string{r.Candidates[0].Case, r.Candidates[1].Case, r.Candidates[2].Case}; strings.Join(got, ",") != "case-d,case-c,case-a" {
		t.Fatalf("候选顺序 %v，期望按最后活动倒序 case-d,case-c,case-a", got)
	}
	for _, c := range r.Candidates {
		if c.Case == "case-b" || c.Case == "case-e" {
			t.Fatalf("%s 不该进候选（最后一条不是绿档）", c.Case)
		}
	}
	a := r.Candidates[2]
	if a.Entries != 2 || a.DetailChars != 8 || a.Refs != 1 || a.SameTitle != 2 {
		t.Fatalf("case-a 事实不对：%+v", a)
	}
	if strings.Join(a.Signals, " | ") != "2 条推进 | detail 8 字 | refs 1 个 | 同名 2 次" {
		t.Fatalf("case-a signals=%v", a.Signals)
	}
}

// 技能覆盖：声明了但没正文 = missing；有正文但没人声明 = 孤儿。
func TestSkillCoverageAndOrphans(t *testing.T) {
	r := Build(Input{
		TimelineWired: true, SkillsWired: true,
		Roles: []RoleSkills{
			{Role: "manager", Skills: []string{"now", "im-send"}},
			{Role: "ops", Skills: []string{"now"}},
		},
		Bodies: []string{"now", "orphan"},
	})
	if len(r.Skills) != 2 {
		t.Fatalf("技能 %d 份，期望 2：%+v", len(r.Skills), r.Skills)
	}
	if r.Skills[0].Name != "im-send" || !r.Skills[0].Missing || strings.Join(r.Skills[0].Roles, ",") != "manager" {
		t.Fatalf("im-send 应缺正文且只由 manager 声明：%+v", r.Skills[0])
	}
	if r.Skills[1].Name != "now" || r.Skills[1].Missing || strings.Join(r.Skills[1].Roles, ",") != "manager,ops" {
		t.Fatalf("now 应有正文、由 manager+ops 声明：%+v", r.Skills[1])
	}
	if len(r.Orphans) != 1 || r.Orphans[0] != "orphan" {
		t.Fatalf("孤儿技能=%v，期望 [orphan]", r.Orphans)
	}
	if got := firstLineOf(r.Gaps, "技能正文没落：skills/im-send/SKILL.md"); got == "" {
		t.Fatalf("缺口里没说 im-send 正文没落：%v", r.Gaps)
	}
}

// 空 timeline 不是错；没有候选 ≠ 没有资产，这句话必须留在 gaps 里。
func TestEmptyTimelineIsNotAnError(t *testing.T) {
	r := Build(Input{TimelineWired: true, SkillsWired: true})
	if r.Error != "" {
		t.Fatalf("空 timeline 不该是错误：%s", r.Error)
	}
	if r.Candidates == nil || len(r.Candidates) != 0 {
		t.Fatalf("空时候选要是空数组，不是 null：%#v", r.Candidates)
	}
	if got := firstLineOf(r.Gaps, "timeline：还没有任何记录"); got == "" {
		t.Fatalf("空 timeline 的缺口没写清：%v", r.Gaps)
	}
}

// org 读不动时，技能那一段答不了 —— 但要与「没有技能」分开说。
func TestSkillsNotWiredIsReported(t *testing.T) {
	r := Build(Input{TimelineWired: true, SkillsWired: false})
	if r.SkillsWired {
		t.Fatal("SkillsWired 应为 false")
	}
	if got := firstLineOf(r.Gaps, "技能覆盖：org 真相源读不动"); got == "" {
		t.Fatalf("缺口没交代技能那一段：%v", r.Gaps)
	}
}

// B1：绿档但**判不出来**的 case 不进候选，而且要能解释为什么少了几条。
func TestCandidatesExcludeUndecidable(t *testing.T) {
	in := Input{
		TimelineWired: true, SkillsWired: true,
		Entries: []timeline.Entry{
			// 冲突：两条互不引用的相反主张（同秒）
			row("x1", "case-x", "2026-10-18T09:00:00+08:00", "done", "两份报告", "trade", "我完成了"),
			withPrev(row("x2", "case-x", "2026-10-18T09:00:00+08:00", "running", "两份报告", "trade",
				"还差一截"), "x0"),
			// 老数据中途变过状态（无 prev）→ 推定且混档，不沉淀
			row("y1", "case-y", "2026-10-18T10:00:00+08:00", "running", "老案子", "trade", "在办"),
			row("y2", "case-y", "2026-10-18T11:00:00+08:00", "done", "老案子", "trade", "做完了"),
			// 正常闭环（有前序）→ 该进候选
			row("z0", "case-z", "2026-10-18T10:00:00+08:00", "running", "正常案子", "trade", "开工"),
			withPrev(row("z1", "case-z", "2026-10-18T12:00:00+08:00", "done", "正常案子", "trade",
				"收工"), "z0"),
		},
	}
	r := Build(in)
	if r.CandidateTotal != 1 || len(r.Candidates) != 1 || r.Candidates[0].Case != "case-z" {
		t.Fatalf("只该有 case-z 一条候选，拿到 %+v", r.Candidates)
	}
	if len(r.Excluded) != 2 {
		t.Fatalf("该有 2 条被排除且附原因，拿到 %+v", r.Excluded)
	}
	for _, x := range r.Excluded {
		if strings.TrimSpace(x.Why) == "" {
			t.Errorf("%s 被排除了但没写原因", x.Case)
		}
	}
	if r.Candidates[0].HeadEvent != "z1" {
		t.Errorf("候选要绑住结案那条事件，拿到 %q", r.Candidates[0].HeadEvent)
	}
}

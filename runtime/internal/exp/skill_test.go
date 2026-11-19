package exp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deployed(t *testing.T, v string) Experience {
	t.Helper()
	if _, err := Add(v, Experience{ID: "e1", Title: "对账口径", Claim: "以附件三为准", Scope: "财务/对账",
		From: []string{"blocker:b1"}, By: "anc distill"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Approve(v, "e1", "alice", "证据够"); err != nil {
		t.Fatal(err)
	}
	e, err := Deploy(v, "e1", "alice")
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	return e
}

// 固化：deploy 之后 skills/<id>/SKILL.md 必须真的存在，且带来源与版本。
func TestDeployWritesSkillFile(t *testing.T) {
	v := t.TempDir()
	e := deployed(t, v)
	p := filepath.Join(v, "skills", e.ID, "SKILL.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("SKILL.md 没写出来：%v", err)
	}
	s := string(b)
	for _, want := range []string{"name: e1", "version: 1", "approved_by: alice", "blocker:b1", "以附件三为准"} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md 缺 %q：\n%s", want, s)
		}
	}
	// 台账要能对得上这次写出的字节
	led, _ := LedgerOf(v)
	last := led[len(led)-1]
	if last.Action != "deploy" || last.SkillDigest == "" {
		t.Fatalf("部署行该带 skill_digest，拿到 %+v", last)
	}
}

// 回滚：内容回到上一版，但版本号继续往前走（回滚本身也是事实）。
func TestRollbackRestoresPreviousBytes(t *testing.T) {
	v := t.TempDir()
	e := deployed(t, v)
	first, _ := os.ReadFile(filepath.Join(v, "skills", e.ID, "SKILL.md"))

	// 改内容再部署一次（模拟"经验被修订后重新部署"）
	e.Claim = "以附件四为准"
	if err := save(v, e); err != nil {
		t.Fatal(err)
	}
	// 内容改过 → 必须重新确认（否则就是"确认的是 A、部署的是 B"）
	if _, err := Deploy(v, "e1", "alice"); err == nil {
		t.Fatal("内容改了却不重新确认，部署该被拒")
	}
	if _, err := Approve(v, "e1", "alice", "改用附件四"); err != nil {
		t.Fatalf("重新确认: %v", err)
	}
	if _, err := Deploy(v, "e1", "alice"); err != nil {
		t.Fatalf("第二次 deploy: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(v, "skills", e.ID, "SKILL.md"))
	if string(first) == string(second) {
		t.Fatal("第二次部署该写出不同内容")
	}

	got, err := Rollback(v, "e1", "alice")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got.Version != 3 {
		t.Fatalf("回滚后版本号该继续往前走（3），拿到 %d", got.Version)
	}
	back, _ := os.ReadFile(filepath.Join(v, "skills", e.ID, "SKILL.md"))
	if string(back) != string(first) {
		t.Fatalf("回滚后内容该等于第一版：\nfirst=%s\nback=%s", first, back)
	}
	led, _ := LedgerOf(v)
	if last := led[len(led)-1]; last.Action != "rollback" {
		t.Fatalf("台账最后一行该是 rollback，拿到 %+v", last)
	}
}

// 没部署过的不能回滚（没有旧字节可回）。
func TestRollbackRefusesWhenNotDeployed(t *testing.T) {
	v := t.TempDir()
	if _, err := Add(v, Experience{ID: "e2", Claim: "c", From: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Rollback(v, "e2", "alice"); err == nil {
		t.Fatal("未部署的经验不该能回滚")
	}
}

// 判据 7 仍然成立：没 approve 的 deploy 连 skills 都不该写。
func TestDeployWithoutApprovalWritesNoSkill(t *testing.T) {
	v := t.TempDir()
	if _, err := Add(v, Experience{ID: "e3", Claim: "c", From: []string{"x"}, Scope: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Deploy(v, "e3", "alice"); err == nil {
		t.Fatal("未确认不该部署")
	}
	if _, err := os.Stat(filepath.Join(v, "skills", "e3", "SKILL.md")); err == nil {
		t.Fatal("被拒的部署不该留下 SKILL.md")
	}
}

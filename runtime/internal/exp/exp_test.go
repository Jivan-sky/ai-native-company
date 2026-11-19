package exp

import (
	"errors"
	"testing"
)

func newVault(t *testing.T) string { t.Helper(); return t.TempDir() }

func candidate(t *testing.T, v string) Experience {
	t.Helper()
	e, err := Add(v, Experience{ID: "e1", Title: "口径以附件三为准", Claim: "对账先看附件三",
		Scope: "财务/对账", From: []string{"timeline:ev-1"}, By: "alice"})
	if err != nil {
		t.Fatalf("建候选失败：%v", err)
	}
	return e
}

// 判据 7：未经人工确认，**不许**部署。
func TestDeployWithoutApprovalIsRefused(t *testing.T) {
	v := newVault(t)
	candidate(t, v)
	if _, err := Deploy(v, "e1", "bob"); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("该被 ErrNotApproved 拒，拿到 %v", err)
	}
	if got, _ := Get(v, "e1"); got.Status != StatusCandidate {
		t.Fatalf("被拒后状态不该变，拿到 %s", got.Status)
	}
}

// 判据 8：部署要留版本与 digest，台账可查。
func TestDeployWritesVersionAndDigestToLedger(t *testing.T) {
	v := newVault(t)
	candidate(t, v)
	if _, err := Approve(v, "e1", "bob", "证据够"); err != nil {
		t.Fatalf("approve 失败：%v", err)
	}
	e, err := Deploy(v, "e1", "bob")
	if err != nil {
		t.Fatalf("deploy 失败：%v", err)
	}
	if e.Version != 1 {
		t.Fatalf("首次部署版本该是 1，拿到 %d", e.Version)
	}
	led, _ := LedgerOf(v)
	if len(led) != 3 {
		t.Fatalf("台账该有 3 行（add/approve/deploy），拿到 %d", len(led))
	}
	last := led[len(led)-1]
	if last.Action != "deploy" || last.Digest == "" || last.Version != 1 {
		t.Fatalf("部署行要带 digest 与 version，拿到 %+v", last)
	}
}

// 判据 12：否决要留理由，且状态可查（垃圾池不是黑洞）。
func TestRejectRequiresWhyAndIsQueryable(t *testing.T) {
	v := newVault(t)
	candidate(t, v)
	if _, err := Reject(v, "e1", "bob", ""); err == nil {
		t.Fatal("不写理由的否决该被拒")
	}
	e, err := Reject(v, "e1", "bob", "只是巧合，不是可复用经验")
	if err != nil {
		t.Fatalf("reject 失败：%v", err)
	}
	if e.Status != StatusRejected {
		t.Fatalf("状态该是 rejected，拿到 %s", e.Status)
	}
	all, _ := List(v)
	if len(all) != 1 || all[0].Status != StatusRejected {
		t.Fatalf("被否决的条目要能查到，拿到 %+v", all)
	}
}

// 没有出处的"经验"不许入库（判据 1）。
func TestAddRequiresFrom(t *testing.T) {
	v := newVault(t)
	if _, err := Add(v, Experience{ID: "e2", Claim: "一句没有出处的结论"}); err == nil {
		t.Fatal("没有 from 的经验该被拒")
	}
}

// 废止要理由，且留下台账。
func TestRetireRequiresWhy(t *testing.T) {
	v := newVault(t)
	candidate(t, v)
	if _, err := Approve(v, "e1", "bob", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := Deploy(v, "e1", "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := Retire(v, "e1", "bob", ""); err == nil {
		t.Fatal("不写理由的废止该被拒")
	}
	e, err := Retire(v, "e1", "bob", "口径改了")
	if err != nil {
		t.Fatalf("retire 失败：%v", err)
	}
	if e.Status != StatusRetired {
		t.Fatalf("状态该是 retired，拿到 %s", e.Status)
	}
}

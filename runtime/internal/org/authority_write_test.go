package org

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 空 vault：先没权威源就签不出根授权 —— 这是 C1 的底线。
func TestAppendGrantNeedsAuthority(t *testing.T) {
	vault := t.TempDir()
	_, err := AppendGrant(vault, Grant{Slug: "g1", From: "member:alice", To: "member:bob",
		Action: "read", Object: "domain:trade", IssuedBy: "member:alice"})
	if !errors.Is(err, ErrNoAuthority) {
		t.Fatalf("没有权威源就该签不出来，拿到 %v", err)
	}
}

// 签发 → 立刻能通过 Exercise；同名再签 → 拒绝（不覆盖历史）。
func TestAppendGrantThenExercise(t *testing.T) {
	vault := simVault(t) // 已有 authority v3 / issuer member:alice
	g := Grant{Slug: "new-grant", From: "member:alice", To: "member:carol",
		Action: "read", Object: "domain:trade", IssuedBy: "member:alice",
		Reason: "用例", IssuedAt: "2026-11-01T00:00:00Z"}
	if _, err := AppendGrant(vault, g); err != nil {
		t.Fatalf("签发该成功：%v", err)
	}
	o, err := Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := o.grantByRef("new-grant")
	if !ok {
		t.Fatal("签发之后真相源里该看得到")
	}
	auth, wired, _ := LoadAuthority(vault)
	if v := o.Exercise(got, auth, wired, authAt); !v.OK {
		t.Fatalf("刚签的根授权该成立：%+v", v)
	}
	// 同名不覆盖
	if _, err := AppendGrant(vault, g); !errors.Is(err, ErrGrantExists) {
		t.Fatalf("同名该拒绝，拿到 %v", err)
	}
}

// 撤权：版本 +1，立刻失效；且只有受托签发人能撤。
func TestRevokeBumpsVersionAndStopsGrant(t *testing.T) {
	vault := simVault(t)
	g := Grant{Slug: "to-revoke", From: "member:alice", To: "member:carol",
		Action: "read", Object: "domain:trade", IssuedBy: "member:alice",
		IssuedAt: "2026-11-01T00:00:00Z"}
	if _, err := AppendGrant(vault, g); err != nil {
		t.Fatal(err)
	}
	before, _, _ := LoadAuthority(vault)

	if _, err := Revoke(vault, "to-revoke", "member:carol"); err == nil {
		t.Fatal("非受托签发人撤权该被拒")
	}
	after, err := Revoke(vault, "to-revoke", "member:alice")
	if err != nil {
		t.Fatalf("受托签发人撤权该成功：%v", err)
	}
	if after.Version != before.Version+1 {
		t.Fatalf("撤权要把版本推进一版：%d → %d", before.Version, after.Version)
	}
	o, _ := Load(vault)
	got, _ := o.grantByRef("to-revoke")
	if v := o.Exercise(got, after, true, authAt); v.OK || v.Unknown {
		t.Fatalf("撤权后必须明确拒绝：%+v", v)
	}
}

// 撤一条不存在的：不许白白推进版本号。
func TestRevokeUnknownGrantRefused(t *testing.T) {
	vault := simVault(t)
	if _, err := Revoke(vault, "nope", "member:alice"); err == nil {
		t.Fatal("撤不存在的授权该报错")
	}
	a, _, _ := LoadAuthority(vault)
	if a.Version != 3 {
		t.Fatalf("版本不该被推进，拿到 %d", a.Version)
	}
}

// 半份文件不该出现：写入走临时文件 + 换名，读的人要么旧要么新。
func TestWriteAtomicLeavesNoTempFiles(t *testing.T) {
	vault := t.TempDir()
	if _, err := InitAuthority(vault, "member:alice", "用例"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(vault)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("留下了临时文件：%s", e.Name())
		}
	}
	body, err := os.ReadFile(filepath.Join(vault, AuthorityFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "version: 1") {
		t.Fatalf("init 该写 v1：%s", body)
	}
}

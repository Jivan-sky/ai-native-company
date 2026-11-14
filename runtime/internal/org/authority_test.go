package org

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var authAt = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func writeFileT(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 三档账号的模拟权威源：alice 是受托签发人，bob 拿 alice 的根授权，
// carol 拿 bob 转授的更窄一条。文件就是普通 markdown —— 不依赖任何真实组织。
func simVault(t *testing.T) string {
	t.Helper()
	// 在**真实 fixture** 上加一层模拟：company / roles / domains 都来自 testdata，
	// 我只补三个东西 —— 权威源、委派链、三档成员。这样测的是判定，不是手搓 vault 的手艺。
	vault := copyVault(t, "domains")
	writeFileT(t, vault, "authority.md", `---
authority:
  version: 3
  issuer: member:alice
  note: 客户 IT 负责人书面委任（模拟）
  revoked: grants/revoked-one.md@2
---
`)
	writeFileT(t, vault, "grants/root-trade.md", `---
grant:
  from: member:alice
  to: member:bob
  action: read
  object: domain:trade
  issued_by: member:alice
  issued_at: 2026-11-01T00:00:00Z
---
`)
	writeFileT(t, vault, "grants/carol-logi.md", `---
grant:
  from: member:bob
  to: member:carol
  action: read
  object: domain:trade
  parent: grants/root-trade.md
  issued_by: member:bob
  issued_at: 2026-11-02T00:00:00Z
---
`)
	writeFileT(t, vault, "grants/bob-logi.md", `---
grant:
  from: member:alice
  to: member:bob
  action: read
  object: domain:logistics
  issued_by: member:alice
  issued_at: 2026-11-01T00:00:00Z
  expires_at: 2026-12-01T00:00:00Z
---
`)
	// 一条被撤掉的转授（撤权记录见 authority.md）
	writeFileT(t, vault, "grants/revoked-one.md", `---
grant:
  from: member:bob
  to: member:carol
  action: read
  object: domain:trade
  parent: grants/root-trade.md
  issued_by: member:bob
  issued_at: 2026-11-02T00:00:00Z
---
`)
	writeFileT(t, vault, "grants/forged.md", `---
grant:
  from: member:bob
  to: member:bob
  action: read
  object: domain:trade
  issued_by: member:carol
---
`)
	writeFileT(t, vault, "grants/overreach.md", `---
grant:
  from: member:bob
  to: member:bob
  action: write
  object: domain:logistics
  parent: grants/bob-logi.md
  issued_by: member:bob
---
`)
	m := func(name, role string) string {
		return "---\nmember:\n  name: " + name + "\n  role: " + role + "\n---\n"
	}
	writeFileT(t, vault, "members/alice.md", m("alice", "manager"))
	writeFileT(t, vault, "members/bob.md", m("bob", "manager"))
	writeFileT(t, vault, "members/carol.md", m("carol", "devbot"))
	return vault
}

func mustLoad(t *testing.T, vault string) (*Org, Authority) {
	t.Helper()
	o, err := Load(vault)
	if err != nil {
		t.Fatalf("真相源加载失败：%v", err)
	}
	a, wired, err := LoadAuthority(vault)
	if err != nil || !wired {
		t.Fatalf("权威源该读得到：wired=%v err=%v", wired, err)
	}
	return o, a
}

// 根授权：受托签发人签的成立；别人签的根不成立。
func TestExerciseRootGrant(t *testing.T) {
	o, a := mustLoad(t, simVault(t))
	root, ok := o.grantByRef("root-trade")
	if !ok {
		t.Fatal("找不到根授权")
	}
	if v := o.Exercise(root, a, true, authAt); !v.OK {
		t.Fatalf("受托签发人签的根该成立：%+v", v)
	}
	forged, _ := o.grantByRef("forged")
	v := o.Exercise(forged, a, true, authAt)
	if v.OK || v.Unknown {
		t.Fatalf("冒充签发人必须明确拒绝（不是「无法判定」）：%+v", v)
	}
	if !strings.Contains(v.Reason, "受托签发人") {
		t.Errorf("拒绝理由要说清「只有受托签发人能签根」：%s", v.Reason)
	}
}

// 转授：父成立才成立；换动作 / 扩大客体 / 比父活得久都不成立。
func TestExerciseDelegation(t *testing.T) {
	o, a := mustLoad(t, simVault(t))
	child, _ := o.grantByRef("carol-logi")
	if v := o.Exercise(child, a, true, authAt); !v.OK {
		t.Fatalf("父成立时转授该成立：%+v", v)
	}
	over, _ := o.grantByRef("overreach")
	v := o.Exercise(over, a, true, authAt)
	if v.OK {
		t.Fatalf("转授换动作（read→write）不该成立：%+v", v)
	}
	if !strings.Contains(v.Reason, "不能换动作") {
		t.Errorf("理由要说清是动作越界：%s", v.Reason)
	}
}

// 撤权：从权威源的版本起失效；版本没过那一版时仍成立（历史回放）。
func TestExerciseRevocation(t *testing.T) {
	vault := simVault(t)
	o, a := mustLoad(t, vault)
	child, _ := o.grantByRef("revoked-one")
	// 权威版本 3、撤权记录 @2 → 现在已撤
	v := o.Exercise(child, a, true, authAt)
	if v.OK || v.Unknown {
		t.Fatalf("撤权后必须明确拒绝：%+v", v)
	}
	if !strings.Contains(v.Reason, "撤回") {
		t.Errorf("理由要说清被撤回：%s", v.Reason)
	}
	// 历史回放：版本 1 时还没撤（把撤权记录推到 v3）
	a2 := a
	a2.Version = 1
	childRev := child
	if v := o.Exercise(childRev, a2, true, authAt); !v.OK {
		t.Fatalf("v1 时还没撤，历史回放该成立：%+v", v)
	}
}

// 到期：左闭右开；到期那一刻就失效。
func TestExerciseExpiry(t *testing.T) {
	o, a := mustLoad(t, simVault(t))
	g, _ := o.grantByRef("bob-logi")
	before, _ := time.Parse(time.RFC3339, "2026-11-30T23:59:59Z")
	if v := o.Exercise(g, a, true, before); !v.OK {
		t.Fatalf("到期前该成立：%+v", v)
	}
	at, _ := time.Parse(time.RFC3339, "2026-12-01T00:00:00Z")
	v := o.Exercise(g, a, true, at)
	if v.OK || v.Unknown {
		t.Fatalf("到期那一刻就该失效：%+v", v)
	}
}

// 没有权威源 = 无法判定（不是拒绝，也不是允许）。
func TestExerciseWithoutAuthority(t *testing.T) {
	o, _ := mustLoad(t, simVault(t))
	root, _ := o.grantByRef("root-trade")
	v := o.Exercise(root, Authority{}, false, authAt)
	if !v.Unknown {
		t.Fatalf("没有权威源必须判成「无法判定」：%+v", v)
	}
	if v.OK {
		t.Fatal("无法判定绝不能同时是允许")
	}
}

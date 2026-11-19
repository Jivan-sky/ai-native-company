package org

import (
	"os"
	"strings"
	"testing"
	"time"
)

// 例外的三条硬约束：没有理由、没有到期、超过 7 天 —— 缺任一条都不算例外。
func TestValidateExceptionRequiresReasonExpiryAndShortTTL(t *testing.T) {
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	base := Grant{Kind: KindException, Slug: "x", Reason: "客户现场断网", IssuedBy: "member:alice",
		ExpiresAt: "2026-11-22T09:00:00Z"}

	if p := ValidateGrant(base, at); len(p) != 0 {
		t.Fatalf("这条例外是合规的，却报了：%v", p)
	}

	cases := map[string]struct {
		g    Grant
		want string
	}{
		"缺理由":   {Grant{Kind: KindException, IssuedBy: "member:alice", ExpiresAt: "2026-11-22T09:00:00Z"}, "reason"},
		"缺签发":   {Grant{Kind: KindException, Reason: "r", ExpiresAt: "2026-11-22T09:00:00Z"}, "issued_by"},
		"缺到期":   {Grant{Kind: KindException, Reason: "r", IssuedBy: "m:a"}, "expires_at"},
		"超过七天":  {Grant{Kind: KindException, Reason: "r", IssuedBy: "m:a", ExpiresAt: "2026-12-19T09:00:00Z"}, "7 天"},
		"到期读不懂": {Grant{Kind: KindException, Reason: "r", IssuedBy: "m:a", ExpiresAt: "下周五"}, "RFC3339"},
	}
	for name, c := range cases {
		got := strings.Join(ValidateGrant(c.g, at), " | ")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s：该报 %q，实际 %q", name, c.want, got)
		}
	}
}

// 常规授权不受例外约束影响：老数据 kind 为空，一律当 grant，不能因此报错。
func TestValidateGrantLeavesNormalGrantsAlone(t *testing.T) {
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	if p := ValidateGrant(Grant{Slug: "g", Reason: ""}, at); len(p) != 0 {
		t.Fatalf("常规授权不该被例外的约束卡住：%v", p)
	}
}

// 写入口必须把 kind 落进文件 —— 否则例外会被静默降级成常规授权。
func TestAppendGrantWritesKindForException(t *testing.T) {
	vault := copyVault(t, "domains")
	if _, err := InitAuthority(vault, "member:alice", "测试"); err != nil {
		t.Fatalf("init: %v", err)
	}
	g := Grant{Slug: "exc-1", From: "member:alice", To: "member:bob", Action: "read",
		Object: "domains/x", IssuedBy: "member:alice", Kind: KindException,
		Reason: "客户现场断网", ExpiresAt: time.Now().Add(48 * time.Hour).Format(time.RFC3339)}
	p, err := AppendGrant(vault, g)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "kind: exception") {
		t.Fatalf("写盘丢了 kind —— 例外被降级成常规授权：\n%s", b)
	}
	// 读回来也要还是 exception（走 Load，与生产同一条路；ScanGrants 需要 Policy）
	o, err := Load(vault)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	found := false
	for _, x := range o.Grants {
		if x.Slug == "exc-1" {
			found = true
			if x.NormalizeKind() != KindException {
				t.Fatalf("读回来 kind 丢了，拿到 %q", x.Kind)
			}
		}
	}
	if !found {
		t.Fatal("写进去的例外没被扫出来")
	}
}

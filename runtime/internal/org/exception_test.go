package org

import (
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

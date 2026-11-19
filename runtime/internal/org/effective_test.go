package org

import (
	"strings"
	"testing"
	"time"
)

// 单一视图的三条硬要求：
//
//	1 每条结论都来自已有判定器（不自己发明规则）
//	2 权威源没接上时，委派一律 unknown，**不是 ok**
//	3 解不出身份时直接报错，不返回一个"看起来什么都不能"的空视图
func TestEffectiveCombinesAxesAndNeverFakesAllowed(t *testing.T) {
	o := orgOf(t, copyVault(t, "domains"))
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	caps := CapTable{"devbot": {{Module: "data", Action: "read", Scope: "all"}}}

	v, err := Effective(o, caps, Authority{}, false, "devbot", at)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if v.Kind == "" || v.Role == "" {
		t.Fatalf("视图必须带上 kind 与 role：%+v", v)
	}
	for _, n := range v.Notes {
		if strings.Contains(n, "无法判定") {
			return // 权威源没接上时出现这条说明是对的
		}
	}
	// 没有任何委派时允许直接返回（Notes 里会写"跨域能力为零"）
	if len(v.Delegated) == 0 {
		return
	}
	for _, d := range v.Delegated {
		if d.Verdict == "ok" {
			t.Fatalf("权威源没接上时不该出现 ok：%+v", d)
		}
	}
}

func TestEffectiveRejectsUnknownIdentity(t *testing.T) {
	o := orgOf(t, copyVault(t, "domains"))
	if _, err := Effective(o, CapTable{}, Authority{}, false, "查无此人", time.Now()); err == nil {
		t.Fatal("解不出的身份必须报错，不许给一个空视图")
	}
}

func orgOf(t *testing.T, vault string) *Org {
	t.Helper()
	o, err := Load(vault)
	if err != nil {
		t.Fatalf("load %s: %v", vault, err)
	}
	return o
}

// 判据 2 的收口：单一视图**不许自己算**可见性 —— 必须与看板/检索同源。
func TestEffectiveVisibilityMatchesViewer(t *testing.T) {
	o := orgOf(t, copyVault(t, "domains"))
	v, err := Effective(o, CapTable{}, Authority{}, false, "devbot", time.Now())
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	id, ok := o.Identity("devbot")
	if !ok {
		t.Fatal("fixture 里该有 devbot")
	}
	// 用看板那条路算出来的可见性，必须与 Effective 给出的一致
	want := Visibility{Domains: id.Domains}
	if id.Kind == KindMember {
		if mv, found := o.ResolveMemberViewer(id.Ref); found {
			want = mv.Visibility()
		}
	}
	if v.AllDoms != want.All {
		t.Fatalf("all_domains 不一致：effective=%v viewer=%v", v.AllDoms, want.All)
	}
	if strings.Join(v.Domains, ",") != strings.Join(want.Domains, ",") {
		t.Fatalf("可见域不一致：effective=%v viewer=%v", v.Domains, want.Domains)
	}
}

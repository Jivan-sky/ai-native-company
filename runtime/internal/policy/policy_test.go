package policy

import (
	"strings"
	"testing"
)

func pol() *Policy {
	return &Policy{Schema: Schema, DataSource: "demo", Default: "deny", Rules: []Rule{
		{Subject: "alice", Table: "orders", Columns: []string{"id", "name"}, Action: "allow"},
		{Subject: "alice", Table: "orders", Columns: []string{"total"}, Action: "mask"},
		{Subject: "*", Table: "*", Columns: []string{"id"}, Action: "allow"},
	}}
}

func TestActionResolution(t *testing.T) {
	p := pol()
	cases := []struct{ subj, table, col, want string }{
		{"alice", "orders", "name", "allow"},
		{"alice", "orders", "total", "mask"},
		{"alice", "orders", "secret", "deny"}, // 没规则 → 默认拒绝
		{"bob", "orders", "id", "allow"},      // 通配规则
		{"bob", "orders", "name", "deny"},
	}
	for _, c := range cases {
		if got := p.Action(c.subj, c.table, c.col); got != c.want {
			t.Errorf("%s/%s.%s = %s，期望 %s", c.subj, c.table, c.col, got, c.want)
		}
	}
}

func TestApplyMasksAndDenies(t *testing.T) {
	p := pol()
	cols := []string{"id", "name", "total"}
	rows := [][]*string{{strp("1"), strp("alice"), strp("18.00")}}
	dec, err := p.Apply("alice", map[string]string{"id": "orders", "name": "orders", "total": "orders"},
		map[string]bool{"id": true, "name": true, "total": true}, cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Masked) != 1 || dec.Masked[0] != "total" {
		t.Fatalf("应当只脱敏 total：%+v", dec)
	}
	if *rows[0][2] != "***" {
		t.Fatalf("值没被替换：%q", *rows[0][2])
	}
	if *rows[0][1] != "alice" {
		t.Fatal("允许的列不该被动")
	}

	// 越权列：整条拒绝，且报出是哪一列
	_, err = p.Apply("bob", map[string]string{"name": "orders"}, map[string]bool{"id": true, "name": true},
		[]string{"id", "name"}, [][]*string{{strp("1"), strp("x")}})
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("应当拒绝并指出列名：%v", err)
	}
}

// 没有策略 = 全部拒绝。
func TestNilPolicyDeniesAll(t *testing.T) {
	var p *Policy
	if got := p.Action("alice", "orders", "id"); got != "deny" {
		t.Fatalf("nil 策略必须默认拒绝，实际 %s", got)
	}
}

func strp(s string) *string { return &s }

// 派生列（目录里没有的列，比如 count(*)）：默认拒绝；显式 derived=allow 才放行，且被标记。
func TestDerivedColumns(t *testing.T) {
	p := pol()
	p.Rules = nil
	p.Default = "deny"
	rows := [][]*string{{strp("3")}}
	if _, err := p.Apply("alice", nil, map[string]bool{"id": true}, []string{"count"}, rows); err == nil {
		t.Fatal("默认应当拒绝派生列")
	}
	p.Derived = "allow"
	dec, err := p.Apply("alice", nil, map[string]bool{"id": true}, []string{"count"}, rows)
	if err != nil {
		t.Fatalf("显式允许后应当放行：%v", err)
	}
	if len(dec.Derived) != 1 || dec.Derived[0] != "count" {
		t.Fatalf("派生列要标出来：%+v", dec)
	}
	// 目录里已知的列不受 derived 影响，仍按策略（这里是默认拒绝）
	if _, err := p.Apply("alice", nil, map[string]bool{"id": true}, []string{"id"}, [][]*string{{strp("1")}}); err == nil {
		t.Fatal("已知列不该被 derived 放行")
	}
}

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
	dec, err := p.Apply("alice", map[string]string{"id": "orders", "name": "orders", "total": "orders"}, cols, rows)
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
	_, err = p.Apply("bob", map[string]string{"name": "orders"}, []string{"id", "name"}, [][]*string{{strp("1"), strp("x")}})
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

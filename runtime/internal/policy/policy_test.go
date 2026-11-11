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

// 文件目录用前缀规则：dir/* → allow。
func TestPrefixMatchForPaths(t *testing.T) {
	p := &Policy{Schema: Schema, DataSource: "kb", Default: "deny", Rules: []Rule{
		{Subject: "alice", Table: "10-knowledge/*", Columns: []string{"*"}, Action: "allow"},
	}}
	if got := p.Action("alice", "10-knowledge", "*"); got != "allow" {
		t.Fatalf("目录本身也应当命中前缀：%s", got)
	}
	if got := p.Action("alice", "10-knowledge/a/b.md", "*"); got != "allow" {
		t.Fatalf("前缀应当命中：%s", got)
	}
	if got := p.Action("alice", "20-private/x.md", "*"); got != "deny" {
		t.Fatalf("前缀外应当拒绝：%s", got)
	}
	if got := p.Action("bob", "10-knowledge/a.md", "*"); got != "deny" {
		t.Fatalf("别人应当拒绝：%s", got)
	}
}

// 行过滤：只包一层，不解析 SQL；表名出现才套。
func TestWrapRowFilters(t *testing.T) {
	p := &Policy{Schema: Schema, DataSource: "demo", Default: "deny",
		RowFilters: []RowFilter{{Table: "orders", Where: "region = 'cn'"}}}
	wrapped, applied, werr := p.Wrap("SELECT id, region FROM orders WHERE id > 0")
	if werr != nil {
		t.Fatalf("不该报错：%v", werr)
	}
	if len(applied) != 1 || !strings.Contains(wrapped, "anc_rls") || !strings.Contains(wrapped, "region = 'cn'") {
		t.Fatalf("没套上行过滤：%s %v", wrapped, applied)
	}
	// 别的表：不套
	if out, ap, _ := p.Wrap("SELECT id FROM users"); out != "SELECT id FROM users" || len(ap) != 0 {
		t.Fatalf("不该套：%s %v", out, ap)
	}
}

func TestRowFilterValidation(t *testing.T) {
	p := Policy{Schema: Schema, DataSource: "demo", RowFilters: []RowFilter{{Table: "t", Where: "a=1; DROP"}}}
	if err := p.validate("demo"); err == nil {
		t.Fatal("where 里有分号必须被拒")
	}
}

// 行过滤引用的列不在查询里：执行前就给可操作的理由，而不是让数据库报列不存在。
func TestWrapRequiresColumn(t *testing.T) {
	p := &Policy{Schema: Schema, DataSource: "demo", Default: "deny",
		RowFilters: []RowFilter{{Table: "orders", Where: "region = 'cn'"}}}
	if _, _, err := p.Wrap("SELECT id FROM orders"); err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("应当提示缺列：%v", err)
	}
}

package catalog

import (
	"strings"
	"testing"
)

// 标识符里的引号必须翻倍：表名列名来自目录，也要按最坏情况处理。
func TestBuildValueSQLQuotesIdentifiers(t *testing.T) {
	sql := buildValueSQL(`we"ird`, `co"l`, DefaultRules())
	if !strings.Contains(sql, `"we""ird"`) || !strings.Contains(sql, `"co""l"`) {
		t.Fatalf("标识符没转义：%s", sql)
	}
	if !strings.Contains(sql, "FILTER (WHERE") || !strings.Contains(sql, "~ '") {
		t.Fatalf("聚合写法不对：%s", sql)
	}
	if !strings.Contains(sql, "LIMIT 2000") {
		t.Fatalf("要限制采样行数：%s", sql)
	}
}

func TestConfirmValidation(t *testing.T) {
	s := snap(tbl("orders", col("email", "text")))
	if _, err := Confirm(s, "orders", "email", "pii", "", ""); err == nil {
		t.Fatal("必须记谁拍的板")
	}
	if _, err := Confirm(s, "orders", "email", "secret", "alice", ""); err == nil {
		t.Fatal("级别要合法")
	}
	if _, err := Confirm(s, "orders", "nope", "pii", "alice", ""); err == nil {
		t.Fatal("目录里没有的列不许确认")
	}
	out, err := Confirm(s, "orders", "email", "pii", "alice", "值形状命中")
	if err != nil {
		t.Fatal(err)
	}
	if out.Classifications["orders.email"].Level != "pii" || out.Classifications["orders.email"].By != "alice" {
		t.Fatalf("拍板没记下：%+v", out.Classifications)
	}
}

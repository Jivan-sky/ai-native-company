package catalog

import "testing"

func snap(tables ...Table) Snapshot {
	s := Snapshot{Schema: Schema, DataSource: "demo", Tables: tables}
	s.Fingerprint = Fingerprint(s)
	return s
}

func tbl(name string, cols ...Column) Table { return Table{Name: name, Columns: cols} }
func col(name, typ string) Column           { return Column{Name: name, Type: typ} }

func TestFingerprintIgnoresTime(t *testing.T) {
	a := snap(tbl("t", col("id", "integer")))
	// 深拷贝：切片是共享的，直接赋值会连 Tables 一起改。
	b := snap(tbl("t", col("id", "integer")))
	b.ScannedAt = a.ScannedAt.Add(3600)
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("指纹不该受扫描时间影响")
	}
	b.Tables[0].Columns[0].Type = "text"
	if Fingerprint(a) == Fingerprint(b) {
		t.Fatal("类型变了指纹必须变")
	}
}

func TestDiffDetectsRename(t *testing.T) {
	old := snap(tbl("orders", col("id", "integer"), col("amount", "numeric")))
	cur := snap(tbl("orders", col("id", "integer"), col("total", "numeric")))
	ch := Diff(old, cur)
	if len(ch) != 1 || ch[0].Kind != "renamed_column" {
		t.Fatalf("应识别为改名：%+v", ch)
	}
	if ch[0].Column != "total" {
		t.Fatalf("改名后的列名不对：%+v", ch[0])
	}
}

func TestDiffAddRemoveChange(t *testing.T) {
	// 两增一删：不满足「一对一」的改名判据，应当老老实实报增/删/改。
	old := snap(tbl("t", col("a", "integer"), col("b", "text")))
	cur := snap(tbl("t", col("a", "bigint"), col("c", "text"), col("d", "integer")), tbl("t2", col("x", "text")))
	ch := Diff(old, cur)
	kinds := map[string]int{}
	for _, c := range ch {
		kinds[c.Kind]++
	}
	if kinds["changed_column"] != 1 || kinds["added_column"] != 2 ||
		kinds["removed_column"] != 1 || kinds["added_table"] != 1 {
		t.Fatalf("diff 分类不对：%+v", ch)
	}
}

func TestDiffRemovedTable(t *testing.T) {
	old := snap(tbl("a"), tbl("b"))
	cur := snap(tbl("a"))
	ch := Diff(old, cur)
	if len(ch) != 1 || ch[0].Kind != "removed_table" || ch[0].Table != "b" {
		t.Fatalf("删表没报出来：%+v", ch)
	}
}

func TestClassify(t *testing.T) {
	s := snap(tbl("users",
		col("id", "integer"), col("email", "text"), col("salary", "numeric"), col("name", "text")))
	got := Classify(s)
	if len(got) != 2 {
		t.Fatalf("应当给出 2 条建议：%+v", got)
	}
	levels := map[string]string{}
	for _, g := range got {
		levels[g.Column] = g.Level
	}
	if levels["email"] != "pii" || levels["salary"] != "sensitive" {
		t.Fatalf("分级不对：%+v", levels)
	}
}

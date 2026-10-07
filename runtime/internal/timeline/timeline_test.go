package timeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mk(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func ent(id, caseID, at, status, title string) Entry {
	return Entry{ID: id, Case: caseID, At: at, Status: status, Title: title, By: "demo-alice"}
}

// ---- 口径 2：词表不锁死 ----

func TestBand_已知词与未知词(t *testing.T) {
	cases := map[string]string{
		"done":    BandGreen,
		"running": BandYellow,
		"blocked": BandRed,
		"failed":  BandRed,
		"DONE":    BandGreen,   // 大小写不敏感
		" done ":  BandGreen,   // 前后空白不敏感
		"waiting": BandUnknown, // 以后加的词：不报错，落灰档
		"":        BandUnknown, // 没写状态也不算错
	}
	for in, want := range cases {
		if got := Band(in); got != want {
			t.Errorf("Band(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- 口径 3：一个 case 多行 = 一次推进 ----

func TestFold_取最后一条当当前态_历史一行不丢(t *testing.T) {
	es := []Entry{
		ent("t1", "trade-q3", "2026-10-01T09:00:00+08:00", "running", "开始对接"),
		ent("t2", "trade-q3", "2026-10-02T09:00:00+08:00", "running", "卡在权限"),
		ent("t3", "trade-q3", "2026-10-03T09:00:00+08:00", "blocked", "等 Alice 拍板"),
	}
	cs := Fold(es)
	if len(cs) != 1 {
		t.Fatalf("折出 %d 个 case，want 1", len(cs))
	}
	c := cs[0]
	if c.Entries != 3 {
		t.Errorf("entries = %d, want 3", c.Entries)
	}
	if c.Status != "blocked" || c.Band != BandRed {
		t.Errorf("当前态 = %q/%q, want blocked/%s", c.Status, c.Band, BandRed)
	}
	if c.Title != "等 Alice 拍板" {
		t.Errorf("title = %q, want 最后一条的标题", c.Title)
	}
	if c.FirstAt != "2026-10-01T09:00:00+08:00" || c.LastAt != "2026-10-03T09:00:00+08:00" {
		t.Errorf("区间 = %s..%s", c.FirstAt, c.LastAt)
	}
	if len(c.Recent) != 3 {
		t.Fatalf("recent %d 条, want 3", len(c.Recent))
	}
	if c.Recent[0].ID != "t3" || c.Recent[2].ID != "t1" {
		t.Errorf("recent 应最新在上：got %s..%s", c.Recent[0].ID, c.Recent[2].ID)
	}
}

func TestFold_后面只补进展时不抹掉责任人(t *testing.T) {
	es := []Entry{
		{ID: "a", Case: "c", At: "2026-10-01T09:00:00+08:00", Status: "running", Title: "起", By: "demo-alice", To: "Alice", Role: "经理"},
		{ID: "b", Case: "c", At: "2026-10-02T09:00:00+08:00", Status: "running", Title: "进展", By: "demo-alice"},
	}
	c := Fold(es)[0]
	if c.To != "Alice" || c.Role != "经理" {
		t.Errorf("To/Role = %q/%q, want 沿用上一条", c.To, c.Role)
	}
}

func TestFold_超过5条只留最新5条(t *testing.T) {
	var es []Entry
	for i := 0; i < 8; i++ {
		es = append(es, ent("t", "c", "2026-10-0"+string(rune('1'+i))+"T09:00:00+08:00", "running", "第 n 步"))
	}
	c := Fold(es)[0]
	if c.Entries != 8 {
		t.Errorf("entries = %d, want 8（计数要全，只有 recent 截断）", c.Entries)
	}
	if len(c.Recent) != recentKeep {
		t.Errorf("recent = %d, want %d", len(c.Recent), recentKeep)
	}
}

func TestFold_case为空时自己一条(t *testing.T) {
	cs := Fold([]Entry{
		ent("t1", "", "2026-10-01T09:00:00+08:00", "done", "独立一条"),
		ent("t2", "", "2026-10-02T09:00:00+08:00", "done", "另一条"),
	})
	if len(cs) != 2 {
		t.Fatalf("want 2 个 case，got %d（没有 case 字段时不许折叠到一起）", len(cs))
	}
	if cs[0].Key != "t2" {
		t.Errorf("应按最后活动倒序，got key=%s", cs[0].Key)
	}
}

// ---- 口径 1：行文本，读不懂的行要报出来 ----

func TestLoad_目录不存在是空时间线不是错误(t *testing.T) {
	doc, err := Load(mk(t))
	if err != nil {
		t.Fatalf("err = %v, want nil（刚 init 的机器不该红）", err)
	}
	if doc.Len() != 0 || len(doc.Bad) != 0 {
		t.Errorf("want 空, got %d 条 / %d 条坏行", doc.Len(), len(doc.Bad))
	}
}

func TestLoad_坏行逐条列出且不影响好行(t *testing.T) {
	v := mk(t)
	dir := filepath.Join(v, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"id":"ok1","at":"2026-10-01T09:00:00+08:00","title":"好行"}`,
		`这不是 JSON`,
		`{"id":"","at":"2026-10-01T09:00:00+08:00","title":"缺 id"}`,
		`{"id":"ok2","at":"不是时间","title":"时间坏了"}`,
		``,
		`# 注释行不算坏行`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "2026-10.demo-alice.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Len() != 1 || doc.Entries[0].ID != "ok1" {
		t.Errorf("好行应照常读出，got %d 条", doc.Len())
	}
	if len(doc.Bad) != 3 {
		t.Fatalf("坏行 = %v, want 3 条", doc.Bad)
	}
	if doc.Bad[0] != "2026-10.demo-alice.jsonl:2" {
		t.Errorf("坏行要带文件名:行号，got %q", doc.Bad[0])
	}
}

func TestLoad_非文件后缀不读(t *testing.T) {
	v := mk(t)
	dir := filepath.Join(v, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(`{"id":"x","at":"2026-10-01T09:00:00+08:00","title":"不该被读"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Len() != 0 {
		t.Errorf("只读 *.jsonl，got %d 条", doc.Len())
	}
}

// ---- 写入：按作者分片 ----

func TestAppend_按作者分片_两个人不共文件(t *testing.T) {
	v := mk(t)
	p1, err := Append(v, Entry{ID: "a", At: "2026-10-08T02:41:00+08:00", Title: "甲记的", By: "demo-alice"})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Append(v, Entry{ID: "b", At: "2026-10-08T02:42:00+08:00", Title: "乙记的", By: "demo-bob"})
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatalf("两个作者写到同一个文件了：%s（并发会互相踩）", p1)
	}
	if filepath.Base(p1) != "2026-10.demo-alice.jsonl" {
		t.Errorf("文件名 = %s, want 2026-10.demo-alice.jsonl", filepath.Base(p1))
	}
	doc, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Len() != 2 {
		t.Errorf("读回 %d 条, want 2", doc.Len())
	}
}

func TestAppend_必填项缺了就报错不写(t *testing.T) {
	v := mk(t)
	bad := []struct {
		name string
		e    Entry
	}{
		{"缺 id", Entry{At: "2026-10-08T02:41:00+08:00", Title: "t", By: "a"}},
		{"缺 title", Entry{ID: "x", At: "2026-10-08T02:41:00+08:00", By: "a"}},
		{"缺 by", Entry{ID: "x", At: "2026-10-08T02:41:00+08:00", Title: "t"}},
		{"at 不是 RFC3339", Entry{ID: "x", At: "2026/10/08", Title: "t", By: "a"}},
		{"by 全是中文（文件名叫不出来）", Entry{ID: "x", At: "2026-10-08T02:41:00+08:00", Title: "t", By: "张三"}},
	}
	for _, c := range bad {
		if _, err := Append(v, c.e); err == nil {
			t.Errorf("%s：应当报错", c.name)
		}
	}
	if _, err := os.Stat(filepath.Join(v, DirName)); !os.IsNotExist(err) {
		t.Errorf("报错时不该建目录/写文件")
	}
}

func TestAppend_往返一致(t *testing.T) {
	v := mk(t)
	in := Entry{
		ID: "t-1", Case: "c-1", At: "2026-10-08T02:41:00+08:00", Kind: "decision",
		Status: "done", By: "demo-alice", Role: "经理", To: "Alice", Domain: "trade",
		Title: "拍板走 B", Detail: "详细理由", Refs: []string{"charters/trade-q3"},
	}
	if _, err := Append(v, in); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Len() != 1 {
		t.Fatalf("读回 %d 条", doc.Len())
	}
	got := doc.Entries[0]
	if got.ID != in.ID || got.Kind != in.Kind || got.Status != in.Status || got.To != in.To || len(got.Refs) != 1 {
		t.Errorf("往返不一致：\ngot  %+v\nwant %+v", got, in)
	}
}

func TestTally(t *testing.T) {
	cs := []Case{
		{Band: BandGreen}, {Band: BandYellow}, {Band: BandRed},
		{Band: BandRed}, {Band: BandUnknown},
	}
	n := Tally(cs)
	if n.Green != 1 || n.Yellow != 1 || n.Red != 2 || n.Unknown != 1 {
		t.Errorf("Tally = %+v, want 1/1/2/1", n)
	}
}

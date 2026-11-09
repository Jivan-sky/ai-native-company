package pricing

import (
	"os"
	"path/filepath"
	"testing"
)

// 出厂表必须是**空**的：我们不知道挂牌价，编一个数比没有更坏。
func TestBuiltinIsEmptyButValid(t *testing.T) {
	tb, err := Load("")
	if err != nil {
		t.Fatalf("出厂表要能读：%v", err)
	}
	if tb.Schema != Schema {
		t.Fatalf("schema=%q", tb.Schema)
	}
	if len(tb.Entries) != 0 {
		t.Fatalf("出厂表应当是空的（编价格比没有更坏），实际 %d 条", len(tb.Entries))
	}
	if _, ok := tb.Lookup("deepseek-flash"); ok {
		t.Fatal("空表里不该查得到任何模型")
	}
}

func TestLookupAndCost(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "prices.json")
	body := `{"schema":"anc.prices/v1","currency":"USD","unit":"per_1m_tokens","entries":[
	  {"model":"DeepSeek-Flash","in":0.1,"out":0.2,"cache_read":0.01,"cache_write":0.1,"effective_from":"2026-11-01"}
	]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tb, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	// 大小写不敏感：记录里各家写法不同。
	e, ok := tb.Lookup("deepseek-flash")
	if !ok {
		t.Fatal("大小写不该影响查找")
	}
	// 四类各自计价：80086*0.1 + 72052*0.2 + 2227840*0.01 = 44697.4 → /1e6
	got := e.Cost(80086, 72052, 2227840, 0)
	if diff := got - 0.0446974; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("算钱不对：%v", got)
	}
	if _, ok := tb.Lookup("nope"); ok {
		t.Fatal("没登记的模型不该查到 —— 不许按相似模型估")
	}
}

func TestLoadRejectsBadTables(t *testing.T) {
	cases := map[string]string{
		"schema 不对": `{"schema":"anc.prices/v0","entries":[]}`,
		"没 model":   `{"schema":"anc.prices/v1","entries":[{"in":1}]}`,
		"重复模型": `{"schema":"anc.prices/v1","entries":[
			{"model":"a","in":1},{"model":"A","in":2}]}`,
	}
	for name, body := range cases {
		dir := t.TempDir()
		p := filepath.Join(dir, "p.json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil {
			t.Errorf("%s：应当报错", name)
		}
	}
}

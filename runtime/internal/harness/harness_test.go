package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultTableHasClaudeReader(t *testing.T) {
	f := DefaultFamily()
	if f.ID != DefaultID {
		t.Fatalf("默认家是 %q，想要 %q", f.ID, DefaultID)
	}
	if err := f.CanRead(); err != nil {
		t.Fatalf("默认家读不了：%v", err)
	}
	if got := f.MainPath("/r", "abc"); got != filepath.Join("/r", "abc.jsonl") {
		t.Errorf("MainPath = %q", got)
	}
}

func TestLookupIsCaseInsensitive(t *testing.T) {
	tb := Default()
	if _, ok := tb.Lookup("CLAUDE"); !ok {
		t.Error("大写 id 查不到")
	}
	if _, ok := tb.Lookup("nope"); ok {
		t.Error("不存在的 id 却查到了")
	}
}

func TestResolveRootFlagBeatsEnvBeatsHome(t *testing.T) {
	f := DefaultFamily()
	t.Setenv("CLAUDE_CONFIG_DIR", "/from/env")
	if got, why := f.ResolveRoot("/from/flag"); got != "/from/flag" || why != "旗标指定" {
		t.Errorf("旗标没赢：%q / %q", got, why)
	}
	if got, why := f.ResolveRoot(""); got != "/from/env" || why != "$CLAUDE_CONFIG_DIR" {
		t.Errorf("环境变量没赢：%q / %q", got, why)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("拿不到用户目录")
	}
	want := filepath.Join(home, ".claude")
	if got, why := f.ResolveRoot(""); got != want || !strings.Contains(why, ".claude") {
		t.Errorf("兜底不对：%q / %q，想要 %q", got, why, want)
	}
}

func TestTranscriptDirUsesLayoutNotCode(t *testing.T) {
	f := DefaultFamily()
	got, err := f.TranscriptDir("/root", `D:\work\alice`)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/root", "projects", "D--work-alice")
	if got != want {
		t.Errorf("TranscriptDir = %q，想要 %q", got, want)
	}
}

func TestSubagentPaths(t *testing.T) {
	f := DefaultFamily()
	glob, meta := f.SubagentPaths("/r", "abc")
	if glob != filepath.Join("/r", "abc", "subagents", "*.jsonl") {
		t.Errorf("子任务 glob = %q", glob)
	}
	if meta != ".meta.json" {
		t.Errorf("子任务元数据后缀 = %q", meta)
	}
	// 没声明子任务记录的家：glob 空，不编一个出来。
	naked := Family{ID: "x", Display: "X", RecordFormat: "jsonl-plain"}
	if g, _ := naked.SubagentPaths("/r", "a"); g != "" {
		t.Errorf("没声明却有 glob：%q", g)
	}
}

func TestCanReadSaysWhyWhenNoReader(t *testing.T) {
	tb := Default()
	f, ok := tb.Lookup("codebuddy")
	if !ok {
		t.Skip("表里没有 codebuddy")
	}
	if f.Reader != "" {
		t.Skip("codebuddy 已经有读取器了，这条测试该换个样本")
	}
	err := f.CanRead()
	if err == nil {
		t.Fatal("没有读取器却说不报错")
	}
	if _, ok := err.(ErrNoReader); !ok {
		t.Fatalf("错误类型是 %T，想要 ErrNoReader", err)
	}
	for _, want := range []string{"codebuddy", "只登记了口径"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息里没有 %q：%s", want, err.Error())
		}
	}
}

func TestSlugRule(t *testing.T) {
	got, err := Slug(RuleNonalnumDash, `D:\Agetn_Context\c\a`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "D--Agetn-Context-c-a" {
		t.Errorf("Slug = %q", got)
	}
	if _, err := Slug("不存在的规则", "x"); err == nil {
		t.Error("不认识的规则却不报错")
	}
}

func TestLoadOverridesWholeTable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.json")
	body := `{"schema":"anc.harnesses/v1","families":[{"id":"solo","display":"Solo",
	  "root_dir":".solo","record_format":"jsonl-plain","session_handle":"file-stem","reader":"x",
	  "layout":{"dir_rule":"nonalnum-to-dash","projects_dir":"p","main_file":"<id>.jsonl"}}]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tb, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tb.Lookup("claude"); ok {
		t.Error("给了文件却还留着内置的家 —— 说明是合并，不是整份替换")
	}
	if _, ok := tb.Lookup("solo"); !ok {
		t.Error("文件里的家没进来")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("文件不存在却不报错")
	}
}

func TestValidateRejectsBadTables(t *testing.T) {
	cases := map[string]string{
		"schema 不对":   `{"schema":"x","families":[{"id":"a","display":"A","record_format":"jsonl-plain","layout":{"dir_rule":"nonalnum-to-dash","projects_dir":"p","main_file":"<id>.jsonl"}}]}`,
		"id 重复":       `{"schema":"anc.harnesses/v1","families":[{"id":"a","display":"A","record_format":"jsonl-plain","layout":{"dir_rule":"nonalnum-to-dash","projects_dir":"p","main_file":"<id>.jsonl"}},{"id":"a","display":"B","record_format":"jsonl-plain","layout":{"dir_rule":"nonalnum-to-dash","projects_dir":"p","main_file":"<id>.jsonl"}}]}`,
		"规则不认识":       `{"schema":"anc.harnesses/v1","families":[{"id":"a","display":"A","record_format":"jsonl-plain","layout":{"dir_rule":"猜的","projects_dir":"p","main_file":"<id>.jsonl"}}]}`,
		"少了 projects": `{"schema":"anc.harnesses/v1","families":[{"id":"a","display":"A","record_format":"jsonl-plain","layout":{"dir_rule":"nonalnum-to-dash","main_file":"<id>.jsonl"}}]}`,
		"少了 display":  `{"schema":"anc.harnesses/v1","families":[{"id":"a","record_format":"jsonl-plain","layout":{"dir_rule":"nonalnum-to-dash","projects_dir":"p","main_file":"<id>.jsonl"}}]}`,
		"families 空":  `{"schema":"anc.harnesses/v1","families":[]}`,
	}
	for name, body := range cases {
		p := filepath.Join(t.TempDir(), "h.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil {
			t.Errorf("%s：不合法却过了", name)
		}
	}
}

func TestNoRecordFamilyNeedsNoLayout(t *testing.T) {
	body := `{"schema":"anc.harnesses/v1","families":[{"id":"n","display":"N","record_format":"none"}]}`
	p := filepath.Join(t.TempDir(), "h.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Errorf("没有记录的家不该要求布局：%v", err)
	}
}

package models

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExampleRegistryAndBinding(t *testing.T) {
	reg, err := LoadRegistry(filepath.Join("..", "..", "testdata", "registry", "registry.json"))
	if err != nil {
		t.Fatalf("示例注册表应当通过：%v", err)
	}
	if len(reg.Entries) != 2 {
		t.Fatalf("示例应有 2 条：%+v", reg.Entries)
	}
	if _, ok := reg.Lookup("qwen-max-prod"); !ok {
		t.Fatal("按 id 找不到")
	}
	b, err := LoadBinding(filepath.Join("..", "..", "testdata", "registry", "apps", "invoice-sweep.json"), reg)
	if err != nil {
		t.Fatalf("示例绑定应当通过：%v", err)
	}
	if b.Models[0] != "qwen-max-prod" || b.Egress.MaxBytes != 200000 {
		t.Fatalf("绑定读错：%+v", b)
	}
}

// 最重要的一条：**密钥值不许进注册表**（注册表要进 git）。
func TestRegistryRejectsKeyValueAsSecret(t *testing.T) {
	p := write(t, "r.json", `{"schema":"anc.models/v1","entries":[
	  {"id":"a","provider":"p","base_url":"https://x","model":"m","secret":"sk-1234567890abcdef",
	   "limits":{"rpm":1}}]}`)
	_, err := LoadRegistry(p)
	if err == nil || !strings.Contains(err.Error(), "不要把密钥值写进注册表") {
		t.Fatalf("把密钥值粘进来必须被拒：%v", err)
	}
}

func TestRegistryRejectsNoLimits(t *testing.T) {
	p := write(t, "r.json", `{"schema":"anc.models/v1","entries":[
	  {"id":"a","provider":"p","base_url":"https://x","model":"m","secret":"K"}]}`)
	if _, err := LoadRegistry(p); err == nil {
		t.Fatal("没有限额的条目必须被拒（等于无限授权）")
	}
}

func TestRegistryRejectsUnknownField(t *testing.T) {
	p := write(t, "r.json", `{"schema":"anc.models/v1","entries":[],"extra":1}`)
	if _, err := LoadRegistry(p); err == nil {
		t.Fatal("未知字段必须报错")
	}
}

func TestBindingRules(t *testing.T) {
	reg, err := LoadRegistry(filepath.Join("..", "..", "testdata", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"注册名不存在":   `{"schema":"anc.binding/v1","app":"x","models":["nope"],"egress":{"domains":["d"],"max_bytes":1},"run":{}}`,
		"egress 空": `{"schema":"anc.binding/v1","app":"x","models":["qwen-max-prod"],"egress":{"domains":[],"max_bytes":1},"run":{}}`,
		"没有上限":     `{"schema":"anc.binding/v1","app":"x","models":["qwen-max-prod"],"egress":{"domains":["d"],"max_bytes":0},"run":{}}`,
	}
	for name, body := range cases {
		if _, err := LoadBinding(write(t, "b.json", body), reg); err == nil {
			t.Errorf("%s：应当报错", name)
		}
	}
}

func TestMask(t *testing.T) {
	if got := Mask(""); got != "（未配置）" {
		t.Fatalf("空值：%q", got)
	}
	if got := Mask("abcd"); got != "****" {
		t.Fatalf("太短：%q", got)
	}
	if got := Mask("sk-abcdef123456"); got != "****3456" {
		t.Fatalf("尾巴四位：%q", got)
	}
}

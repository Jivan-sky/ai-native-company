package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这一组用例吃的是**纯函数**那一腿：直接 NewRedactor / Apply / CleanRecord，
// 绕开 Effective() 那张进程级全局表 —— 它是包级共享状态，用例去动它会让并行的用例跟着抖。
// 全局那一腿由最后两条端到端用例覆盖（它们只**读**全局，不改）。
//
// 断言刻意不去锁「命中哪条规则」之外的细节（规则表的顺序是可以调的，
// 调了不该红），只锁三件真正是契约的事：**明文没了 / 键名还在 / 占位符写出了哪一类**。

// fakeSecret 是形态档一眼认得出的假密钥（`sk-` 前缀 + 足够长的串）。
const fakeSecret = "sk-ant-api03-Zz9Qq7Ww3Ee5Rr1Tt"

func testRedactor() *Redactor { return NewRedactor(BuiltinRedactRules(), nil) }

// 1 键名档：值抹成占位符，**键名留着** —— 键名不是秘密，回显它才知道抹掉的是哪一类。
func TestRedactKeyTierMasksValueKeepsKeyName(t *testing.T) {
	r := testRedactor()
	got, hits := r.Apply(`{"ANTHROPIC_AUTH_TOKEN": "a1b2c3d4e5f6g7h8"}`)
	if !strings.Contains(got, "ANTHROPIC_AUTH_TOKEN") {
		t.Fatalf("键名不该被抹掉，得到 %q", got)
	}
	if strings.Contains(got, "a1b2c3d4e5f6g7h8") {
		t.Fatalf("值还留着明文：%q", got)
	}
	if !strings.Contains(got, markOpen) {
		t.Fatalf("值该变成占位符，得到 %q", got)
	}
	if len(hits) == 0 {
		t.Fatal("命中的规则名不能为空 —— 占位符要写出「哪一类」")
	}
}

// 2 形态档：前缀原样保留（人还得看出这里是 bearer / 是哪个平台），后面那串抹掉。
func TestRedactFormTierKeepsPrefixMasksRest(t *testing.T) {
	r := testRedactor()
	cases := []struct{ in, keep string }{
		{"Bearer " + fakeSecret, "Bearer "},
		{"key=ghp_a1b2c3d4e5f6g7h8i9j0", "ghp_"},
		{"xoxb-1234-5678-abcdefghijkl", "xoxb-"},
		{"AKIAIOSFODNN7EXAMPLE", "AKIA"},
	}
	for _, c := range cases {
		got, hits := r.Apply(c.in)
		if got == c.in || len(hits) == 0 {
			t.Errorf("%q 该被抹，却是原样（命中 %v）", c.in, hits)
		}
		if strings.Contains(got, fakeSecret) {
			t.Errorf("%q：明文还在 %q", c.in, got)
		}
		if !strings.Contains(got, markOpen) {
			t.Errorf("%q：该有占位符，得到 %q", c.in, got)
		}
		if !strings.Contains(got, c.keep) {
			t.Errorf("%q：前缀 %q 该原样保留，得到 %q", c.in, c.keep, got)
		}
	}
}

// 2b 自带键名的 `Authorization:` 头：命中的是**键名档**（`Bearer` 那一整词会被键名档吃掉，
// 所以这里只钉「明文没了、留了占位符」，不钉哪个前缀活下来 —— 规则顺序是可调的）。
// 真正要保证的是：无论哪一档吃掉它，`sk-…` 那一串都不许留在输出里。
func TestRedactAuthorizationHeaderNeverLeavesTheToken(t *testing.T) {
	r := testRedactor()
	got, hits := r.Apply("Authorization: Bearer " + fakeSecret)
	if strings.Contains(got, fakeSecret) || strings.Contains(got, "sk-ant") {
		t.Fatalf("令牌还留在输出里：%q", got)
	}
	if !strings.Contains(got, markOpen) || len(hits) == 0 {
		t.Fatalf("该被抹并报出命中的规则名，得到 %q（命中 %v）", got, hits)
	}
}

// 3 不误伤：计量口径的词、已经是引用（指针）的、空值、短值 —— 一个都不许动。
// 抹了它们，审计就读不出用量、也看不出「这里写的是引用而不是明文」。
func TestRedactLeavesUsageCountersAndPointersAlone(t *testing.T) {
	r := testRedactor()
	for _, in := range []string{
		"input_tokens=1234",
		"output_tokens=5678",
		"max_tokens=4096",
		"token=${ANTHROPIC_AUTH_TOKEN}",
		`secret=""`,
		"password=abc",
	} {
		if got, hits := r.Apply(in); got != in || len(hits) != 0 {
			t.Errorf("%q 不该被抹，得到 %q（命中 %v）", in, got, hits)
		}
	}
}

// 4 不动点：形态叠着来（`Bearer sk-…` 两档都能命中）时，过完不许剩明文，
// 而且要停在一个再抹一遍也不变的状态 —— 不然占位符自己会被下一趟再啃一次。
func TestRedactRunsToFixpointOnStackedShapes(t *testing.T) {
	r := testRedactor()
	in := "Authorization: Bearer " + fakeSecret
	got, _ := r.Apply(in)
	if strings.Contains(got, fakeSecret) || strings.Contains(got, "sk-ant") {
		t.Fatalf("过完还留着明文：%q", got)
	}
	if !strings.Contains(got, markOpen) {
		t.Fatalf("该有占位符，得到 %q", got)
	}
	again, hits := r.Apply(got)
	if again != got || len(hits) != 0 {
		t.Errorf("该停在不动点，第二趟却变成 %q（命中 %v）", again, hits)
	}
}

// 5 端到端 · 写口：Append 落盘之后，**磁盘上**不许有那个明文。
// 这一条走 Effective() —— 也就是「没落定过就是出厂表」那条口径。
func TestAppendWritesRedactedToDisk(t *testing.T) {
	vault := t.TempDir()
	path, err := Append(vault, Record{
		ID: "w1", At: "2026-10-10T12:00:00+08:00", Actor: "alice",
		Action: ActionWrite, Result: ResultOK,
		Why: "写之前先把口令文件念了一遍：" + fakeSecret,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shard: %v", err)
	}
	if strings.Contains(string(raw), fakeSecret) {
		t.Fatalf("磁盘上还躺着明文：%s", raw)
	}
	if !strings.Contains(string(raw), markOpen) {
		t.Fatalf("该留下占位符（让人看出这里有过东西），得到 %s", raw)
	}
	doc, err := Load(vault)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if doc.Len() != 1 {
		t.Fatalf("该读回 1 条，得到 %d", doc.Len())
	}
	if strings.Contains(doc.Records[0].Why, fakeSecret) {
		t.Fatalf("读回来还带着明文：%q", doc.Records[0].Why)
	}
	if len(doc.Tainted) != 0 {
		t.Errorf("这条是写口脱敏之后落盘的，不该算「老明文」：%v", doc.Tainted)
	}
}

// 6 端到端 · 读口兜底：这份代码落地**之前**就躺在盘上的明文，
// 读出来必须是干净的，同时**如实报出位置**（Tainted），而**文件本身不许被改**
// —— 处置是轮换密钥 + 清文件，那是人的事，代码只能把位置指出来。
func TestLoadTaintsPreExistingPlaintextButDoesNotRewriteIt(t *testing.T) {
	vault := t.TempDir()
	dir := filepath.Join(vault, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	shard := filepath.Join(dir, "2026-10.alice.jsonl")
	line := `{"id":"old-1","at":"2026-10-01T09:00:00+08:00","actor":"alice","action":"read",` +
		`"result":"ok","why":"老明文 ` + fakeSecret + `"}`
	if err := os.WriteFile(shard, []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("write shard: %v", err)
	}
	doc, err := Load(vault)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if doc.Len() != 1 {
		t.Fatalf("该读回 1 条，得到 %d", doc.Len())
	}
	if strings.Contains(doc.Records[0].Why, fakeSecret) {
		t.Fatalf("读口兜底没生效，读出来还是明文：%q", doc.Records[0].Why)
	}
	if len(doc.Tainted) != 1 || doc.Tainted[0] != "2026-10.alice.jsonl:1" {
		t.Fatalf("该如实报出「落盘时就是明文」的位置，得到 %v", doc.Tainted)
	}
	raw, err := os.ReadFile(shard)
	if err != nil {
		t.Fatalf("read shard: %v", err)
	}
	if !strings.Contains(string(raw), fakeSecret) {
		t.Fatal("读口不该改文件 —— 抹的是显示，不是盘上那一份")
	}
}

// 7 已知值那一腿：按**值**字面替换（连没有键名、没有形态的也挡得住），
// 而且这个值**不许从任何出口出去** —— 包括回显规则表那个出口。
func TestKnownSecretIsMaskedAndNeverLeavesTheRedactor(t *testing.T) {
	const val = "Th1s-Is-A-Real-Token-Value"
	r := NewRedactor(BuiltinRedactRules(), []KnownSecret{{Name: "ANTHROPIC_AUTH_TOKEN", Value: val}})
	if r.KnownCount() != 1 {
		t.Fatalf("该收下 1 条已知值，得到 %d", r.KnownCount())
	}
	got, hits := r.Apply("口令=" + val)
	if strings.Contains(got, val) {
		t.Fatalf("已知值没被抹掉：%q", got)
	}
	if !strings.Contains(got, markOpen+"ANTHROPIC_AUTH_TOKEN"+markClose) {
		t.Fatalf("该抹成「已脱敏:ANTHROPIC_AUTH_TOKEN」（名字指认哪一类），得到 %q", got)
	}
	if len(hits) == 0 {
		t.Fatal("命中规则名不能为空")
	}
	for _, rule := range r.Rules() {
		if strings.Contains(rule.Name+rule.Match, val) {
			t.Fatalf("值从规则表出口漏出去了：%+v", rule)
		}
	}
	// 短值不登记：长度不够时抹它只会误伤正常文本，而它也不构成一个秘密。
	if n := NewRedactor(BuiltinRedactRules(), []KnownSecret{{Name: "x", Value: "abc"}}).KnownCount(); n != 0 {
		t.Fatalf("太短的值不该登记，得到 %d", n)
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"anc/internal/bench"
)

// benchGrant 是测试夹具用的一条逐主体授权（to: member:alice）。
// 为什么不写 role:manager：`org.IdentityForRef` 明确**不把岗位当主体**，
// 于是一条岗位授权不命中任何一个人的「与我相关」（见 DESIGN §7.1.71 的发现 2）。
const benchGrant = `---
grant:
  from: member:bob
  to: member:alice
  action: read
  object: 10-knowledge
  ttl: forever
  reason: 测试夹具
---

测试夹具。
`

// TestBenchCLISaysNo 是这条测量的**验收**：配置正确时全到手；抽掉一样东西，
// 同一份 suite 必须当场掉下来，而且说得出缺的是哪一样。
//
// 只测「能报数」是不够的 —— 一个只会报 100% 的测量器等于没有。
func TestBenchCLISaysNo(t *testing.T) {
	vault := copyFixture(t, "domains")
	grantPath := filepath.Join(vault, "grants", "read-knowledge.md")
	if err := os.MkdirAll(filepath.Dir(grantPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, grantPath, benchGrant)

	suite := t.TempDir()
	writeFile(t, filepath.Join(suite, "t1.json"), `{
  "id": "t1",
  "title": "夹具任务",
  "as": ["alice"],
  "needs": [
    {"kind": "domain", "value": "trade"},
    {"kind": "grant", "value": "read:10-knowledge"}
  ]
}`)

	out, code := capture(t, func() int {
		return cmdBench([]string{vault, "--suite", suite, "--json"})
	})
	if code != 0 {
		t.Fatalf("基线应当退出码 0，得到 %d：\n%s", code, out)
	}
	var rep bench.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("--json 出的不是报告：%v\n%s", err, out)
	}
	if len(rep.Tasks) != 1 || !rep.Tasks[0].PassK {
		t.Fatalf("配置正确时应当全到手：%+v", rep.Tasks)
	}
	if rep.Asks != 0 {
		t.Fatalf("全到手就不该有「要问人」：%+v", rep)
	}

	// 负控：抽掉授权文件。同一份 suite、同一棵树，只少这一样。
	if err := os.Remove(grantPath); err != nil {
		t.Fatal(err)
	}
	out, code = capture(t, func() int {
		return cmdBench([]string{vault, "--suite", suite, "--json"})
	})
	if code != 0 {
		t.Fatalf("掉分不等于失败退出（--min 才管退出码）：%d\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if rep.Tasks[0].PassK {
		t.Fatalf("授权都没了还算过 —— 这个测量器是橡皮图章：%+v", rep.Tasks)
	}
	if rep.Asks != 1 {
		t.Fatalf("缺一样就该记一次「要问人」：%+v", rep)
	}
	if got := rep.Tasks[0].Views[0].Needs[1]; got.Delivered || got.Kind != bench.KindGrant {
		t.Fatalf("掉的那一条应当是授权：%+v", got)
	}
}

// TestBenchMinGate：给了 --min 才用退出码表达「不够」；不给就只报数。
func TestBenchMinGate(t *testing.T) {
	vault := copyFixture(t, "domains")
	suite := t.TempDir()
	// 这棵树里没有 grants/，所以这条 need 必然没到手。
	writeFile(t, filepath.Join(suite, "t1.json"), `{
  "id": "t1",
  "as": ["alice"],
  "needs": [{"kind": "grant", "value": "read:10-knowledge"}]
}`)

	if _, code := capture(t, func() int {
		return cmdBench([]string{vault, "--suite", suite})
	}); code != 0 {
		t.Fatalf("不给 --min 时掉分不该让命令失败：%d", code)
	}
	if _, code := capture(t, func() int {
		return cmdBench([]string{vault, "--suite", suite, "--min", "0.9"})
	}); code != 1 {
		t.Fatalf("给了 --min 且没过，应当退出码 1，得到 %d", code)
	}
}

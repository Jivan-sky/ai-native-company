package division

import "testing"

// 三条规则各自要能命中，并说清是哪一条把它推到"人"这一侧。
func TestDecideRules(t *testing.T) {
	cases := []struct {
		name  string
		in    Case
		rule  string
		human bool
	}{
		{"跨数据优先于一切", Case{CrossData: true, Money: true}, "rule1", true},
		{"花钱", Case{Money: true}, "rule2", true},
		{"对外承诺", Case{Promise: true}, "rule2", true},
		{"动权限", Case{Permissions: true}, "rule2", true},
		{"不可逆", Case{Irreversible: true}, "rule3", true},
		{"可撤回、不花钱", Case{}, "else", false},
	}
	for _, c := range cases {
		v := Decide(c.in)
		if v.Rule != c.rule || v.Human != c.human {
			t.Errorf("%s：拿到 rule=%s human=%v，期望 %s/%v", c.name, v.Rule, v.Human, c.rule, c.human)
		}
		if v.Reason == "" || len(v.Path) == 0 {
			t.Errorf("%s：结论必须带理由与路径（可审计）", c.name)
		}
	}
}

// 同一组事实永远同一个结论（纯函数）。
func TestDecideIsDeterministic(t *testing.T) {
	c := Case{Money: true, Irreversible: true}
	a := Decide(c)
	for i := 0; i < 5; i++ {
		b := Decide(c)
		if a.Rule != b.Rule || a.Human != b.Human {
			t.Fatalf("同样的输入给出了不同结论：%+v vs %+v", a, b)
		}
	}
}

// 答案集与树必须一致；不一致要说清是哪条。
func TestSelfTestMatchesAnswerKey(t *testing.T) {
	res, err := SelfTest()
	if err != nil {
		t.Fatal(err)
	}
	if res.Total < 20 {
		t.Fatalf("答案集至少要 20 条（判据 11' 的样本量），拿到 %d", res.Total)
	}
	if len(res.Disagreed) != 0 {
		t.Fatalf("树与答案集打架：%v", res.Disagreed)
	}
	if res.Rate < 0.95 {
		t.Fatalf("一致率 %.2f 低于 95%%", res.Rate)
	}
}

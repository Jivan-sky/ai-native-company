package render

import (
	"strings"
	"testing"

	"anc/internal/org"
)

// 呈现档：**聊天窗是给人看的观测面，不是 agent 的工作日志。**
// 默认必须是 quiet —— 出厂就发「思考 + 每一次工具调用」会把窗口塞满，人反而看不清结论。
// 四档组合在这里一次钉死，避免以后有人「顺手」把默认改回 full。
func TestDisplayModeDrivesArtifact(t *testing.T) {
	cases := []struct {
		name       string
		display    string
		wantMode   string
		wantFooter string
	}{
		{"没写 = 出厂默认 quiet", "", org.DisplayQuiet, "reply_footer = false\n"},
		{"显式 quiet", org.DisplayQuiet, org.DisplayQuiet, "reply_footer = false\n"},
		{"compact 也隐藏思考与工具", org.DisplayCompact, org.DisplayCompact, "reply_footer = false\n"},
		{"full 是排查档，全开", org.DisplayFull, org.DisplayFull, "reply_footer = true\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, host := fixtureOrg(t, "six")
			o.Company.Display = c.display
			p, err := Build(o, Options{Host: host, Version: "test", Now: fixedNow})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(p.Text, "[display]\n") {
				t.Fatal("产物里没有 [display] 段：抹掉 = 沿用上游默认 full，等于把过程又放出来了")
			}
			if !strings.Contains(p.Text, "mode = \""+c.wantMode+"\"\n") {
				t.Fatalf("display=%q 应渲染成 mode=%q", c.display, c.wantMode)
			}
			if !strings.Contains(p.Text, c.wantFooter) {
				t.Fatalf("display=%q 的 footer 档位不对，期望产物含 %q", c.display, c.wantFooter)
			}
		})
	}
}

// footer 第二行会把 work_dir（本机绝对路径）推进 IM —— 与 SPEC §2.3 相冲，
// 所以非 full 档必须显式关掉它，不许只靠「上游默认」。
func TestDisplayQuietTurnsFooterOffExplicitly(t *testing.T) {
	o, host := fixtureOrg(t, "one")
	p, err := Build(o, Options{Host: host, Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Text, "reply_footer = false\n") {
		t.Fatal("默认档没有显式关 footer：等于把本机工作目录推进客户 IM")
	}
}

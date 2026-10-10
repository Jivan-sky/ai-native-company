package card

import (
	"os"
	"path/filepath"
	"testing"
)

// 样本一处收着：`runtime/testdata/card/`（见 README 的「结构」）。
// Go 的 `*_test.go` 必须与它测的包同目录（语言约束），所以代码在这儿、样本在那儿。
func sample(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "card", name))
	if err != nil {
		t.Fatalf("样本读不到：%v", err)
	}
	return raw
}

// ---- 进：同一件事的几种写法都收（2026-10-11 实测抓到的形状差异，见 DESIGN §7.1.41）----

func TestParseClick_收三种形状(t *testing.T) {
	cases := []struct {
		样本     string
		who    string // 谁点的
		chat   string // 点在哪
		msg    string // 哪条消息
		id     string // 对哪一条
		signal string // 点的什么
	}{
		{"click-flat.json", "ou_demo_alice", "oc_demo_chat_1", "om_demo_card_1", "prop-card-1", "approve"},
		{"click-nested.json", "ou_demo_alice", "oc_demo_chat_1", "om_demo_card_1", "prop-card-1", "approve"},
		{"click-values-object.json", "ou_demo_bob", "oc_demo_chat_2", "om_demo_card_2", "prop-card-2", "reject"},
	}
	for _, c := range cases {
		t.Run(c.样本, func(t *testing.T) {
			click, err := ParseClick(sample(t, c.样本))
			if err != nil {
				t.Fatalf("该收的没收：%v", err)
			}
			if click.OpenID != c.who || click.ChatID != c.chat || click.MessageID != c.msg {
				t.Errorf("谁/哪/哪条 = %q / %q / %q，want %q / %q / %q",
					click.OpenID, click.ChatID, click.MessageID, c.who, c.chat, c.msg)
			}
			id, signal, ok := click.Point()
			if !ok || id != c.id || signal != c.signal {
				t.Errorf("Point() = %q / %q / %v，want %q / %q / true", id, signal, ok, c.id, c.signal)
			}
		})
	}
}

// ---- 出：认不出就拒，不猜 ----

func TestParseClick_认不出就拒(t *testing.T) {
	for _, name := range []string{
		"click-no-action.json",    // 事件里根本没有 action：不知道点的是哪个按键
		"click-plain-string.json", // action_value 是普通串，不是 JSON 对象
		"click-not-json.json",     // 整个事件体就不是 JSON
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseClick(sample(t, name)); err == nil {
				t.Fatal("认不出却收了 —— 判据不许猜")
			}
		})
	}
}

// ---- 两个键都在才算数：事件本身好的，只是缺一个键 → 解析收下，Point 点不出来 ----

func TestClickPoint_两个键都在才算数(t *testing.T) {
	click, err := ParseClick(sample(t, "click-signal-only.json"))
	if err != nil {
		t.Fatalf("事件本身是好的（只缺 anc_id），不该在解析这一层就拒：%v", err)
	}
	if _, _, ok := click.Point(); ok {
		t.Fatal("只有 anc_signal 也算数了 —— 缺一个就不许猜")
	}
}

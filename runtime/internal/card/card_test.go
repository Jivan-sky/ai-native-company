package card

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// ---- 收口帧：出厂那份三个按键都在、都禁用，名字认得出 ----
//
// 收口的样子是「变暗」不是「撕掉」（2026-10-11 human 拍板，见 DESIGN §7.1.43）：
// 三个按键留着（看得出原来有哪几个选项），但一律 disabled（点不动）。
func TestDefaultDone_出厂收口帧三键都在但全禁用(t *testing.T) {
	f := DefaultDone()
	if f.Name != "approval-done" {
		t.Errorf("name = %q，want approval-done", f.Name)
	}
	found := false
	for _, b := range f.Blocks {
		if !strings.EqualFold(strings.TrimSpace(b.Kind), KindButtons) {
			continue
		}
		found = true
		if len(b.Buttons) != 3 {
			t.Fatalf("收口帧的按键数 = %d，want 3（三个都留着才看得出原来有哪几个选项）", len(b.Buttons))
		}
		for _, btn := range b.Buttons {
			if !btn.Disabled {
				t.Errorf("按键 %q 没有禁用 —— 收口之后点不动才算收口", btn.Signal)
			}
		}
	}
	if !found {
		t.Fatal("收口帧里一个 buttons 块都没有 —— 收口的样子是变暗，不是把按键撕掉")
	}
	c, notes := f.Render(map[string]string{
		"verdict": "同意", "title": "member:alice 申请 trade 的权", "tone": "green", "id": "prop-card-1",
		"body": "请开 trade 域的读权：import-q4 这批合同要对一下台账，只读，不写。",
		"by":   "alice", "via": "飞书卡片", "at": "2026-10-11T00:20:12Z",
	})
	if len(notes) != 0 {
		t.Errorf("槽位给齐了还喊：%v", notes)
	}
	if c.Header.Title != "同意 · member:alice 申请 trade 的权" {
		t.Errorf("标题 = %q", c.Header.Title)
	}
	var btns []CardButton
	for _, el := range c.Elements {
		if el.Kind == KindButtons {
			btns = append(btns, el.Buttons...)
		}
	}
	if len(btns) != 3 {
		t.Fatalf("渲染出来 %d 个按键，want 3", len(btns))
	}
	for _, b := range btns {
		if !b.Disabled {
			t.Errorf("渲染出来的 %q 没带 disabled —— 变暗这件事到不了出口", b.Label)
		}
	}
}

// 出口：帧说禁用的按键，飞书那份 JSON 里必须真带 disabled —— 变暗得送到平台上才算数。
func TestFeishuJSON_禁用按键带disabled(t *testing.T) {
	c, notes := DefaultDone().Render(map[string]string{
		"verdict": "同意", "title": "t", "tone": "green", "id": "prop-card-1",
		"body": "b", "by": "alice", "via": "飞书卡片", "at": "2026-10-11T00:20:12Z",
	})
	if len(notes) != 0 {
		t.Fatalf("槽位给齐了还喊：%v", notes)
	}
	raw, err := c.FeishuJSON()
	if err != nil {
		t.Fatalf("出口出错：%v", err)
	}
	var out struct {
		Elements []struct {
			Actions []struct {
				Disabled bool `json:"disabled"`
			} `json:"actions"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("出口不是合法 JSON：%v", err)
	}
	n := 0
	for _, el := range out.Elements {
		for _, a := range el.Actions {
			n++
			if !a.Disabled {
				t.Errorf("第 %d 个按键没带 disabled —— 平台上它还能点", n)
			}
		}
	}
	if n != 3 {
		t.Fatalf("出口里按键数 = %d，want 3", n)
	}
}

func TestBuiltin_出厂帧有两份(t *testing.T) {
	if f, ok := Builtin("approval"); !ok || f.Name != "approval" {
		t.Errorf("approval：ok=%v name=%q", ok, f.Name)
	}
	if f, ok := Builtin("approval-done"); !ok || f.Name != "approval-done" {
		t.Errorf("approval-done：ok=%v name=%q", ok, f.Name)
	}
	if _, ok := Builtin("没有这一份"); ok {
		t.Error("名字对不上却说有 —— 出厂的那几份不许瞎认")
	}
}

func TestFindFrame_收口帧按名字取得到(t *testing.T) {
	f, where, err := FindFrame("approval-done", "")
	if err != nil {
		t.Fatalf("找帧出错：%v", err)
	}
	if f.Name != "approval-done" {
		t.Errorf("取到的是 %q（%s）—— 按名字该拿到收口那份，不是待批那份", f.Name, where)
	}
}

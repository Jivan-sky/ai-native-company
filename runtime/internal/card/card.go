// Package card —— 卡片是**数据**：帧说长什么样，值由实时业务现填。
//
// 为什么要做成数据（2026-10-10 拍板。human 原话：「你不能把它写死了…甚至出一个粗糙框架
// 就可以，就是像按键之类的。但里面的键、值都得根据实时业务去填」）：
//
//	帧 Frame   —— 一张卡片长什么样：标题、几段文字、哪几列字、哪几个按键。
//	              **住在文件里**（--frame 指过去的那一份），改它不用碰代码。
//	值 values  —— 这一张卡片此刻要显示的东西：谁提的、等多久、该谁批…
//	              由**调用方**当场从真相源与热层算出来（map[string]string）。
//	卡 Card    —— 渲染结果，平台无关。
//
// 三件刻意的事：
//
//  1. **槽位缺了就喊。** 帧里写了 {{x}} 而值里没有 x，那一格渲染成「（没这个值：x）」，
//     并回一条 Note —— 不静默变空：空与「本来就没有」在卡片上分不出来。
//  2. **不认平台。** 本包只到 Card 为止。飞书那份 JSON 是**出口之一**（FeishuJSON）；
//     加钉钉 / 企微是再加一个出口，不是重写这一层。
//  3. **按键回程只带两个键**（anc_id / anc_signal）—— 判据永远只看这两个键，与 approvals
//     的「点头只认三个字」是同一条口径：口子开得越小，越骗不动。
package card

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Schema 是帧的版本。帧是**外置数据**，所以要能说清「这份是哪个格式的」——
// 以后格式变了，老帧不会静默按新规矩解释。
const Schema = "anc.card/v1"

// 按键回程带的两个键。**只此一处定义**：渲染与解析各写一遍，迟早对不上。
const (
	ClickIDKey     = "anc_id"     // 对哪一条（取自槽位 id）
	ClickSignalKey = "anc_signal" // 点的什么（帧里那个 signal，原样回来）
)

// 帧里认得的三种块。要加第四种 = 加一种 kind + 一个渲染分支（这是加语法，不是写死业务）。
const (
	KindText    = "text"
	KindFields  = "fields"
	KindButtons = "buttons"
)

// Frame 是一张卡片长什么样的**数据**。
//
// 只有 header.title 是必要的一句；blocks 可以一块都没有（那就只剩一个标题）——
// 帧写残了不拦，渲染时如实把缺的喊出来。
type Frame struct {
	Schema string  `json:"schema"`
	Name   string  `json:"name,omitempty"`
	Header Header  `json:"header"`
	Blocks []Block `json:"blocks,omitempty"`
}

// Header 是标题条。Tone 是颜色名（飞书那套：blue / green / red / orange / yellow / grey …）；
// 它通常写成一个槽位（{{tone}}）—— 颜色也得跟着实时的那件事走，不是帧里钉死的。
type Header struct {
	Title string `json:"title"`
	Tone  string `json:"tone,omitempty"`
}

// Block 是一种块。三种 kind 各用哪几个字段，见 Kind* 常量。
type Block struct {
	Kind    string   `json:"kind"`
	Text    string   `json:"text,omitempty"`    // text
	Items   []Item   `json:"items,omitempty"`   // fields
	Buttons []Button `json:"buttons,omitempty"` // buttons
}

// Item 是一列字：左边标签、右边值。两边都能写槽位。
type Item struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Button 是一个按键。
//
//   - Label：按键上写什么（可写槽位）
//   - Signal：**原样回程**的那个信号。它不查表、不校验 —— 认不认是回调那一侧的事
//     （同 approvals：只认三个字，别的拒，但拒在收的那一头，不在这里猜）
//   - Style：primary / default / danger（飞书那三个）
type Button struct {
	Label  string `json:"label"`
	Signal string `json:"signal"`
	Style  string `json:"style,omitempty"`
}

// Default 是**出厂帧**：粗糙的骨架 —— 一句标题 + 一段正文 + 几列字 + 一排按键。
//
// 它是**数据**，不是分支。客户要改：`anc approvals card --dump-frame > my.json` 拿走改，
// 再用 --frame 指回来。没有这一条，卡片长什么样就永远是代码说了算。
func Default() Frame {
	return Frame{
		Schema: Schema,
		Name:   "approval",
		Header: Header{Title: "待批 · {{title}}", Tone: "{{tone}}"},
		Blocks: []Block{
			{Kind: KindText, Text: "{{body}}"},
			{Kind: KindFields, Items: []Item{
				{Label: "谁提的", Value: "{{submitter}}"},
				{Label: "属于谁", Value: "{{submitter_owner}}"},
				{Label: "该谁批", Value: "{{approver}}"},
				{Label: "客体", Value: "{{target}}"},
				{Label: "进队", Value: "{{enqueued}}（已等 {{waited}}）"},
			}},
			{Kind: KindButtons, Buttons: []Button{
				{Label: "同意", Signal: "approve", Style: "primary"},
				{Label: "驳回", Signal: "reject", Style: "danger"},
				{Label: "挂起", Signal: "hold", Style: "default"},
			}},
		},
	}
}

// ParseFrame 读一份帧。**只认一版 schema**：别的版本不按这一版的规矩解释（宁可说读不懂）。
func ParseFrame(raw []byte) (Frame, error) {
	var f Frame
	if err := json.Unmarshal(raw, &f); err != nil {
		return Frame{}, fmt.Errorf("帧不是合法 JSON：%w", err)
	}
	if got := strings.TrimSpace(f.Schema); got != Schema {
		return Frame{}, fmt.Errorf("认不出的帧版本 %q —— 这一版只认 %s", got, Schema)
	}
	if strings.TrimSpace(f.Header.Title) == "" {
		return Frame{}, fmt.Errorf("帧没有 header.title —— 一张没有标题的卡片，收的人不知道这是什么")
	}
	return f, nil
}

// Encode 把帧打回 JSON（`--dump-frame` 用；人拿走改的就是这一份）。
func (f Frame) Encode() ([]byte, error) { return json.MarshalIndent(f, "", "  ") }

// Note 是渲染时发现的一件事。渲染**不因为发现就不出卡片** —— 出卡片 + 如实说哪里不对，
// 比整张卡不出现强（看板那几页就是这条口径）。
type Note struct {
	Where string // 哪一处：header / 块 2 / 按键 1
	What  string
}

// Card 是渲染结果，平台无关。
type Card struct {
	Header   Header
	Elements []Element
}

// Element 是卡片上的一块。
type Element struct {
	Kind    string // text / divider / fields / buttons
	Text    string
	Fields  []Item
	Buttons []CardButton
}

// CardButton 是一个已经绑好的按键：点它会把 Payload 原样送回。
type CardButton struct {
	Label   string
	Style   string
	Payload map[string]string
}

// Render 把帧渲染成卡片：**值由调用方现算**，这里只负责把槽位填上。
//
// 槽位写法 `{{键}}`。值里没有那个键 → 那一格是「（没这个值：键）」并回一条 Note。
func (f Frame) Render(values map[string]string) (Card, []Note) {
	var notes []Note
	c := Card{}
	where := "header"
	c.Header.Title = f.fill(f.Header.Title, values, where, &notes)
	c.Header.Tone = f.fill(f.Header.Tone, values, where, &notes)

	for i, b := range f.Blocks {
		where := fmt.Sprintf("块 %d（%s）", i+1, orKind(b.Kind))
		switch strings.ToLower(strings.TrimSpace(b.Kind)) {
		case KindText:
			c.Elements = append(c.Elements, Element{Kind: KindText, Text: f.fill(b.Text, values, where, &notes)})
		case KindFields:
			el := Element{Kind: KindFields}
			for _, it := range b.Items {
				el.Fields = append(el.Fields, Item{
					Label: f.fill(it.Label, values, where, &notes),
					Value: f.fill(it.Value, values, where, &notes),
				})
			}
			if len(el.Fields) == 0 {
				notes = append(notes, Note{Where: where, What: "这一块一列字都没有 —— 帧里 items 是空的"})
			}
			c.Elements = append(c.Elements, el)
		case KindButtons:
			el := Element{Kind: KindButtons}
			for j, btn := range b.Buttons {
				bw := fmt.Sprintf("%s 按键 %d", where, j+1)
				signal := strings.TrimSpace(btn.Signal)
				if signal == "" {
					notes = append(notes, Note{Where: bw, What: "这个按键没写 signal —— 点了也不知道是要干什么，**没有渲染它**（不猜）"})
					continue
				}
				payload := map[string]string{ClickSignalKey: signal}
				if id := strings.TrimSpace(values["id"]); id != "" {
					payload[ClickIDKey] = id
				} else {
					notes = append(notes, Note{Where: bw, What: "槽位 id 是空的 —— 点了这一下回不到任何一条（回调那一侧会拒）"})
				}
				el.Buttons = append(el.Buttons, CardButton{
					Label:   f.fill(btn.Label, values, bw, &notes),
					Style:   strings.TrimSpace(btn.Style),
					Payload: payload,
				})
			}
			if len(el.Buttons) == 0 {
				notes = append(notes, Note{Where: where, What: "这一块一个按键都没渲染出来 —— 卡片点了也没用"})
			}
			c.Elements = append(c.Elements, el)
		case "":
			notes = append(notes, Note{Where: where, What: "这一块没写 kind —— 没渲染它（认不出的东西不猜）"})
		default:
			notes = append(notes, Note{Where: where,
				What: fmt.Sprintf("认不出的块 kind=%q —— 这一版只认 %s / %s / %s，没渲染它", b.Kind, KindText, KindFields, KindButtons)})
		}
	}
	return c, notes
}

// fill 把 `{{键}}` 换成值。缺的键**不静默变空**：那一格写「（没这个值：键）」并回一条 Note。
func (f Frame) fill(s string, values map[string]string, where string, notes *[]Note) string {
	if s == "" || !strings.Contains(s, "{{") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, "{{")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.Index(s[i:], "}}")
		if j < 0 {
			// 只有左括号：当普通文字，不猜后半段在哪
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		key := strings.TrimSpace(s[i+2 : i+j])
		if v, ok := values[key]; ok {
			b.WriteString(v)
		} else {
			b.WriteString("（没这个值：" + key + "）")
			*notes = append(*notes, Note{Where: where, What: "帧里要 " + key + "，值里没有这个键"})
		}
		s = s[i+j+2:]
	}
}

func orKind(k string) string {
	if strings.TrimSpace(k) == "" {
		return "没写 kind"
	}
	return k
}

// Text 是同一张卡片的**纯文本降级**：平台不支持卡片时用它（同 cc-connect 的 RenderText）。
func (c Card) Text() string {
	var b strings.Builder
	if t := strings.TrimSpace(c.Header.Title); t != "" {
		fmt.Fprintf(&b, "【%s】\n", t)
	}
	for _, el := range c.Elements {
		switch el.Kind {
		case KindText:
			if strings.TrimSpace(el.Text) != "" {
				b.WriteString(el.Text + "\n")
			}
		case KindFields:
			for _, it := range el.Fields {
				fmt.Fprintf(&b, "%s：%s\n", it.Label, it.Value)
			}
		case KindButtons:
			var labels []string
			for _, btn := range el.Buttons {
				labels = append(labels, "["+btn.Label+"]")
			}
			if len(labels) > 0 {
				b.WriteString(strings.Join(labels, " ") + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// FeishuTones 是飞书 header 认的模板名（**data 是这一串**；写错了平台会拒，所以渲染时喊一声）。
var FeishuTones = []string{"blue", "wathet", "turquoise", "green", "yellow", "orange",
	"red", "carmine", "violet", "purple", "indigo", "grey"}

// FeishuJSON 是飞书 interactive 卡片的出口（v1 格式：config / header / elements）。
//
// 按键的 value 就是 Payload —— 点了以后飞书把它原样送回来（card.action.trigger 事件里
// 的 event.action.value），**所以回程要什么，就只在这里放什么**。
func (c Card) FeishuJSON() ([]byte, error) {
	out := map[string]any{"config": map[string]any{"wide_screen_mode": true}}
	if t := strings.TrimSpace(c.Header.Title); t != "" {
		tone := strings.TrimSpace(c.Header.Tone)
		if tone == "" {
			tone = "blue" // 飞书自己也是这个默认值（帧没给颜色就跟它走）
		}
		out["header"] = map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": t},
			"template": tone,
		}
	}
	var els []map[string]any
	for _, el := range c.Elements {
		switch el.Kind {
		case KindText:
			if strings.TrimSpace(el.Text) == "" {
				continue
			}
			els = append(els, map[string]any{"tag": "markdown", "content": el.Text})
		case KindFields:
			var fs []map[string]any
			for _, it := range el.Fields {
				fs = append(fs, map[string]any{
					"is_short": true,
					"text": map[string]any{"tag": "lark_md",
						"content": "**" + it.Label + "**\n" + it.Value},
				})
			}
			if len(fs) > 0 {
				els = append(els, map[string]any{"tag": "div", "fields": fs})
			}
		case KindButtons:
			var as []map[string]any
			for _, btn := range el.Buttons {
				style := strings.TrimSpace(btn.Style)
				if style == "" {
					style = "default"
				}
				as = append(as, map[string]any{
					"tag":   "button",
					"text":  map[string]any{"tag": "plain_text", "content": btn.Label},
					"type":  style,
					"value": btn.Payload,
				})
			}
			if len(as) > 0 {
				els = append(els, map[string]any{"tag": "action", "actions": as})
			}
		}
	}
	out["elements"] = els
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// --- 回程：一次按键 ---

// Click 是一次按键回程 —— 从飞书 `card.action.trigger` 那件事里把我们要的几样挑出来。
type Click struct {
	OpenID    string            // 谁点的（本系统里唯一的「人」标识；由 org.ByOpenID 定位）
	ChatID    string            // 点在哪（群 / 单聊）
	MessageID string            // 哪条消息（留痕时能回到那一条卡片）
	Values    map[string]string // 按键带回来的键值对
}

// ParseClick 读一份 `card.action.trigger` 的事件体。
//
// 事件体是飞书的形状（外面那层 event 可以省：有的转发层只递里面那一份）——
// **两处都收，别的一概不猜**。
func ParseClick(raw []byte) (Click, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return Click{}, fmt.Errorf("事件体不是合法 JSON：%w", err)
	}
	body := top
	if inner, ok := top["event"].(map[string]any); ok {
		body = inner
	}
	c := Click{Values: map[string]string{}}
	if op, ok := body["operator"].(map[string]any); ok {
		c.OpenID = str(op["open_id"])
	}
	if c.OpenID == "" {
		// 有的形状把点的人放在 message / sender 上（转发层照抄的写法不一）
		for _, k := range []string{"operator_id", "open_id", "user_id"} {
			if v := str(body[k]); v != "" {
				c.OpenID = v
				break
			}
		}
	}
	if ctx, ok := body["context"].(map[string]any); ok {
		c.ChatID = str(ctx["open_chat_id"])
		c.MessageID = str(ctx["open_message_id"])
	}
	if act, ok := body["action"].(map[string]any); ok {
		if v, ok := act["value"].(map[string]any); ok {
			for k, val := range v {
				c.Values[k] = str(val)
			}
		}
	}
	if len(c.Values) == 0 {
		return Click{}, fmt.Errorf("这份事件里没有 action.value —— 不知道点的是哪个按键")
	}
	return c, nil
}

// Point 是这次回程要对哪一条、点的是什么。
//
// **两个键都在才算数** —— 缺一个就不猜（判据永远只看这两个键，同 approvals 的「只认三个字」）。
func (c Click) Point() (id, signal string, ok bool) {
	id = strings.TrimSpace(c.Values[ClickIDKey])
	signal = strings.TrimSpace(c.Values[ClickSignalKey])
	if id == "" || signal == "" {
		return "", "", false
	}
	return id, signal, true
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		// JSON 里的数字：id 这类东西被平台解析成数字也照收（不因此丢掉一次回程）
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%v", x), "0"), ".")
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", x))
	}
}

// FindFrame 找帧。
//
// 顺序：--frame 指的 > 本机 <exe 目录>/templates/cards/<名>.json > ./templates/cards/<名>.json > 出厂帧。
// 返回第三个值「这份帧住在哪」：**看得见用的是哪一份** ——
// 「我改了帧怎么没生效」最难查，就因为没人说用的是哪一份。
func FindFrame(name, explicit string) (Frame, string, error) {
	if p := strings.TrimSpace(explicit); p != "" {
		raw, err := os.ReadFile(p)
		if err != nil {
			return Frame{}, "", fmt.Errorf("--frame 指的这份读不到：%w", err)
		}
		f, err := ParseFrame(raw)
		if err != nil {
			return Frame{}, "", fmt.Errorf("--frame 指的这份：%w", err)
		}
		return f, p, nil
	}
	if strings.TrimSpace(name) == "" {
		name = "approval"
	}
	rel := filepath.Join("templates", "cards", name+".json")
	var cands []string
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), rel))
	}
	cands = append(cands, rel)
	for _, c := range cands {
		abs, _ := filepath.Abs(c)
		raw, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		f, err := ParseFrame(raw)
		if err != nil {
			return Frame{}, "", fmt.Errorf("帧 %s：%w", abs, err)
		}
		return f, abs, nil
	}
	return Default(), "出厂帧（编译进去的那一份）", nil
}

// ToneOK 判一个颜色名飞书认不认（不认的话发出去会被平台拒 —— 渲染时喊一声，不替换成别的）。
func ToneOK(tone string) bool {
	t := strings.ToLower(strings.TrimSpace(tone))
	if t == "" {
		return true
	}
	for _, x := range FeishuTones {
		if x == t {
			return true
		}
	}
	return false
}

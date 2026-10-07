// Package envelope —— 接入面里**与 harness 无关的那一层**：一个信封。
//
// 一句话：不管对面是 Claude Code、Codex、DSH、Hermes、OpenClaw，还是一个插件、
// 一条 MCP，进了 ANC 就只剩一种东西 —— 信封。网关可替换（#44），信封不变。
//
// 三条口径（都刻意留了「以后不用重构」的余地）：
//
//  1. **身份不靠自报。** `who` 与 `on_behalf_of` 是**声明**，必须能在真相源
//     （members / roles / domains）里解出来；解不出来不是「格式错」，是**这个人不存在**。
//     所以「读不读得懂」在 Parse、「指向真不真」在 Bind，两类事分开报。
//
//  2. **词表不锁死。** `kind` 只认下面那五个词去做归类，**认不出的原样保留**（落一条 warn），
//     不报错 —— 以后加一个 `escalate` 之类只改数据，不改这里。`needs` 是自由文本，
//     一个词都不认：要不要人拍由信封自己说，我们不从字符串里猜意图（猜错比不猜更糟）。
//
//  3. **只读，不设门禁。** 这个包只解析、只绑定、只出发现；谁能不能发、发了算不算数，
//     归授权层（#32–#35）。门禁是数据（company.md 的 policy 段），见 org.DefaultRules。
package envelope

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Kind 词表 —— 这**不是**白名单，只是「这几个词归成哪一类」。
const (
	KindAsk      = "ask"      // 请示：要一个决定
	KindReport   = "report"   // 汇报：报一个进展
	KindNotify   = "notify"   // 通知：只告知，不等回复
	KindIngest   = "ingest"   // 投喂：送材料进来
	KindProposal = "proposal" // 提案：agent 只能提，不能直接行使（#34）
)

// knownKinds 是归类词表。**不在表里不算错**（见包注释 2）。
var knownKinds = map[string]bool{
	KindAsk: true, KindReport: true, KindNotify: true,
	KindIngest: true, KindProposal: true,
}

// KnownKind 判 kind 认不认识。空串算「没写」—— 调用方自己区分 missing 与 unknown。
func KnownKind(k string) bool {
	return knownKinds[strings.ToLower(strings.TrimSpace(k))]
}

// Scope 是「哪块业务 / 哪个项目」。两个都是**指针**：指向真相源里的 slug，不是自由文本。
type Scope struct {
	Domain  string `json:"domain,omitempty"`
	Project string `json:"project,omitempty"`
}

// Envelope 是接入面的唯一形状（#44）。
//
// 只有 ID / TS / Who 是必需的 —— 它们是「这份信封是谁发的、什么时候、发件人是谁」，
// 缺一个信封就不成立（Parse 拒收）。其余留空不拦：现场先发一句，比逼人填全字段然后干脆不发要好。
type Envelope struct {
	ID         string   `json:"id"`                     // 这一份的唯一 id
	TS         string   `json:"ts"`                     // RFC3339
	Who        string   `json:"who"`                    // 发件人（bot 名）；必须能在真相源里解出来
	OnBehalfOf string   `json:"on_behalf_of,omitempty"` // 代谁：member:名字 | role:岗位 | domain:slug，或裸名字
	Scope      Scope    `json:"scope"`                  // 哪块业务 / 哪个项目
	Kind       string   `json:"kind,omitempty"`         // ask / report / notify / ingest / proposal（认不出的照收）
	Body       string   `json:"body,omitempty"`         // 正文
	Refs       []string `json:"refs,omitempty"`         // 证据在哪
	Needs      []string `json:"needs,omitempty"`        // 要不要人拍 —— 自由文本，一个词都不认
}

// Parse 读一份信封。**只判「读不读得懂」，不判指不指向真东西**（那是 Bind 的事）。
func Parse(b []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return Envelope{}, fmt.Errorf("不是合法的 JSON 信封：%v", err)
	}
	if err := e.structural(); err != nil {
		return Envelope{}, err
	}
	return e, nil
}

// structural 是「读成信封」的最低要求。三条，各自对应一个必答项。
func (e Envelope) structural() error {
	if strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("缺 id —— 信封没有身份，收到两份也认不出是不是同一份")
	}
	if _, ok := e.At(); !ok {
		return fmt.Errorf("ts 必须是 RFC3339（例如 2026-10-08T02:41:00+08:00），现在是 %q", e.TS)
	}
	if strings.TrimSpace(e.Who) == "" {
		return fmt.Errorf("缺 who —— 判据要求「只靠信封就能答出谁」，这一项缺了信封就不成立")
	}
	return nil
}

// At 解析 ts。解析不了返回零值 + false（调用方据此判「读不懂」）。
func (e Envelope) At() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(e.TS))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Bytes 序列化（人读的顺序：先身份、再来源、再内容、最后引用与诉求）。
func (e Envelope) Bytes() ([]byte, error) {
	return json.MarshalIndent(e, "", "  ")
}

// Line 是「六问」里的一问：标签 + 答案。
type Line struct {
	Q string `json:"q"`
	A string `json:"a"`
}

// Brief 把信封摊成六问六答 —— 判据是 #44 写死的：
// **只靠信封就能答出下面六件事**。答不出来就是信封缺字段，不是看的人没本事。
//
// 最后两问刻意**不做判断**：`证据在哪`只列 refs 原文，`要不要人拍`只列 needs 原文。
// 信封自己说要不要人拍，我们不从字符串里猜意图。
func (e Envelope) Brief() []Line {
	return []Line{
		{"谁", blank(e.Who, "（缺）")},
		{"代谁", blank(e.OnBehalfOf, "（空）")},
		{"哪块业务", scopeText(e.Scope)},
		{"要什么", blank(e.Kind, "（没写）")},
		{"证据在哪", listText(e.Refs, "（没带）")},
		{"要不要人拍", listText(e.Needs, "（没提）")},
	}
}

func blank(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func scopeText(s Scope) string {
	d := strings.TrimSpace(s.Domain)
	p := strings.TrimSpace(s.Project)
	switch {
	case d == "" && p == "":
		return "（没写）"
	case p == "":
		return d
	case d == "":
		return p
	default:
		return d + " / " + p
	}
}

func listText(list []string, fallback string) string {
	if len(list) == 0 {
		return fallback
	}
	return strings.Join(list, "、")
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"anc/internal/audit"
	"anc/internal/envelope"
	"anc/internal/org"
	renderpkg "anc/internal/render"
)

// ---------- 出站读出口（W2）：桥的另一半 ----------
//
// 入站是 agent **递**给 ANC（anc_send_envelope）；出站是 agent 向 ANC **要**上下文
// （anc_read_context）—— **只读**：一个字都不写回真相源。
//
// 三件事全部复用既有件，一件都不新造（各造一套迟早会对不上号）：
//
//  1 **可见范围**：renderpkg.VisibleDomains —— 自己的域全行 / 别人的域只给「找谁」。
//     persona 段 8 用的是同一把尺子。
//  2 **身份**：与信封同一条口径（envelope.Resolves）—— 解不出**不是格式错，是这个人不存在**。
//  3 **留痕**：audit.Append 一条（谁 / 要什么 / 给没给 / 为什么拒）。审计包自己写着
//     「出站动作由人 / agent 显式补记」—— 这里就是那条出站动作的自动落点。
//
// 回话是 **JSON**，字段就是契约，多一个都不给。这不只是好看：它同时是
// 「出站不泄漏本机路径与自由文本」的**结构性**保证 —— 只回契约字段，指针一律 vault 相对。

// readContextOut 是 anc_read_context 回给 harness 的那一份。
type readContextOut struct {
	Who        string          `json:"who,omitempty"`          // 解出来的身份（成员名）
	OnBehalfOf string          `json:"on_behalf_of,omitempty"` // 署名（原样回，便于人核对）
	Scope      string          `json:"scope,omitempty"`        // 实际答的域；空 = 你负责的全部
	Domains    []readDomainOut `json:"domains,omitempty"`      // 你负责的域：全行（拒的时候不出现）
	Directory  []readBriefOut  `json:"directory,omitempty"`    // 别域目录：只有名称 / 是什么 / 找谁
	Note       string          `json:"note,omitempty"`         // 只在「还没给你划域」时出现
	Refused    *readRefusalOut `json:"refused,omitempty"`      // 拒：不给数据，给理由（能指路就指路）
	At         string          `json:"at"`                     // 回答时刻（RFC3339）
}

// readDomainOut 是「自己的域」那一行的全貌：域是什么 / 数据在哪 / 找谁 / 证据指针。
type readDomainOut struct {
	Slug     string        `json:"slug"`
	Name     string        `json:"name"`
	What     string        `json:"what"`
	Data     string        `json:"data,omitempty"`
	Who      string        `json:"who"`
	Terms    string        `json:"terms,omitempty"`
	Sources  string        `json:"sources,omitempty"`
	Evidence *readEvidence `json:"evidence"`
}

// readBriefOut 是别域的可见面：名称 / 是什么 / 找谁。**没有 data、没有 terms** ——
// 要跨域就按「找谁」接头，走人，不走近道（SPEC §6「数据默认不通」）。
type readBriefOut struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	What string `json:"what"`
	Who  string `json:"who"`
}

// readEvidence 是指回真相源的指针：**vault 相对**路径 + 行号 —— 绝不出现本机绝对路径。
type readEvidence struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

type readRefusalOut struct {
	Reason string `json:"reason"`
	Ask    string `json:"ask,omitempty"` // 该找谁（能指路就指路）
}

// readContext 是工具实现：解身份 → 按可见范围组一份 JSON → 落一条痕。
//
// isErr=true 的三种情形都是「没办成」而不是「工具坏了」：这个人不存在 / on_behalf_of 解不出 /
// 要的是别人的域。三种都留痕 —— 被拒的那次同样是「一次想行使」。
func (g *envelopeIngress) readContext(args map[string]any) (string, bool) {
	now := g.now()
	who := strArg(args, "who")
	onBehalf := strArg(args, "on_behalf_of")
	ask := safeToken(strArg(args, "domain"))

	o, err := org.Load(g.Vault)
	if err != nil {
		// 真相源本身是红的：读不了就不编一份绿灯（同 envelope check 的口径）。
		return "真相源加载失败，读不了：" + err.Error() + "\n（先修 org：anc org check <vault>）", true
	}

	// 身份：解不出（含压根没给）→ **当这个人不存在**。所以理由里不许出现「参数错误」
	// 这类话 —— 那会把读的人引去改参数，而问题是他不在这家公司（envelope 包注释第 1 条）。
	m, ok := o.Member(who)
	if !ok {
		reason := "我们公司没有这个人 —— 身份不许自报：who 必须能在 members/ 里解出来"
		g.auditRead(readActor(who), ask, audit.ResultDenied, reason, now)
		return mustJSON(readContextOut{
			Refused: &readRefusalOut{Reason: reason},
			At:      now.Format(time.RFC3339),
		}), true
	}

	// 署名（可选）：给了就必须解得出来 —— 同一套身份解析（envelope.Resolves），不另开一套。
	if onBehalf != "" && !envelope.Resolves(o, onBehalf) {
		reason := "on_behalf_of 解不出 —— 写 member:名字 | role:岗位 | domain:slug，或直接写名字"
		g.auditRead(m.Name, ask, audit.ResultDenied, reason, now)
		return mustJSON(readContextOut{
			Who: m.Name, OnBehalfOf: onBehalf,
			Refused: &readRefusalOut{Reason: reason},
			At:      now.Format(time.RFC3339),
		}), true
	}

	mine, others := renderpkg.VisibleDomains(o, m)

	// 不写 domain = 你负责的全部域（外加「找谁」目录）；写了 = 只要那一行。
	var chosen []renderpkg.DomainRow
	if ask == "" {
		chosen = mine
	} else {
		for _, d := range mine {
			if d.Slug == ask {
				chosen = []renderpkg.DomainRow{d}
			}
		}
		if len(chosen) == 0 {
			reason, helper := refuseDomain(o, ask)
			g.auditRead(m.Name, ask, audit.ResultDenied, reason, now)
			return mustJSON(readContextOut{
				Who: m.Name, OnBehalfOf: onBehalf,
				Refused: &readRefusalOut{Reason: reason, Ask: helper},
				At:      now.Format(time.RFC3339),
			}), true
		}
	}

	out := readContextOut{Who: m.Name, OnBehalfOf: onBehalf, At: now.Format(time.RFC3339)}
	if ask != "" {
		out.Scope = ask
	}
	out.Domains = domainRows(chosen)
	if ask == "" {
		out.Directory = briefRows(others)
		if len(chosen) == 0 {
			out.Note = "还没给你划域（在 members/" + m.Name + "/persona.md 的 domains 里写）"
		}
	}

	// 给了也要留痕 —— 审计不是只记拒绝。
	g.auditRead(m.Name, ask, audit.ResultOK, "", now)
	return mustJSON(out), false
}

// refuseDomain 组「这块业务不在你的可见范围」这句拒话。两种情况分开：
// 域表里有这个域 → 指路「找谁」（这才是让人不必再问人的那一步）；压根没有 → 明说没有，别编一个谁。
func refuseDomain(o *org.Org, ask string) (reason, helper string) {
	for _, d := range o.Domains {
		if d.Slug != ask {
			continue
		}
		who := o.WhoLabel(d.Who)
		if who == "—" {
			who = ""
		}
		return renderpkg.RefuseDomainText(ask, who), who
	}
	return "domains.md 里没有「" + ask + "」这块业务 —— 要么拼错了，要么这一域还没抽成表行", ""
}

func domainRows(rows []renderpkg.DomainRow) []readDomainOut {
	out := make([]readDomainOut, 0, len(rows))
	for _, d := range rows {
		out = append(out, readDomainOut{
			Slug: d.Slug, Name: d.Name, What: d.What, Data: d.Data, Who: d.Who,
			Terms: d.Terms, Sources: d.Sources,
			Evidence: &readEvidence{File: org.DomainsFile, Line: d.Line},
		})
	}
	return out
}

func briefRows(rows []renderpkg.DomainBrief) []readBriefOut {
	if len(rows) == 0 {
		return nil
	}
	out := make([]readBriefOut, 0, len(rows))
	for _, d := range rows {
		out = append(out, readBriefOut{Slug: d.Slug, Name: d.Name, What: d.What, Who: d.Who})
	}
	return out
}

// auditRead 给一次出站调用留一条痕：谁 / 要什么 / 给没给 / 为什么拒。
//
// 留痕失败**不改回话**：读出口的职责是答问题，不是把审计故障变成对 harness 的报错。
// 但要在 stderr 吼一声 —— 静默丢一条痕，等于审计少一次行使。
func (g *envelopeIngress) auditRead(actor, domain, result, why string, at time.Time) {
	if actor == "" {
		actor = "unknown"
	}
	rec := audit.Record{
		ID:     fmt.Sprintf("read-%d-%s", at.UnixNano(), audit.ShardName(actor)),
		At:     at.Format(time.RFC3339),
		Actor:  actor,
		Action: audit.ActionRead,
		Object: domain,
		Result: result,
		Why:    why,
		Tool:   "anc_read_context",
		Source: "serve",
	}
	if _, err := audit.Append(g.Vault, rec); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  出站调用没留上痕（audit）：%v\n", err)
	}
}

// readActor 决定「解不出的那个 who」往谁的账上记：用请求里报上来的名字（清洗后），空则 unknown。
// 一次都不许静默不记 —— 「没有身份」不是「没有发生」。
func readActor(claimed string) string {
	if s := safeToken(claimed); s != "" {
		return s
	}
	return "unknown"
}

// safeToken 把请求里的自由文本压成一条能落盘的短串：去控制字符、压成单行、封顶 64 字。
// 落进痕里的是它 —— 请求原文不落盘（审计流水不是给人塞私货的地方，更不是给注入留的一条通道）。
func safeToken(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 64 {
		s = string(r[:64]) + "…"
	}
	return s
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// 结构是我们自己定的，序列化不了只可能是编程错误 —— 别把 panic 扔给 harness。
		return `{"refused":{"reason":"内部错误：回话序列化失败"}}`
	}
	return string(b)
}

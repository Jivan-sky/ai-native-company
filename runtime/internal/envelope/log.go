package envelope

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/audit"
	"anc/internal/org"
)

// DirName 是运行态里放信封日志的那一层。
const DirName = "envelope"

// Finding 是一条绑定发现的**落盘形状**（规则 id / 档位 / 文案）。
// 与 org.Issue 分开：那个是内存里的形状（带 Where、字段没 json tag），这个是给日志用的稳定形状。
type Finding struct {
	Rule  string `json:"rule"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

// ToFindings 把绑定发现折成落盘形状。
func ToFindings(in []org.Issue) []Finding {
	out := make([]Finding, 0, len(in))
	for _, i := range in {
		out = append(out, Finding{Rule: i.Rule, Level: string(i.Level), Msg: i.Msg})
	}
	return out
}

// Record 是落到日志里的一行：**原信封**（一字不改）+ 服务端收到的时刻 + 绑定结果。
//
// 原信封一字不改地留着，是因为「收到的是什么」和「我们怎么看它」是两件事 ——
// 前者不能因为我们后来改了规则而变形。
type Record struct {
	Envelope Envelope  `json:"envelope"`
	At       string    `json:"at"`
	Findings []Finding `json:"findings,omitempty"`
}

// scrub 把一份记录里**可能夹带原话**的那几格过一遍脱敏 —— 与审计吃同一张表（议题 #64）。
//
// 只碰这几格：
//   - `envelope.body` 正文（原话写在这里）、`envelope.refs` 证据链接（查询串里可以带凭据）、
//     `envelope.needs` 自由文本；
//   - `findings[].msg` 是**判据文案**，而判据文案会把字段值原样引一遍
//     （例如 `domain="nope" 不在 domains.md 里`）—— 所以它也算「可能夹带内容」。
//   - `id` / `ts` / `who` / `on_behalf_of` / `scope` / `kind` 是标识与词表，抹了就看不出
//     「谁发的、发的是哪一类」。
func scrub(rec *Record) []string {
	var hits []string
	for _, f := range []*string{&rec.Envelope.Body} {
		s, h := audit.Scrub(*f)
		*f = s
		hits = append(hits, h...)
	}
	for i, r := range rec.Envelope.Refs {
		s, h := audit.Scrub(r)
		rec.Envelope.Refs[i] = s
		hits = append(hits, h...)
	}
	for i, n := range rec.Envelope.Needs {
		s, h := audit.Scrub(n)
		rec.Envelope.Needs[i] = s
		hits = append(hits, h...)
	}
	for i := range rec.Findings {
		s, h := audit.Scrub(rec.Findings[i].Msg)
		rec.Findings[i].Msg = s
		hits = append(hits, h...)
	}
	return hits
}

// Append 把「收到的一封信」落到运行态日志：`<data>/envelope/<YYYY-MM>.<who>.jsonl`。
//
// 为什么在 **data 目录**、而不是 vault（git）：
//   - 这是**流量**，不是真相。真相（谁被授权做什么）在 git 里；每一封信都进 git，
//     就是每天刷 diff —— 同一个理由让 §13 Q15 的「进度」不进表。
//   - 而且 vault 顶层新目录默认会被 org 当成「数据目录」扫进路由表（`scanRouting`；
//     除非像 timeline / grants 那样进 skipDirs）——
//     一封信都不该改变谁的数据来源。
//
// 为什么按 who 分片：两个 agent 同时发，就碰不到同一个文件（同 timeline 的做法，
// 并发现场就地消掉，不靠锁）。
func Append(dataDir string, rec Record) (string, error) {
	if err := rec.Envelope.structural(); err != nil {
		return "", err
	}
	if _, ok := rec.Envelope.At(); !ok {
		return "", fmt.Errorf("信封的 ts 必须是 RFC3339")
	}
	if strings.TrimSpace(rec.At) == "" {
		return "", fmt.Errorf("at（服务端收到的时刻）不能为空")
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(rec.At)); err != nil {
		return "", fmt.Errorf("at 必须是 RFC3339：%v", err)
	}
	author := safeName(rec.Envelope.Who)
	if author == "" {
		return "", fmt.Errorf("who=%q 压不出能当文件名的样子（只留字母数字与 ._-）", rec.Envelope.Who)
	}
	t, _ := rec.Envelope.At()
	dir := filepath.Join(dataDir, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, t.Format("2006-01")+"."+author+".jsonl")
	// 落盘前过一遍脱敏（同 audit.Append 的位置与理由）：写口是承诺的兑现处。
	scrub(&rec)
	b, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return "", err
	}
	return path, nil
}

// safeName 把名字变成能当文件名的样子：只留字母数字与 ._-
// （中文名会被压成空 → 调用方报错，而不是写出一个怪文件名）。
func safeName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "._-")
}

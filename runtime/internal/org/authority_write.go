package org

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 权威源与授权的**写口**。读口在 authority.go，写口在这里。
//
// 三条纪律，与 A1/A2 修出来的那套一致：
//  1. **先写临时文件，再原子换名**：半份 authority.md 比没有更糟 —— 读的人会看到一个
//     版本号配一份不完整的撤权表。
//  2. **同名不覆盖**：一条授权是一个文件；要改就换 slug，历史才留得住。
//  3. **版本只增不减**：撤权靠"版本推进到哪一版"生效，版本回退等于把撤掉的权又还回去。

// ErrGrantExists：同名授权已经在了。不覆盖 —— 覆盖等于悄悄改掉一条已经有审计记录的授权。
var ErrGrantExists = errors.New("这条授权已经存在")

// ErrNoAuthority：还没有权威源。签不了 —— 没有受托签发人的世界不该有根授权。
var ErrNoAuthority = errors.New("还没有权威源")

// InitAuthority 建一份权威源（v1）。已经有就报错，不覆盖。
func InitAuthority(vault, issuer, note string) (Authority, error) {
	if strings.TrimSpace(issuer) == "" {
		return Authority{}, errors.New("受托签发人不能为空（写 member:<名>）")
	}
	p := filepath.Join(vault, AuthorityFile)
	if _, err := os.Stat(p); err == nil {
		return Authority{}, fmt.Errorf("%s 已经存在 —— 改签发人请走 authority.revoke + 重签，不要原地改", AuthorityFile)
	}
	body := "---\nauthority:\n  version: 1\n  issuer: " + strings.TrimSpace(issuer) + "\n"
	if s := strings.TrimSpace(note); s != "" {
		body += "  note: " + s + "\n"
	}
	body += "---\n\n# 权威源（模拟件）\n\n" +
		"受托签发人：`" + strings.TrimSpace(issuer) + "`。第一笔根授权只能由它签发。\n" +
		"撤权从 `version` 推进到的那一版起生效。规则见 `runtime/docs/C1C2-AUTHORITY-SIM.md`。\n"
	if err := writeAtomic(p, body); err != nil {
		return Authority{}, err
	}
	a, _, err := LoadAuthority(vault)
	return a, err
}

// AppendGrant 写一条授权。已存在同名 → ErrGrantExists。
//
// `IssuedBy` 必须写：签不出是谁签的授权，判定时会如实说「无法证明」而不是放行。
func AppendGrant(vault string, g Grant) (string, error) {
	if _, ok, err := LoadAuthority(vault); err != nil {
		return "", err
	} else if !ok {
		return "", ErrNoAuthority
	}
	slug := strings.TrimSpace(g.Slug)
	if slug == "" {
		return "", errors.New("授权要有一个 ascii slug（当文件名用）")
	}
	if !reID.MatchString(slug) {
		return "", fmt.Errorf("slug %q 不是小写 ASCII 标识 —— 它要进路径", slug)
	}
	if strings.TrimSpace(g.IssuedBy) == "" {
		return "", errors.New("缺 issued_by —— 签不出谁签的授权，判定时会判「无法证明」")
	}
	dir := filepath.Join(vault, GrantsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, slug+".md")
	if _, err := os.Stat(p); err == nil {
		return "", fmt.Errorf("%w：%s", ErrGrantExists, slug)
	}
	var b strings.Builder
	b.WriteString("---\ngrant:\n")
	field := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			b.WriteString("  " + k + ": " + strings.TrimSpace(v) + "\n")
		}
	}
	field("from", g.From)
	field("to", g.To)
	field("action", g.Action)
	field("object", g.Object)
	field("issued_by", g.IssuedBy)
	field("issued_at", firstNonEmptyOrg(g.IssuedAt, time.Now().Format(time.RFC3339)))
	field("expires_at", g.ExpiresAt)
	field("parent", g.Parent)
	field("ttl", g.TTL)
	field("on_behalf_of", g.OnBehalfOf)
	field("reason", g.Reason)
	b.WriteString("---\n")
	if s := strings.TrimSpace(g.Body); s != "" {
		b.WriteString("\n" + s + "\n")
	}
	return p, writeAtomic(p, b.String())
}

// Revoke 追加一条撤权记录并把权威版本推进一版 —— **撤权从这一版起生效**（C2 的生效点）。
//
// 为什么是"追加 + 版本 +1"而不是删文件：删掉就看不见「曾经授过、后来撤了」。
// 审计要能回答「那一刻他有没有权」，答案必须留在历史里。
func Revoke(vault, ref, by string) (Authority, error) {
	auth, ok, err := LoadAuthority(vault)
	if err != nil {
		return Authority{}, err
	}
	if !ok {
		return Authority{}, ErrNoAuthority
	}
	if !sameRef(by, auth.Issuer) {
		return Authority{}, fmt.Errorf("撤权只能由受托签发人执行（你写的是 %s，权威源是 %s）", by, auth.Issuer)
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Authority{}, errors.New("要撤哪一条？给 slug 或 grants/<slug>.md")
	}
	// 撤的必须是真存在的一条
	if _, found := findGrantByRef(vault, ref); !found {
		return Authority{}, fmt.Errorf("找不到授权 %q —— 撤一条不存在的东西只会让版本号白白推进", ref)
	}
	next := auth.Version + 1
	var refs []string
	for _, r := range auth.Revoked {
		refs = append(refs, fmt.Sprintf("%s@%d", r.Ref, r.At))
	}
	refs = append(refs, fmt.Sprintf("%s@%d", ref, next))
	sort.Strings(refs)

	body := "---\nauthority:\n  version: " + fmt.Sprint(next) + "\n  issuer: " + auth.Issuer + "\n"
	if auth.Note != "" {
		body += "  note: " + auth.Note + "\n"
	}
	body += "  revoked: " + strings.Join(refs, ", ") + "\n---\n\n# 权威源（模拟件）\n\n" +
		"撤权从 `version` 推进到的那一版起生效。最后一次撤权：`" + ref + "` @ v" + fmt.Sprint(next) + "。\n"
	if err := writeAtomic(filepath.Join(vault, AuthorityFile), body); err != nil {
		return Authority{}, err
	}
	a, _, err := LoadAuthority(vault)
	return a, err
}

// findGrantByRef 在磁盘上找一条授权（不依赖 org 加载，撤权只要文件在就算数）。
func findGrantByRef(vault, ref string) (string, bool) {
	base := filepath.Join(vault, GrantsDir)
	name := strings.TrimSuffix(filepath.Base(ref), ".md")
	p := filepath.Join(base, name+".md")
	if _, err := os.Stat(p); err == nil {
		return p, true
	}
	return "", false
}

// writeAtomic 写临时文件再换名：读的人要么看到旧的、要么看到新的，不会看到半份。
func writeAtomic(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

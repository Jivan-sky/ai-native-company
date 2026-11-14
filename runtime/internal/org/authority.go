package org

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 权威源：**谁有权发第一个权限、撤权从哪一版起算**。
//
// 这一层存在的理由只有一个：SPEC 不允许「自称 owner 就行」。没有权威源，
// 一条 grant 只能证明「有个文件这么写了」，不能证明「有权的人这么决定的」。
//
// 形态：`<vault>/authority.md` 的 frontmatter。选它的原因是它跟 grants / domains
// 一样是**数据**，而且就在 SPEC 所在的那份真相源里 —— 客户能读、能改、能进 Git 历史。
//
// 字段：
//
//	version:   单调递增的权威版本号。撤权从这一版起算（C2 的生效点，见 docs/C1C2-AUTHORITY-SIM.md）
//	issuer:    受托签发人（member:<名>）。第一笔根授权只能由它签发
//	revoked:   撤权记录，逗号分隔的 `<grant 的 slug 或路径>@<版本>`
//	note:      一句话：这份委任从哪来（客户书面确认 / 会议纪要 …）
type Authority struct {
	Version int
	Issuer  string
	Revoked []Revocation
	Note    string
}

// Revocation 是一条撤权记录：从 `At` 这一版起，`Ref` 那一条授权失效。
type Revocation struct {
	Ref string
	At  int
}

// AuthorityFile 是权威源在 vault 里的位置。
const AuthorityFile = "authority.md"

// LoadAuthority 读权威源。第三个返回值表示「文件在不在」：
// 不在 = 权威源没接上 → 判定一律 **无法判定**（不是允许，也不是拒绝）。
func LoadAuthority(vault string) (Authority, bool, error) {
	p := filepath.Join(vault, AuthorityFile)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Authority{}, false, nil
		}
		return Authority{}, false, err
	}
	doc, perr := Parse(string(b))
	if perr != nil {
		return Authority{}, true, fmt.Errorf("authority.md 读不出：%w", perr)
	}
	get := func(k string) string {
		v, err := doc.Str("authority." + k)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(v)
	}
	// version 在 YAML 里是数字不是字符串：`Str` 会拒（类型不符），所以单独读原始值。
	a := Authority{
		Version: toInt(doc.Meta["authority.version"]),
		Issuer:  get("issuer"),
		Note:    get("note"),
	}
	for _, raw := range strings.Split(get("revoked"), ",") {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		ref, ver := s, 0
		if i := strings.LastIndexByte(s, '@'); i > 0 {
			ref, ver = strings.TrimSpace(s[:i]), atoiOr(s[i+1:], 0)
		}
		a.Revoked = append(a.Revoked, Revocation{Ref: ref, At: ver})
	}
	return a, true, nil
}

// RevokedAt 判一条授权在 `version` 这一版是否已被撤回。按 slug 与相对路径两种写法都认。
func (a Authority) RevokedAt(g Grant, version int) (Revocation, bool) {
	for _, r := range a.Revoked {
		if r.Ref != g.Slug && r.Ref != g.Path {
			continue
		}
		// `@0` 表示「不写版本」：从记录它的那一版起算，取权威源当前版本。
		at := r.At
		if at <= 0 {
			at = a.Version
		}
		if version >= at {
			return r, true
		}
	}
	return Revocation{}, false
}

// Verdict 是一次「这条授权现在还能不能用」的完整回答。三态，不是两态：
//
//	OK=true, Unknown=false  → 可以行使
//	OK=false, Unknown=false → 明确不行（附原因）
//	Unknown=true           → **无法判定**：缺权威源 / 数据读不全 / 链断了但说不清
//
// 第三态是这一层的重点：把「不知道」显示成「允许」，比拒绝危险得多。
type Verdict struct {
	OK      bool
	Unknown bool
	Reason  string
	// Chain 是从叶到根的授权链（slug 列表），给审计用：出了事要能顺着链回去。
	Chain []string
	// Version 是这次判定用的权威版本（0 = 没有权威源）。
	Version int
}

func denyVerdict(reason string, chain []string, v int) Verdict {
	return Verdict{OK: false, Reason: reason, Chain: chain, Version: v}
}

func unknownVerdict(reason string, chain []string, v int) Verdict {
	return Verdict{OK: false, Unknown: true, Reason: reason, Chain: chain, Version: v}
}

// Exercise 判「这条 grant 在 at 时刻能不能用来行使」。
//
// 四件事一起做，缺一条就不算数：
//  1. 没被撤回（按权威版本）
//  2. 在有效期内（左闭右开：到期那一刻已经失效）
//  3. 签发者在签发那一刻**确实有权**（根签发人 = 权威源的 issuer；转授 = 父授权）
//  4. 子项不得超出父项（动作相同、客体相同、到期不晚于父项）
func (o *Org) Exercise(g Grant, auth Authority, authWired bool, at time.Time) Verdict {
	if !authWired {
		return unknownVerdict("权威源没接上（<vault>/"+AuthorityFile+" 不存在）—— 判不出这条授权是否还有效", nil, 0)
	}
	// 版本 0 = 没有版本号：撤权与生效点都无从谈起，不给绿灯。
	if auth.Version <= 0 {
		return unknownVerdict("权威源没有 version —— 撤权从哪一版起算说不清", nil, 0)
	}
	return o.exercise(g, auth, at, 0, nil)
}

func (o *Org) exercise(g Grant, auth Authority, at time.Time, depth int, chain []string) Verdict {
	chain = append(chain, firstNonEmptyOrg(g.Slug, g.Path))
	if depth > 8 {
		return unknownVerdict("授权链太深（>8 层）—— 可能是环或数据有问题", chain, auth.Version)
	}

	// 1) 撤权
	if r, hit := auth.RevokedAt(g, auth.Version); hit {
		return denyVerdict(fmt.Sprintf("授权 %s 已在 v%d 被撤回", r.Ref, r.At), chain, auth.Version)
	}

	// 2) 有效期（左闭右开）
	if g.IssuedAt != "" {
		if t, ok := parseOrgTime(g.IssuedAt); ok && at.Before(t) {
			return denyVerdict("授权还没生效（issued_at="+g.IssuedAt+"）", chain, auth.Version)
		}
	}
	if g.ExpiresAt != "" {
		t, ok := parseOrgTime(g.ExpiresAt)
		if !ok {
			return unknownVerdict("expires_at 读不懂："+g.ExpiresAt, chain, auth.Version)
		}
		if !at.Before(t) {
			return denyVerdict("授权已到期（expires_at="+g.ExpiresAt+"）", chain, auth.Version)
		}
	}

	// 3) 签发者：根看权威源，转授看父
	by := strings.TrimSpace(g.IssuedBy)
	if by == "" {
		return unknownVerdict("这条授权没写 issued_by —— 无法证明是有权的人签的", chain, auth.Version)
	}
	if strings.TrimSpace(g.Parent) == "" {
		if !sameRef(by, auth.Issuer) {
			return denyVerdict("根授权只能由受托签发人签发（这条写的是 "+by+"，权威源是 "+
				firstNonEmptyOrg(auth.Issuer, "（空）")+"）", chain, auth.Version)
		}
	} else {
		parent, ok := o.grantByRef(g.Parent)
		if !ok {
			return unknownVerdict("找不到父授权 "+g.Parent+" —— 链断了，判不出这条还成不成立", chain, auth.Version)
		}
		pv := o.exercise(parent, auth, at, depth+1, chain)
		if !pv.OK {
			return pv // 父不成，子也不成（原因沿用父的）
		}
		// 4) 子不得超出父
		if g.Action != "" && parent.Action != "" && !strings.EqualFold(g.Action, parent.Action) {
			return denyVerdict("转授不能换动作：父 "+parent.Action+"，子 "+g.Action, chain, auth.Version)
		}
		if !sameObjectScope(g.Object, parent.Object) {
			return denyVerdict("转授不能扩大客体：父 "+parent.Object+"，子 "+g.Object, chain, auth.Version)
		}
		if g.ExpiresAt != "" && parent.ExpiresAt != "" {
			ct, okc := parseOrgTime(g.ExpiresAt)
			pt, okp := parseOrgTime(parent.ExpiresAt)
			if okc && okp && ct.After(pt) {
				return denyVerdict("转授不能比父项活得更久：父到 "+parent.ExpiresAt+"，子到 "+g.ExpiresAt, chain, auth.Version)
			}
		} else if g.ExpiresAt != "" && parent.ExpiresAt == "" {
			// 父没有期限 = 受上游与权威约束；子自己写期限也不算错。
		}
		// 签发者必须真的持有父项：`to` 要能对上签发者
		if !refHasIdentity(parent.To, by) {
			return denyVerdict("父授权是给 "+parent.To+" 的，不是给签发人 "+by+" —— 不能拿别人的权去转授", chain, auth.Version)
		}
	}

	// 5) 签发人在判定时刻仍然在职（离职即失效）
	if name, ok := memberNameOf(by); ok {
		if m, found := o.Member(name); found && m.Disabled {
			return denyVerdict("签发人 "+by+" 已停用 —— 以其授权为来源的链一并失效", chain, auth.Version)
		}
	}
	if strings.TrimSpace(g.To) == "" {
		return unknownVerdict("授权没有 to —— 不知道是给谁的", chain, auth.Version)
	}
	return Verdict{OK: true, Chain: chain, Version: auth.Version}
}

// grantByRef 按 slug 或相对路径找一条授权。
func (o *Org) grantByRef(ref string) (Grant, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Grant{}, false
	}
	for _, g := range o.Grants {
		if g.Slug == ref || g.Path == ref || strings.TrimSuffix(g.Path, ".md") == strings.TrimSuffix(ref, ".md") {
			return g, true
		}
	}
	return Grant{}, false
}

// sameObjectScope 判「子客体是否不超过父客体」。同域即算不扩大 ——
// 更细的列级 / 行级范围这一版没有定义处，不去发明。
func sameObjectScope(child, parent string) bool {
	c, p := objectDomain(child), objectDomain(parent)
	if c == "" || p == "" {
		return false
	}
	return c == p
}

func objectDomain(object string) string {
	object = strings.TrimSpace(object)
	if object == "" {
		return ""
	}
	switch kind, name := SplitRef(object); kind {
	case "", "domain":
		return name
	}
	return ""
}

func refHasIdentity(to, ref string) bool {
	to, ref = strings.TrimSpace(to), strings.TrimSpace(ref)
	if to == "" || ref == "" {
		return false
	}
	if to == ref {
		return true
	}
	tk, tn := SplitRef(to)
	rk, rn := SplitRef(ref)
	if tn != rn {
		return false
	}
	if tk == "" || rk == "" {
		return true
	}
	return tk == rk
}

func memberNameOf(ref string) (string, bool) {
	kind, name := SplitRef(strings.TrimSpace(ref))
	if kind == "member" {
		return name, true
	}
	if kind == "" && name != "" {
		return name, true
	}
	return "", false
}

func sameRef(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == b {
		return true
	}
	ak, an := SplitRef(a)
	bk, bn := SplitRef(b)
	if an != bn {
		return false
	}
	if ak == "" || bk == "" {
		return true
	}
	return ak == bk
}

func parseOrgTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	return t, err == nil
}

func atoiOr(s string, def int) int {
	n := 0
	seen := false
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
		seen = true
	}
	if !seen {
		return def
	}
	return n
}

func firstNonEmptyOrg(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// toInt 读 YAML 里的数字（int / float / string 三种都认）—— 只给 version 这类计数字段用。
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		return atoiOr(n, 0)
	}
	return 0
}

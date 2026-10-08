package audit

import (
	"path/filepath"
	"strings"

	"anc/internal/org"
)

// Scope 是「客体 → 业务域」的映射，外加「行使者 → 他的域集合」。
//
// 它是**派生**，不落盘：域表的 data 列改了，历史流水就按新表解读
// （见 Record 的职责边界 —— 存死派生值会在域表调整之后变成假证据）。
//
// 用的是 domains.md 的 **data 列**：那一列的语义本来就是「这一域的作用域 = vault 顶层目录」，
// 与「域标签」这个授权客体同源（SPEC §6）。所以审计不另造一张表 ——
// **不自己发明一套词表**是本议题划下的边界（议题 #43 第 4 点）。
type Scope struct {
	vault     string
	companyID string
	byDir     map[string]string   // vault 顶层目录名 → 域 slug
	label     map[string]string   // 域 slug → 显示名
	actors    map[string][]string // 成员名 → 域集合
}

// NewScope 从 org 真相源建一张映射。o 为 nil 时返回一张空表（一切落「未归属」，不 panic）。
func NewScope(vault string, o *org.Org) *Scope {
	sc := &Scope{
		vault:  vault,
		byDir:  map[string]string{},
		label:  map[string]string{},
		actors: map[string][]string{},
	}
	if o == nil {
		return sc
	}
	sc.companyID = o.Company.ID
	for _, d := range o.Domains {
		dir := strings.TrimSpace(d.Data)
		if dir == "" {
			continue
		}
		// 多个域的 data 指向同一个目录时**先到的赢** —— 域表本身重复是 `anc org check` 的事，
		// 审计这里不报第二遍，但也不许两个域抢同一个目录（那会让「向谁」变得不确定）。
		if _, dup := sc.byDir[dir]; !dup {
			sc.byDir[dir] = d.Slug
			sc.label[d.Slug] = d.Name
		}
	}
	for _, m := range o.Members {
		if len(m.Domains) > 0 {
			sc.actors[m.Name] = m.Domains
		}
	}
	return sc
}

// Label 是域 slug 的显示名（没有显示名时回落到 slug 本身）。
func (sc *Scope) Label(slug string) string {
	if s := strings.TrimSpace(sc.label[slug]); s != "" {
		return s
	}
	return slug
}

// Owner 判这个客体落在哪个域。
//
// 只处理**路径形状**的客体 —— 其它形状（通道、外部系统）落「未归属」：
// 那些客体的词表还没有定义处，现在编一个只能是猜（同 grants.go 对 to / object 的留白）。
func (sc *Scope) Owner(object string) (string, bool) {
	dir := sc.topDir(object)
	if dir == "" {
		return "", false
	}
	slug, ok := sc.byDir[dir]
	return slug, ok
}

// InVault 判这个客体在不在（这个）vault 里。
// 它把「域外」和「vault 内、但不是任何域的目录」分开 —— 那是两条不同的信号（见 Zone 常量）。
func (sc *Scope) InVault(object string) bool {
	o := filepath.ToSlash(strings.TrimSpace(object))
	if o == "" {
		return false
	}
	v := filepath.ToSlash(strings.TrimSpace(sc.vault))
	if v == "" {
		return !isAbsPath(o)
	}
	if o == v || strings.HasPrefix(o, v+"/") {
		return true
	}
	// 相对路径算「在 vault 内」：vault 是唯一的参照系（归集器给的一般是绝对路径，这是兜底）。
	return !isAbsPath(o)
}

// topDir 把客体归一成「vault 下的顶层目录名」。不是 vault 内的路径 → ""。
func (sc *Scope) topDir(object string) string {
	o := filepath.ToSlash(strings.TrimSpace(object))
	if o == "" {
		return ""
	}
	rel := ""
	if v := filepath.ToSlash(strings.TrimSpace(sc.vault)); v != "" {
		switch {
		case o == v:
			return ""
		case strings.HasPrefix(o, v+"/"):
			rel = strings.TrimPrefix(o, v+"/")
		}
	}
	if rel == "" {
		if isAbsPath(o) {
			// 是绝对路径，但不在这个 vault 里 —— 那是**域外**，不是「归到某个域」。
			// （成员家目录 homes/<名> 就走这条：它不在 vault 下。）
			return ""
		}
		rel = o
	}
	rel = strings.TrimPrefix(rel, "./")
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return rel
}

// isAbsPath 认两种绝对路径：POSIX 的 `/…` 与 Windows 的 `C:\…`（已归一成 `C:/…`）。
func isAbsPath(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	return len(p) > 1 && p[1] == ':'
}

// ActorDomains 解出行使者属于哪几个域。
//
// bot 名（= 成员名）与 project 名（`<公司 id>-<成员名>`）都收 —— 归集器拿到的是后者。
func (sc *Scope) ActorDomains(actor string) []string {
	a := strings.TrimSpace(actor)
	if d, ok := sc.actors[a]; ok {
		return d
	}
	if sc.companyID != "" {
		if name := strings.TrimPrefix(a, sc.companyID+"-"); name != a {
			if d, ok := sc.actors[name]; ok {
				return d
			}
		}
	}
	return nil
}

// 目标落在哪一类。四个值：
//
//	domain  —— 落在某个业务域（domains.md 的 data 列指向的顶层目录）
//	vault   —— 在 vault 内，但不属于任何域（结构目录 / 还没划域的数据目录）
//	outside —— 在 vault 之外：越过真相源边界
//	unknown —— 抽不出目标（object 为空）
//
// 分成四类而不是「跨 / 不跨」两个值，是因为 **outside 与 vault 是两条不同的信号**：
// 前者越过了整个真相源边界（实测里就有：bot 去读了 `~/.claude/settings.json`），
// 后者只是还没划域。混成一个「未归属」，等于把这两种一起丢进黑洞。
const (
	ZoneDomain  = "domain"
	ZoneVault   = "vault"
	ZoneOutside = "outside"
	ZoneUnknown = "unknown"
)

// Derived 是一条记录**按当前域表**解读出来的东西。它不进流水（见 Record 的职责边界）。
type Derived struct {
	Actee string `json:"actee"` // 目标域 slug（Zone=domain 时有值）
	Zone  string `json:"zone"`  // domain | vault | outside | unknown
	Cross bool   `json:"cross"` // 跨域（只在 Zone=domain 且行使者有域时有意义）
	Known bool   `json:"known"` // 跨域判定了没有（false = 判不出，不是「不跨域」）
}

// Derive 把一条记录读成「向谁 + 算不算跨域」。
//
// 「跨域」按 SPEC §6 的严格含义用：**域与域之间**（目标在一个业务域里、且那不是行使者自己的域）。
// 域外与 vault 内非域目录**不叫跨域** —— 它们各自是独立的信号，由 Zone 带出去。
//
// Known 和 Cross 是两件事，页面上不许混着显示：Known=false 表示**没判**
// （目标抽不出 / 域表里没有这个目录 / 行使者没配域），这时 Cross 无意义 ——
// 要显示「判不出」，不许显示成「不跨域」。把「没判」画成「没事」是最坏的一种假绿。
func Derive(r Record, sc *Scope) Derived {
	if strings.TrimSpace(r.Object) == "" {
		return Derived{Zone: ZoneUnknown}
	}
	slug, isDomain := sc.Owner(r.Object)
	if !isDomain {
		if sc.InVault(r.Object) {
			return Derived{Zone: ZoneVault}
		}
		return Derived{Zone: ZoneOutside}
	}
	domains := sc.ActorDomains(r.Actor)
	if len(domains) == 0 {
		return Derived{Actee: slug, Zone: ZoneDomain}
	}
	cross := true
	for _, d := range domains {
		if d == slug {
			cross = false
			break
		}
	}
	return Derived{Actee: slug, Zone: ZoneDomain, Cross: cross, Known: true}
}

// EntryView 是一条记录 + 按当前域表算出来的东西。
//
// CLI 与看板共用这一份 —— 免得两处各算一次「跨域」：算法一漂，同一份流水就有了两个真相。
type EntryView struct {
	Record
	Band      string `json:"band"`       // 结果档：ok | denied | failed | unknown
	Actee     string `json:"actee"`      // 目标域 slug（"" = 判不出来）
	ActeeName string `json:"actee_name"` // 目标域显示名（"" = 同上）
	Zone      string `json:"zone"`       // domain | vault | outside | unknown
	Cross     bool   `json:"cross"`      // 跨域
	Known     bool   `json:"known"`      // 跨域判定了没有（false = 没判，不是「不跨域」）
	HasWhy    bool   `json:"has_why"`    // 有没有原话（看板据此提示「去 CLI 看」，见 board/audit.go）
}

// View 把一条记录读成视图。
func View(r Record, sc *Scope) EntryView {
	d := Derive(r, sc)
	return EntryView{
		Record:    r,
		Band:      ResultBand(r.Result),
		Actee:     d.Actee,
		ActeeName: sc.Label(d.Actee),
		Zone:      d.Zone,
		Cross:     d.Cross,
		Known:     d.Known,
		HasWhy:    strings.TrimSpace(r.Why) != "",
	}
}

// Views 把一批记录读成视图。
func Views(rs []Record, sc *Scope) []EntryView {
	out := make([]EntryView, 0, len(rs))
	for _, r := range rs {
		out = append(out, View(r, sc))
	}
	return out
}

// Scopes 是「这些行使跨出去没有」的汇总。
type Scopes struct {
	Domain  int `json:"domain"`  // 落在某个业务域
	Vault   int `json:"vault"`   // vault 内、不属于任何域
	Outside int `json:"outside"` // vault 之外
	Unknown int `json:"unknown"` // 目标抽不出
	Cross   int `json:"cross"`   // 其中跨域的
}

// TallyScopes 数每个 Zone 几条，以及其中跨域几条。
func TallyScopes(vs []EntryView) Scopes {
	var n Scopes
	for _, v := range vs {
		switch v.Zone {
		case ZoneDomain:
			n.Domain++
		case ZoneVault:
			n.Vault++
		case ZoneOutside:
			n.Outside++
		default:
			n.Unknown++
		}
		if v.Known && v.Cross {
			n.Cross++
		}
	}
	return n
}

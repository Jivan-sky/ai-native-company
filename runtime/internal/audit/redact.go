package audit

import (
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

// ===========================================================================
// 脱敏 —— 审计落盘前的**强制**内容变换
// ===========================================================================
//
// 起因（议题 #50；2026-10-07 一次真会话实测）：bot 读了 harness 的全局设置文件
// `~/.claude/settings.json`，当时在用的 `ANTHROPIC_AUTH_TOKEN` 明文就跟着工具结果
// 头部进了 `why` 字段，落进 `audit/<月>.<行使者>.jsonl` —— append-only，删不干净。
// 那与 SPEC「config / 备份 / diff 全程无明文」的承诺**直接冲突**。
//
// 三条口径：
//
//  1. **无开关**。脱敏不是门禁，是无条件的内容变换。给它留一个 off 开关，
//     等于给「明文落盘」留了一个合法的写法 —— 所以这里没有 policy 条目、
//     也没有 --no-redact。要改行为，改的是**规则表**（数据），不是把脱敏关掉。
//
//  2. **规则住在数据里**（同 ToolRule 的理由）：加一个键名、加一种密钥形态，
//     改的是这份表，不是这里的 if。客户侧密钥形态与我们不同时，整份换掉即可。
//
//  3. **抹成指针，不抹成空白**：命中之后写的是 `«已脱敏:<规则名>»`，**不是**空字符串。
//     读的人必须还能看出「这里有过一个东西、它是哪一类」——
//     静默抹平与假绿是同一类错误。
//
// 位置：**写口与读口各过一遍**。写口（Append）是承诺的兑现处 ——「落盘前」；
// 读口（Load）是兜底 —— 这份代码落地之前**已经躺着**的明文，至少保证它不再从
// 看板 / CLI 里漏出去，并且**如实报出来**（Doc.Tainted：几行是老明文）。

// RedactRule 是一条脱敏规则。**这是数据**：整份可替换，加条目不用改代码。
type RedactRule struct {
	Name  string `json:"name"`  // 命中时写进占位符的名字（也是「哪一类被抹掉了」的答案）
	Kind  string `json:"kind"`  // key = 「键名后面的那个值」；form = 「长成这样的那一串」
	Match string `json:"match"` // key 用键名（整词匹配）；form 用前缀（前缀原样保留）
}

const (
	RedactKindKey  = "key"
	RedactKindForm = "form"
)

// KnownSecret 是一条**已知密钥值**：名字来自凭据文件的键名，值是明文。
//
// **Value 只用来做字面替换** —— 它不进任何出口：不落盘、不回显、不进 --json。
// 名字可以回显（键名不是秘密，而且回显名字才知道抹掉的是哪一条）。
type KnownSecret struct {
	Name  string
	Value string
}

const (
	markOpen  = "«已脱敏:"
	markClose = "»"
)

func placeholder(name string) string { return markOpen + name + markClose }

// BuiltinRedactRules 是出厂脱敏表。
//
// key 档（键名整词匹配）：**只登记真正装密钥的键名**。刻意不把计量口径的词写进去 ——
// `input_tokens` / `output_tokens` / `max_tokens` 是审计要看的东西，必须原样留着；
// 而整词匹配（见 compileRule）刚好把 `token` 与 `input_tokens` 分开
// （后者里那个 `token` 前面是词字符 `_`，不算一个独立的键名）。
//
// form 档（前缀原样保留、后面那串抹掉）：兜住「键名认不出、形态一眼认得出」的。
func BuiltinRedactRules() []RedactRule {
	return []RedactRule{
		{Name: "ANTHROPIC_AUTH_TOKEN", Kind: RedactKindKey, Match: "ANTHROPIC_AUTH_TOKEN"},
		{Name: "ANTHROPIC_API_KEY", Kind: RedactKindKey, Match: "ANTHROPIC_API_KEY"},
		{Name: "OPENAI_API_KEY", Kind: RedactKindKey, Match: "OPENAI_API_KEY"},
		{Name: "api_key", Kind: RedactKindKey, Match: "api_key"},
		{Name: "api-key", Kind: RedactKindKey, Match: "api-key"},
		{Name: "apikey", Kind: RedactKindKey, Match: "apikey"},
		{Name: "access_token", Kind: RedactKindKey, Match: "access_token"},
		{Name: "refresh_token", Kind: RedactKindKey, Match: "refresh_token"},
		{Name: "id_token", Kind: RedactKindKey, Match: "id_token"},
		{Name: "auth_token", Kind: RedactKindKey, Match: "auth_token"},
		{Name: "client_secret", Kind: RedactKindKey, Match: "client_secret"},
		{Name: "app_secret", Kind: RedactKindKey, Match: "app_secret"},
		{Name: "secret", Kind: RedactKindKey, Match: "secret"},
		{Name: "password", Kind: RedactKindKey, Match: "password"},
		{Name: "passwd", Kind: RedactKindKey, Match: "passwd"},
		{Name: "token", Kind: RedactKindKey, Match: "token"},
		{Name: "authorization", Kind: RedactKindKey, Match: "authorization"},
		{Name: "private_key", Kind: RedactKindKey, Match: "private_key"},

		{Name: "sk", Kind: RedactKindForm, Match: "sk-"},
		{Name: "Bearer", Kind: RedactKindForm, Match: "Bearer "},
		{Name: "Basic", Kind: RedactKindForm, Match: "Basic "},
		{Name: "ghp_", Kind: RedactKindForm, Match: "ghp_"},
		{Name: "gho_", Kind: RedactKindForm, Match: "gho_"},
		{Name: "github_pat_", Kind: RedactKindForm, Match: "github_pat_"},
		{Name: "xoxb-", Kind: RedactKindForm, Match: "xoxb-"},
		{Name: "xoxp-", Kind: RedactKindForm, Match: "xoxp-"},
		{Name: "AKIA", Kind: RedactKindForm, Match: "AKIA"},
		{Name: "ASIA", Kind: RedactKindForm, Match: "ASIA"},
		{Name: "jwt", Kind: RedactKindForm, Match: "eyJ"},
	}
}

var reKeyName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// RulesForKeys 把一组**真实存在的键名**（凭据文件的键）变成 key 档规则。
//
// 为什么要有这一支：ANC 自己的键名是全函数派生的（`ANC_FEISHU_SECRET_<成员>`），
// 出厂表写不全；而且**值是会换的** —— 靠字面值匹配只挡得住「读到的那一次」。
// 把键名本身变成规则，换过的新值、以及别的库里同名的键，都在覆盖范围内。
func RulesForKeys(names []string) []RedactRule {
	out := make([]RedactRule, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] || !reKeyName.MatchString(n) {
			continue
		}
		seen[n] = true
		out = append(out, RedactRule{Name: n, Kind: RedactKindKey, Match: n})
	}
	return out
}

// reEnvRef 是「这已经是个指针了」的样子：`${ENV}` 引用本身不含明文。
// 抹它没有收益，反而会把「这里写的是引用」这个事实抹掉 —— 所以放行。
var reEnvRef = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)

// Redactor 是一份编译好的脱敏表。零值不可用 —— 用 NewRedactor 造。
type Redactor struct {
	rules []RedactRule
	known []KnownSecret
	res   []*regexp.Regexp
}

// NewRedactor 编一份表。rules 留空 = 出厂表（同 Collect 的 Tools 口径）。
func NewRedactor(rules []RedactRule, known []KnownSecret) *Redactor {
	if len(rules) == 0 {
		rules = BuiltinRedactRules()
	}
	r := &Redactor{rules: rules}
	for _, k := range known {
		v := strings.TrimSpace(k.Value)
		if len([]rune(v)) < 6 {
			continue // 值太短：抹它只会误伤正常文本，而它也不构成一个秘密
		}
		if strings.TrimSpace(k.Name) == "" {
			k.Name = "known"
		}
		k.Value = v
		r.known = append(r.known, k)
	}
	// 长的在前：一个密钥是另一个的前缀时（少见但会有），先抹长的才不会留下尾巴。
	sort.SliceStable(r.known, func(i, j int) bool {
		return len(r.known[i].Value) > len(r.known[j].Value)
	})
	for _, rule := range rules {
		r.res = append(r.res, compileRule(rule))
	}
	return r
}

// Rules 给出这份表里的规则（供 `anc audit rules` 回显）。**不含已知值。**
func (r *Redactor) Rules() []RedactRule { return r.rules }

// KnownCount 给出已知密钥值的条数（只给数，不给值）。
func (r *Redactor) KnownCount() int { return len(r.known) }

// Apply 把一段文本过一遍脱敏。返回替换后的文本与**命中的规则名**（去重、按命中顺序）。
//
// 一遍可能不够：一次命中会“揭开”下一层。实测的例子：
// `Authorization: Bearer sk-xxx` —— 键名那一档先把 `Bearer` 当成值抹了，
// 形态那一档就再也够不到后面那串了，于是密钥就被“拆”在外面。
// 所以跑到**不动点**为止；上限四趟是防呆（命中一定改变文本长度，
// 不会无限循环，但上限让「万一」有个确定的结尾）。
func (r *Redactor) Apply(s string) (string, []string) {
	if r == nil || s == "" {
		return s, nil
	}
	var hits []string
	seen := map[string]bool{}
	note := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		hits = append(hits, name)
	}

	for pass := 0; pass < 4; pass++ {
		before := s

		// 1 已知密钥值：**字面替换**，最准的一腿（连没有形态、没有键名的也挡得住）。
		for _, k := range r.known {
			if !strings.Contains(s, k.Value) {
				continue
			}
			s = strings.ReplaceAll(s, k.Value, placeholder(k.Name))
			note(k.Name)
		}

		// 2 规则表：**形态档先跑**（把 `Bearer <token>` 这种整条先吃下来），
		// 然后才是键名档。
		for _, kind := range []string{RedactKindForm, RedactKindKey} {
			for i, rule := range r.rules {
				if rule.Kind != kind {
					continue
				}
				name := rule.Name
				if kind == RedactKindForm {
					s = replaceAll(s, r.res[i], func(sub []string) (string, bool) {
						return renderForm(name, sub)
					}, func() { note(name) })
					continue
				}
				s = replaceAll(s, r.res[i], func(sub []string) (string, bool) {
					return renderKey(name, sub)
				}, func() { note(name) })
			}
		}

		if s == before {
			break
		}
	}
	return s, hits
}

// CleanRecord 把一条记录里**可能夹带内容**的那几格过一遍脱敏，返回命中的规则名。
//
// 只碰这三格：`object` / `why` / `detail` —— 它们是「当时的事实里可能夹着一段原话」的
// 地方。`actor` / `action` / `result` / `tool` / `at` / `id` 是词表与标识，不夹带内容；
// 它们要是被抹了，审计就没法按人、按类别筛了。
func (r *Redactor) CleanRecord(rec *Record) []string {
	var hits []string
	for _, f := range []*string{&rec.Object, &rec.Why, &rec.Detail} {
		s, h := r.Apply(*f)
		*f = s
		hits = append(hits, h...)
	}
	return hits
}

// ---- 编译与替换 ----

// compileRule 按档次编一条正则。
//
// form（前缀原样保留）：
//
//	(前缀)(后面那一串)
//
// 串的字符集是 base64 / hex / 连字符这一族 —— 密钥长这样，人话不长这样。
//
// key（键名整词匹配）：
//
//	(前一个字符不是词字符)(键名自己的开引号)(键名)(收尾引号 + : 或 = + 空白)(值)
//
// 键名**必须**捕成一个组：重建那一步是“原文拆成五段再拼回去”，
// 捕不到就拼不回去 —— 键名会被吞掉（`"键": "值"` 变成 `"": "«已脱敏:键»"`）。
// 实测踩到过。用组而不用 rule.Match：匹配是不区分大小写的，
// 原文里写的是什么大小写，拼回去的就该是什么。
//
// 「前一个字符」那一条是**必须的**：没有它，`token` 会命中 `input_tokens`
// （而那是用量口径，抹了它审计就瞎了）。值四种写法都要认：双引号串 / 单引号串 /
// **裸的 `${ENV}` 指针** / 裸串 —— JSON 的 `"k": "v"`、env 的 `K=v`、yaml 的 `k: v`
// 全在这几种里。
//
// 指针那一支单列，是因为裸串那一支的字符集**排掉了 `}`**（不排掉它，`{"k":"v"}` 的
// 收尾大括号会被当成值的一部分吃掉）。于是 `K=${VAR}` 会被截成 `${VAR`，`redactable`
// 认不出它已经是个指针、照抹，还会留下一个孤零零的 `}`。命中顺序上指针支在裸串支之前，
// 整条 `${VAR}` 才能完整地交给 `reEnvRef` 放行。
// 实测踩到过：`token=${ANTHROPIC_AUTH_TOKEN}` → `token=「已脱敏:token」}`。
func compileRule(rule RedactRule) *regexp.Regexp {
	if rule.Kind == RedactKindForm {
		// 前缀也要**捕成组**：下标数组的 sub[0] 是整段命中，
		// 所以拼回去的预缀只能从捕获组里拿 —— 不捕就拿不到（实测踩到过：
		// 形态档因此一次都没命中过）。
		return regexp.MustCompile(`((?i:` + regexp.QuoteMeta(rule.Match) + `))([A-Za-z0-9_\-\.]{8,})`)
	}
	return regexp.MustCompile(
		`(?i)(^|[^A-Za-z0-9_])(["']?)(` + regexp.QuoteMeta(rule.Match) + `)` +
			`(["']?[ \t]*[:=][ \t]*)("[^"\n]*"|'[^'\n]*'|\$\{[A-Za-z_][A-Za-z0-9_]*\}|[^\s"',;)\]}]*)`)
}

// replaceAll 用 re 扫一遍 s，逐处交给 render 决定换不换。
//
// 刻意**不用** regexp.ReplaceAllStringFunc：它回调里拿到的是整段命中，再拿它去
// FindStringSubmatch 会让 `^` 那一条在命中内部重新成立，前缀字符就可能被吃掉。
// 这里用带下标的扫描，重建时逐字拼接，边界不会被改。
func replaceAll(s string, re *regexp.Regexp, render func(sub []string) (string, bool), hit func()) string {
	locs := re.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	prev := 0
	done := false
	for _, loc := range locs {
		if loc[0] < prev {
			continue // 与前一处重叠：那一处已经吃掉了
		}
		sub := make([]string, len(loc)/2)
		for i := range sub {
			if loc[2*i] < 0 {
				continue
			}
			sub[i] = s[loc[2*i]:loc[2*i+1]]
		}
		out, ok := render(sub)
		if !ok {
			continue
		}
		b.WriteString(s[prev:loc[0]])
		b.WriteString(out)
		prev = loc[1]
		done = true
		hit()
	}
	if !done {
		return s
	}
	b.WriteString(s[prev:])
	return b.String()
}

// renderKey 是 key 档的替换：值（连同它自己的引号）换成占位符，其余原样保留。
func renderKey(name string, sub []string) (string, bool) {
	// sub[0] 是整段命中，所以五项在 sub[1..5]：钥前字符 / 开引号 / 键名 / 分隔符 / 值。
	if len(sub) < 6 {
		return "", false
	}
	val := strings.TrimSpace(sub[5])
	quote := ""
	if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
		quote = string(val[0])
		val = val[1 : len(val)-1]
	}
	if !redactable(val) {
		return "", false
	}
	return sub[1] + sub[2] + sub[3] + sub[4] + quote + placeholder(name) + quote, true
}

// renderForm 是 form 档的替换：前缀保留，后面那一串换成占位符。
func renderForm(name string, sub []string) (string, bool) {
	// sub[1] = 前缀（原样保留），sub[2] = 后面那一串。
	if len(sub) < 3 {
		return "", false
	}
	if !redactable(sub[2]) {
		return "", false
	}
	return sub[1] + placeholder(name), true
}

// redactable 判断一段「取出来的值」值不值得抹。不抹的三种：
//
//   - 空的（没有明文可抹）；
//   - 已经是指针了（`«已脱敏:…»` 或 `${ENV}`）—— 抹它等于把「这是引用」也抹掉；
//   - 太短（少于 4 个字符）—— 那种长度是开关值 / 数字，不是密钥，
//     抹了只会误伤正常文本，而审计要读的正是那些。
func redactable(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || strings.Contains(v, markOpen) || reEnvRef.MatchString(v) {
		return false
	}
	return len([]rune(v)) >= 4
}

// ---- 进程级那一份 ----

var processRedactor atomic.Pointer[Redactor]

var builtinRedactor = NewRedactor(BuiltinRedactRules(), nil)

// Effective 返回**当前生效**的脱敏表。永远不返回 nil —— 也就是永远有脱敏：
// 没落定过就是出厂表。这一条是刻意的（见包顶第 1 条口径）。
func Effective() *Redactor {
	if r := processRedactor.Load(); r != nil {
		return r
	}
	return builtinRedactor
}

// Scrub 是**给别的流水用的那个口**：把一段自由文本过一遍当前生效的脱敏表。
//
// 为什么不让调用方各自 `Effective().Apply`：审计不是唯一一条会夹带原话的流水 ——
// `timeline` 的 title / detail 与 `envelope` 的 body 是同一类东西（议题 #64）。
// 三条流水吃**同一张表、同一个占位符形状**，才不会出现「一处抹了、另一处留着」。
func Scrub(s string) (string, []string) {
	if s == "" {
		return s, nil
	}
	return Effective().Apply(s)
}

// SetRedactor 落定进程级脱敏表。调用方是 main（启动时一次），
// 带上凭据文件里的已知值。传 nil 不生效 —— 不许出现「没有脱敏」这个状态。
func SetRedactor(r *Redactor) {
	if r != nil {
		processRedactor.Store(r)
	}
}

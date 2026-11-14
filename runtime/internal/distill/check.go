package distill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"anc/internal/audit"
	"anc/internal/org"
	"anc/internal/timeline"
)

// CheckSchema 是发布前校验的版本号。
const CheckSchema = "anc.distill.check/v1"

// 三档：fail 才拦发布；warn 是「发得出去，但会出问题」；ok 是这一项过了。
const (
	LevelOK   = "ok"
	LevelWarn = "warn"
	LevelFail = "fail"
)

// RequiredSections 是技能正文的四段 —— 草稿模板与检查共用这一份清单。
var RequiredSections = []string{"何时用", "输入", "步骤", "验证"}

// CheckItem 是一条检查结果。ID 稳定，便于机器过滤与回归。
type CheckItem struct {
	ID     string `json:"id"`
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// CheckReport 是 `anc distill check` 与未来看板共用的形状。
type CheckReport struct {
	Schema   string      `json:"schema"`
	Name     string      `json:"name"`
	Draft    string      `json:"draft"`
	Official string      `json:"official"`
	Ok       bool        `json:"ok"`
	Fails    int         `json:"fails"`
	Warns    int         `json:"warns"`
	Items    []CheckItem `json:"items"`
	Note     string      `json:"note"`
}

// CheckInput 是纯函数输入：文件系统在 LoadCheck 里读完。
type CheckInput struct {
	Name string

	DraftExists bool
	Draft       string

	OfficialExists bool

	RolesWired bool
	Roles      []RoleSkills

	TimelineWired bool
	Entries       []timeline.Entry
}

// Check 做发布前体检。只读：不修草稿、不写 skills/、不登记 roles。
//
// 拦发布（fail）的只有「写出去会坏」的几件事：名字不合法、草稿不在、frontmatter 对不上、
// 四段缺内容、占位符、明文命中了脱敏规则、正式落点已被占用。
// 「发出去 agent 也看不见」（没有岗位声明）与「来源对不上」只提醒 —— 那是人的决定，
// 不是代码该替人拦的门。
func Check(in CheckInput) CheckReport {
	r := CheckReport{
		Schema: CheckSchema, Name: in.Name,
		Draft:    "skills/_drafts/" + in.Name + ".md",
		Official: "skills/" + in.Name + "/SKILL.md",
		Items:    []CheckItem{},
		Note:     "这一条只读：不改草稿、不写 skills/、不登记 roles。",
	}
	add := func(id, level, title, detail string) {
		r.Items = append(r.Items, CheckItem{ID: id, Level: level, Title: title, Detail: detail})
	}

	if !ValidSkillName(in.Name) {
		add("name.valid", LevelFail, "技能名不合法", "必须是小写 ASCII（^[a-z][a-z0-9-]*$）、字母开头、最长 64")
		return finish(r)
	}
	add("name.valid", LevelOK, "技能名合法", in.Name)

	if !in.DraftExists {
		add("draft.exists", LevelFail, "草稿不存在", r.Draft+" —— 先用 anc distill draft 生成")
	} else {
		add("draft.exists", LevelOK, "草稿存在", r.Draft)
		fm, problems, fmOK := parseFrontmatter(in.Draft)
		switch {
		case !fmOK:
			add("draft.frontmatter", LevelFail, "frontmatter 读不懂", "第一行必须是 ---，并用第二个 --- 收尾")
		default:
			if len(problems) > 0 {
				add("draft.meta", LevelFail, "frontmatter 有疑义", strings.Join(problems, "；"))
			} else {
				add("draft.meta", LevelOK, "frontmatter 解析通过", "单行 / 折行 / 块标量都按同一份约定读")
			}
			if got := strings.TrimSpace(fm["name"]); got != in.Name {
				add("draft.name_matches", LevelFail, "frontmatter 的 name 与文件名不一致", "文件是 "+in.Name+"，里面写的是 "+quoteOrEmpty(got))
			} else {
				add("draft.name_matches", LevelOK, "frontmatter 与文件名一致", in.Name)
			}
			// A6：description 是**宿主发现技能的唯一依据**（它决定 agent 什么时候会选它）。
			// 以前这一项压根没查，于是"没有描述的技能"也能通过校验、发布出去。
			if why := descriptionProblem(fm["description"]); why != "" {
				add("draft.description", LevelFail, "description 不合格", why)
			} else {
				add("draft.description", LevelOK, "description 合格", "中英触发语都有")
			}
		}
		secs := parseSections(in.Draft)
		for _, name := range RequiredSections {
			body := strings.TrimSpace(secs[name])
			if body == "" {
				add("draft.section."+name, LevelFail, "「"+name+"」缺内容", "发布前必须写清楚；草稿只装事实，这一段要人补")
				continue
			}
			add("draft.section."+name, LevelOK, "「"+name+"」有内容", firstLineOfBody(body))
		}
		if tok := firstPlaceholder(in.Draft); tok != "" {
			add("draft.placeholder", LevelFail, "正文里还有占位符", "命中："+tok+" —— 发布前删掉或写完")
		} else {
			add("draft.placeholder", LevelOK, "没有占位符", "TODO / TBD / FIXME / 待补 / 以后补")
		}
		if _, hits := audit.Effective().Apply(in.Draft); len(hits) > 0 {
			add("draft.secret", LevelFail, "正文里有会被脱敏规则命中的内容", "规则："+strings.Join(hits, " / ")+" —— 明文不该写进技能正文")
		} else {
			add("draft.secret", LevelOK, "没有命中脱敏规则", "按出厂规则表 + 本机凭据名")
		}
		if lines := absolutePathLines(in.Draft); len(lines) > 0 {
			add("draft.absolute_path", LevelWarn, "正文里有本机绝对路径", "第 "+joinInts(lines)+" 行 —— 技能正文应写 vault 相对指针，换台机器会失效")
		} else {
			add("draft.absolute_path", LevelOK, "没有本机绝对路径", "")
		}
	}

	if in.OfficialExists {
		add("official.collision", LevelFail, "正式落点已被占用", r.Official+" 已存在 —— 要么改 --name，要么直接改那一份，别用草稿覆盖")
	} else {
		add("official.collision", LevelOK, "不撞正式技能", r.Official+" 还不存在")
	}

	if !in.RolesWired {
		add("roles.wired", LevelWarn, "岗位清单读不动", "无法确认发布后有没有岗位声明它")
	} else if roles := declaringRoles(in.Roles, in.Name); len(roles) == 0 {
		add("roles.declared", LevelWarn, "没有任何岗位声明这个技能", "发布后 agent 取不到 —— 在 roles/<岗位>/persona.md 的 skills: 里登记")
	} else {
		add("roles.declared", LevelOK, "已被岗位声明", strings.Join(roles, " / "))
	}

	if !in.TimelineWired {
		add("case.provenance", LevelWarn, "timeline 读不动", "无法核对来源 case")
	} else if c, why := findGreenCase(in.Entries, in.Name, in.Draft); c == "" {
		add("case.provenance", LevelWarn, "来源 case 对不上", why)
	} else {
		add("case.provenance", LevelOK, "来源 case 是绿档", c)
	}
	// B2：草稿还绑着**具体哪一条结案事件**吗？重开、重新结案、出现冲突之后，
	// 旧草稿的 case 可能仍然是绿的 —— 但它依据的那个"完成"已经不是当前态了。
	if why := draftHeadStale(in.Entries, in.Draft); why != "" {
		add("case.head_event", LevelWarn, "依据过期", why)
	} else if in.Draft != "" && in.TimelineWired {
		add("case.head_event", LevelOK, "依据仍对得上当前态", "")
	}
	return finish(r)
}

// LoadCheck 读 vault 里的草稿 / 正式落点 / 岗位 / timeline，再交给 Check。
func LoadCheck(vault, name string) CheckReport {
	in := CheckInput{Name: name}
	if !ValidSkillName(name) {
		return Check(in)
	}
	if b, err := os.ReadFile(filepath.Join(vault, "skills", "_drafts", name+".md")); err == nil {
		in.DraftExists = true
		in.Draft = string(b)
	}
	if _, err := os.Stat(filepath.Join(vault, "skills", name, "SKILL.md")); err == nil {
		in.OfficialExists = true
	}
	if doc, err := timeline.Load(vault); err == nil {
		in.TimelineWired = true
		in.Entries = doc.Entries
	}
	if o, err := org.Load(vault); err == nil {
		in.RolesWired = true
		for role, rs := range o.Roles {
			in.Roles = append(in.Roles, RoleSkills{Role: role, Skills: rs.Skills})
		}
		sort.Slice(in.Roles, func(i, j int) bool { return in.Roles[i].Role < in.Roles[j].Role })
	}
	return Check(in)
}

func finish(r CheckReport) CheckReport {
	for _, it := range r.Items {
		switch it.Level {
		case LevelFail:
			r.Fails++
		case LevelWarn:
			r.Warns++
		}
	}
	r.Ok = r.Fails == 0
	return r
}

// parseFrontmatter 读 SKILL.md 的 YAML 头。
//
// 这一版起**与宿主读法对齐**（A6）：以前按行 `Cut(":")`，只认单行值 ——
// 折行描述（`description: >` / `|`，或缩进续行）会被读成空串或半句，
// 于是"我们校验通过、宿主读出来是另一句"成了可能。
//
// 支持：
//   - 单行 `key: value`
//   - 块标量 `key: >`（折成一行，空行转换行）/ `key: |`（保留换行）
//   - 缩进续行（没有块标记，但下一行缩进更深 → 拼进去）
//   - 重复键 → 报问题（不静默取后者）
//
// 返回值第二个是问题清单（空 = 没问题），第三个是「有没有 frontmatter」。
func parseFrontmatter(md string) (map[string]string, []string, bool) {
	s := strings.TrimPrefix(md, "\ufeff")
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, nil, false
	}
	out := map[string]string{}
	var problems []string
	seen := map[string]int{}
	for i := 1; i < len(lines); i++ {
		raw := lines[i]
		ln := strings.TrimSpace(raw)
		if ln == "---" {
			return out, problems, true
		}
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		// 缩进的行属于上一个键的续行（已在下面消费掉；走到这里说明没有上一个键）
		if raw != strings.TrimLeft(raw, " \t") {
			continue
		}
		k, v, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" {
			continue
		}
		if first, dup := seen[key]; dup {
			problems = append(problems, fmt.Sprintf("字段 %s 重复（第 %d 行与第 %d 行）—— 宿主只会认一个，我们不猜是哪个", key, first, i+1))
			continue
		}
		seen[key] = i + 1

		// 块标量 / 续行
		switch val {
		case ">", "|", ">-", "|-", ">+", "|+":
			var body []string
			j := i + 1
			for ; j < len(lines); j++ {
				nxt := lines[j]
				if strings.TrimSpace(nxt) == "---" {
					break
				}
				if strings.TrimSpace(nxt) == "" {
					body = append(body, "")
					continue
				}
				if nxt == strings.TrimLeft(nxt, " \t") {
					break // 回到顶格 = 下一个键
				}
				body = append(body, strings.TrimSpace(nxt))
			}
			i = j - 1
			if strings.HasPrefix(val, "|") {
				out[key] = strings.TrimSpace(strings.Join(body, "\n"))
			} else {
				out[key] = strings.TrimSpace(foldLines(body))
			}
			continue
		}
		if val == "" {
			// 可能是缩进续行
			var body []string
			j := i + 1
			for ; j < len(lines); j++ {
				nxt := lines[j]
				if strings.TrimSpace(nxt) == "---" {
					break
				}
				if strings.TrimSpace(nxt) == "" {
					continue
				}
				if nxt == strings.TrimLeft(nxt, " \t") {
					break
				}
				body = append(body, strings.TrimSpace(nxt))
			}
			if len(body) > 0 {
				i = j - 1
				out[key] = strings.TrimSpace(foldLines(body))
				continue
			}
		}
		out[key] = strings.Trim(val, `"'`)
	}
	return nil, problems, false
}

// foldLines 是 YAML 的「折行」：相邻行拼成一句话，空行留一个换行。
func foldLines(body []string) string {
	var b strings.Builder
	prevBlank := true
	for _, ln := range body {
		if ln == "" {
			b.WriteString("\n")
			prevBlank = true
			continue
		}
		if !prevBlank {
			b.WriteString(" ")
		}
		b.WriteString(ln)
		prevBlank = false
	}
	return b.String()
}

var h2RE = regexp.MustCompile(`^##\s+(.+?)\s*$`)

// parseSections 把正文按二级标题切开；自闭合的 ``` 代码块里不切。
func parseSections(md string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	cur := ""
	var b strings.Builder
	fence := false
	flush := func() {
		if cur != "" {
			out[cur] = strings.TrimSpace(b.String())
		}
		b.Reset()
	}
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			fence = !fence
			b.WriteString(ln + "\n")
			continue
		}
		if !fence {
			if m := h2RE.FindStringSubmatch(ln); m != nil {
				flush()
				cur = strings.TrimSpace(m[1])
				continue
			}
		}
		if cur != "" {
			b.WriteString(ln + "\n")
		}
	}
	flush()
	return out
}

func firstPlaceholder(md string) string {
	up := strings.ToUpper(md)
	for _, tok := range []string{"TODO", "TBD", "FIXME"} {
		if strings.Contains(up, tok) {
			return tok
		}
	}
	for _, tok := range []string{"待补", "以后补"} {
		if strings.Contains(md, tok) {
			return tok
		}
	}
	return ""
}

var absPathRE = regexp.MustCompile(`(?i)([A-Za-z]:\\|/Users/|/home/|/mnt/)`)

func absolutePathLines(md string) []int {
	var out []int
	for i, ln := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if absPathRE.MatchString(ln) {
			out = append(out, i+1)
		}
	}
	return out
}

func declaringRoles(roles []RoleSkills, name string) []string {
	var out []string
	for _, rs := range roles {
		for _, s := range rs.Skills {
			if strings.TrimSpace(s) == name {
				out = append(out, rs.Role)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func findGreenCase(entries []timeline.Entry, name, draft string) (string, string) {
	fm, _, ok := parseFrontmatter(draft)
	if !ok {
		return "", "草稿没有 frontmatter —— 判不出它来源哪个 case"
	}
	key := strings.TrimSpace(fm["case"])
	if key == "" {
		return "", "frontmatter 没写 case —— 来源无法回溯"
	}
	for _, c := range Build(Input{Entries: entries, TimelineWired: true}).Candidates {
		if c.Case == key {
			return key, ""
		}
	}
	// 在候选之外的原因里找：是被判成冲突 / 待核实，还是根本没闭环。
	for _, x := range Build(Input{Entries: entries, TimelineWired: true}).Excluded {
		if x.Case == key {
			return "", "来源 case 还没法结案：" + x.Why
		}
	}
	return "", "frontmatter 里的 case 不在闭环里（或被删了）—— 来源无法回溯"
}

// draftHeadStale 判「草稿依据的那条结案事件」是否还是当前态。
//
// 三种情况要拦住：
//  1. 草稿写了 head_event，但现在算出来的头是另一条 → 重开/重新结案了
//  2. 这个 case 现在判成冲突 / 待核实 → 当前态不确定，草稿的依据也就不可靠
//  3. 草稿根本没写 head_event（老草稿）→ 只能说"依据未登记"
func draftHeadStale(entries []timeline.Entry, draft string) string {
	if strings.TrimSpace(draft) == "" {
		return ""
	}
	fm, _, ok := parseFrontmatter(draft)
	if !ok {
		return "草稿没有 frontmatter —— 判不出它依据哪一条结案事件"
	}
	key := strings.TrimSpace(fm["case"])
	if key == "" {
		return ""
	}
	want := strings.TrimSpace(fm["head_event"])
	cases := timeline.Fold(entries)
	var c *timeline.Case
	for i := range cases {
		if cases[i].Key == key {
			c = &cases[i]
			break
		}
	}
	if c == nil {
		return "这个 case 在时间线里找不到了"
	}
	switch {
	case c.Conflict:
		return "这个 case 现在状态冲突（两份相反主张互不引用）—— 草稿依据的结论还不成立"
	case c.Pending:
		return "这个 case 顺序待核实（前序没到齐）—— 可能还有一条会推翻草稿的依据"
	case want == "":
		return "草稿没登记 head_event（老草稿）—— 无法确认它依据的是哪一次结案"
	case want != c.Head:
		return "草稿依据的是 " + want + "，但当前态已经是 " + c.Head + " —— 重开或重新结案过了"
	}
	return ""
}

func firstLineOfBody(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			if len([]rune(t)) > 60 {
				return string([]rune(t)[:60]) + "…"
			}
			return t
		}
	}
	return ""
}

func quoteOrEmpty(s string) string {
	if s == "" {
		return "（空）"
	}
	return s
}

func joinInts(v []int) string {
	parts := make([]string, 0, len(v))
	for _, n := range v {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ", ")
}

// descriptionProblem 判 description 够不够格被宿主发现。
//
// 三条要求，都是从「宿主怎么挑技能」倒推的：
//  1. 非空 —— 空的描述在技能清单里等于隐身
//  2. 说清何时用 —— 至少要有一句人话（不是 "TODO"）
//  3. 中英触发语都有 —— 中文团队与英文 harness 各读一套，缺一套就会漏触发
func descriptionProblem(desc string) string {
	d := strings.TrimSpace(desc)
	if d == "" {
		return "缺 description —— 宿主靠它决定什么时候选这个技能，空的等于没发布"
	}
	if len([]rune(d)) < 12 {
		return "description 太短（" + strconv.Itoa(len([]rune(d))) + " 字）—— 要写清「什么时候用」"
	}
	hasCJK := false
	for _, r := range d {
		if r >= 0x4e00 && r <= 0x9fff {
			hasCJK = true
			break
		}
	}
	hasLatin := false
	for _, r := range d {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLatin = true
			break
		}
	}
	if !hasCJK || !hasLatin {
		return "description 要同时有中文与英文触发语（现在是" + map[bool]string{true: "只有中文", false: "只有英文"}[hasCJK] + "）"
	}
	return ""
}

package org

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Company 是 company/company.md 的机器可读部分。
type Company struct {
	Name            string
	ID              string
	Language        string
	Timezone        string
	Platform        string
	Admins          []string
	SyncIntervalMin int
	Defaults        Defaults
	Fallback        *FallbackProvider
	Body            string
}

// Defaults 是公司级默认值；成员/角色可逐级覆盖 model。
type Defaults struct {
	Model                 string
	Mode                  string
	AutoCompressMaxTokens int
}

// FallbackProvider 是兜底 provider 声明（api_key 只允许 env 引用）。
type FallbackProvider struct {
	Name    string
	BaseURL string
	Model   string
}

// Section 是 persona 正文里的一个 H2 段。保留声明顺序 —— 渲染要按作者的原文顺序走。
type Section struct {
	Name string
	Text string
}

// Role 是 roles/<role>/persona.md。
type Role struct {
	Role         string
	Title        string
	Model        string
	Mode         string
	AllowedTools []string
	VaultScope   []string
	Skills       []string
	Sections     map[string]string // 白名单段名 → 正文（清单见 company.persona_sections）
	Extra        []Section         // 白名单之外、但被降级放行的段：原样保留，不倒掉作者的稿子
}

// Feishu 是成员的平台接入信息。open_id 是本系统里唯一的「人」标识，且只在渲染时进 allow_from。
type Feishu struct {
	AppID          string
	OpenID         string
	ExtraAllowFrom []string
	AllowChat      []string
}

// Member 是 members/<name>/persona.md。
type Member struct {
	Name        string
	DisplayName string
	Role        string
	Model       string
	Admin       bool
	Disabled    bool
	Domains     []string // 所属业务域（slug，见 domains.md）；persona 第 8 段与授权作用域都用它
	Feishu      Feishu
	Body        string
}

// Routing 是 vault 顶层数据目录 → 说明，供 persona 段 3 的确定性路由表使用。
type Routing struct {
	Dir     string
	Summary string
}

// Org 是加载并校验过的组织真相源。
type Org struct {
	Root     string
	Company  Company
	Roles    map[string]Role
	Members  []Member  // 按 Name 排序
	Domains  []Domain  // domains.md 的业务域表（罗盘）；文件不存在时为空
	Projects []Project // projects.md 的项目汇总（一行一个项目）；文件不存在时为空
	Routing  []Routing
	Policy   *Policy // 生效规则表（出厂默认 + company.md 的 policy 段覆盖）
	Warnings []Issue // 非红档的校验发现；调用方必须回显，不许吞
}

// Host 是本机层输入（不进 git）；参与 inputs 指纹，改主机参数同样触发重渲染。
type Host struct {
	VaultRoot string
	HomesRoot string
	DataDir   string
}

// skipDirs 是不当数据目录扫描的顶层目录（结构目录，不是业务数据）。
var skipDirs = map[string]bool{
	"roles": true, "members": true, "skills": true, "company": true,
	"templates": true, "scripts": true, "docs": true, "_originals": true,
}

var (
	reID     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	reOpenID = regexp.MustCompile(`^ou_`)
	reH2     = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)
)

// ValidCompanyID 判断公司 id 合不合法。导出它是为了在生成骨架时就能拦住 ——
// 等渲染到一半才报，人已经填了一堆东西了。
func ValidCompanyID(s string) bool { return reID.MatchString(s) }

// Load 读取并校验一棵 org 真相源树。
func Load(root string) (*Org, error) {
	o := &Org{Root: root, Roles: map[string]Role{}}

	cd, err := readDoc(filepath.Join(root, "company", "company.md"))
	if err != nil {
		return nil, err
	}
	c := Company{Platform: "feishu"}
	if c.Name, err = cd.Str("name"); err != nil {
		return nil, err
	}
	if c.ID, err = cd.Str("id"); err != nil {
		return nil, err
	}
	if c.Language, err = cd.Str("language"); err != nil {
		return nil, err
	}
	if c.Timezone, err = cd.Str("timezone"); err != nil {
		return nil, err
	}
	if c.Platform, err = cd.Str("platform"); err != nil {
		return nil, err
	}
	if c.Platform == "" {
		c.Platform = "feishu"
	}
	if c.Admins, err = cd.StrList("admins"); err != nil {
		return nil, err
	}
	if c.SyncIntervalMin, err = cd.Int("sync_interval_min"); err != nil {
		return nil, err
	}
	if c.Defaults.Model, err = cd.Str("defaults.model"); err != nil {
		return nil, err
	}
	if c.Defaults.Mode, err = cd.Str("defaults.mode"); err != nil {
		return nil, err
	}
	if c.Defaults.AutoCompressMaxTokens, err = cd.Int("defaults.auto_compress_max_tokens"); err != nil {
		return nil, err
	}
	if cd.Has("fallback_provider.name") || cd.Has("fallback_provider.base_url") || cd.Has("fallback_provider.model") {
		fp := &FallbackProvider{}
		if fp.Name, err = cd.Str("fallback_provider.name"); err != nil {
			return nil, err
		}
		if fp.BaseURL, err = cd.Str("fallback_provider.base_url"); err != nil {
			return nil, err
		}
		if fp.Model, err = cd.Str("fallback_provider.model"); err != nil {
			return nil, err
		}
		c.Fallback = fp
	}
	c.Body = cd.Body
	o.Company = c

	// 生效规则表：出厂默认 → company.md 的 policy 段覆盖。
	// 覆盖本身也要被校验（拼错规则 id = 你以为关了其实没关），所以它的发现跟主校验一起报。
	pol := DefaultPolicy()
	polIssues := pol.Apply(cd)
	o.Policy = pol

	sections := []string{"职责", "风格", "术语表"}
	if v, err := cd.StrList("persona_sections"); err != nil {
		return nil, err
	} else if len(v) > 0 {
		sections = v
	}
	allowedSection := map[string]bool{}
	for _, s := range sections {
		allowedSection[s] = true
	}

	roleDirs, err := subdirs(filepath.Join(root, "roles"))
	if err != nil {
		return nil, err
	}
	for _, name := range roleDirs {
		d, err := readDoc(filepath.Join(root, "roles", name, "persona.md"))
		if err != nil {
			return nil, err
		}
		r := Role{Role: name, Sections: map[string]string{}}
		if v, err := d.Str("role"); err != nil {
			return nil, err
		} else if v != "" {
			r.Role = v
		}
		if r.Title, err = d.Str("title"); err != nil {
			return nil, err
		}
		if r.Model, err = d.Str("model"); err != nil {
			return nil, err
		}
		if r.Mode, err = d.Str("mode"); err != nil {
			return nil, err
		}
		if r.AllowedTools, err = d.StrList("allowed_tools"); err != nil {
			return nil, err
		}
		if r.VaultScope, err = d.StrList("vault_scope"); err != nil {
			return nil, err
		}
		if r.Skills, err = d.StrList("skills"); err != nil {
			return nil, err
		}
		r.Sections = map[string]string{}
		for _, s := range splitH2(d.Body) {
			if allowedSection[s.Name] {
				r.Sections[s.Name] = s.Text
				continue
			}
			polIssues = append(polIssues, pol.Issue("role.persona.section_unknown",
				"roles/"+name+"/persona.md",
				"正文出现未约定的 H2 段「%s」；白名单是 %s —— 角色层不许私藏事实（canonical registry 纪律）。要放行就在 company.md 写 persona_sections，或在 policy 段把本规则降到 warn/off",
				s.Name, strings.Join(sections, " / ")))
			r.Extra = append(r.Extra, s)
		}
		o.Roles[r.Role] = r
	}

	memberDirs, err := subdirs(filepath.Join(root, "members"))
	if err != nil {
		return nil, err
	}
	for _, dir := range memberDirs {
		d, err := readDoc(filepath.Join(root, "members", dir, "persona.md"))
		if err != nil {
			return nil, err
		}
		m := Member{Name: dir, Body: d.Body}
		if v, err := d.Str("name"); err != nil {
			return nil, err
		} else if v != "" {
			m.Name = v
		}
		if m.DisplayName, err = d.Str("display_name"); err != nil {
			return nil, err
		}
		if m.Role, err = d.Str("role"); err != nil {
			return nil, err
		}
		if m.Model, err = d.Str("model"); err != nil {
			return nil, err
		}
		if m.Admin, err = d.Bool("admin"); err != nil {
			return nil, err
		}
		if m.Disabled, err = d.Bool("disabled"); err != nil {
			return nil, err
		}
		if m.Domains, err = d.StrList("domains"); err != nil {
			return nil, err
		}
		if m.Feishu.AppID, err = d.Str("feishu.app_id"); err != nil {
			return nil, err
		}
		if m.Feishu.OpenID, err = d.Str("feishu.open_id"); err != nil {
			return nil, err
		}
		if m.Feishu.ExtraAllowFrom, err = d.StrList("feishu.extra_allow_from"); err != nil {
			return nil, err
		}
		if m.Feishu.AllowChat, err = d.StrList("feishu.allow_chat"); err != nil {
			return nil, err
		}
		o.Members = append(o.Members, m)
	}
	sort.Slice(o.Members, func(i, j int) bool { return o.Members[i].Name < o.Members[j].Name })

	if o.Routing, err = scanRouting(root); err != nil {
		return nil, err
	}
	// 域表（罗盘）。文件不在 = 这家公司还没划域，不算错（存量 vault 向后兼容）。
	// 它的发现与 policy 段的问题同一批回显：都是「真相源本身」的问题，不是 persona 文本问题。
	domains, domIssues, err := LoadDomains(root, pol)
	if err != nil {
		return nil, err
	}
	o.Domains = domains
	polIssues = append(polIssues, domIssues...)
	// 项目表（立项书汇总）。同域表一档：文件不在 = 还没建表，不算错（存量 vault 向后兼容）。
	// 它是「谁在做哪个项目」的唯一机器落点 —— 所以和域表一样，只有一处维护。
	projects, projIssues, err := LoadProjects(root, pol)
	if err != nil {
		return nil, err
	}
	o.Projects = projects
	polIssues = append(polIssues, projIssues...)
	rep := o.Validate(pol)
	o.validateDomains(pol, rep)
	o.validateProjects(pol, rep)
	rep.Merge(&Report{Issues: polIssues})
	if len(rep.Fatal()) > 0 {
		return nil, &LoadError{Report: rep}
	}
	o.Warnings = rep.Warns()
	return o, nil
}

// Enabled 返回启用中的成员（disabled = 离职停用而不删档）。
func (o *Org) Enabled() []Member {
	out := make([]Member, 0, len(o.Members))
	for _, m := range o.Members {
		if !m.Disabled {
			out = append(out, m)
		}
	}
	return out
}

// Member 按 name 取成员。
func (o *Org) Member(name string) (Member, bool) {
	for _, m := range o.Members {
		if m.Name == name {
			return m, true
		}
	}
	return Member{}, false
}

// ModelFor 三级回退：member.model ∥ role.model ∥ company.defaults.model。
func (o *Org) ModelFor(m Member) string {
	if m.Model != "" {
		return m.Model
	}
	if r, ok := o.Roles[m.Role]; ok && r.Model != "" {
		return r.Model
	}
	return o.Company.Defaults.Model
}

// Validate 校验全量口径。一次返回所有发现，再由规则表的档次分流：红的拦，其余回显。
func (o *Org) Validate(p *Policy) *Report {
	rep := &Report{}
	companyFile := "company/company.md"

	if o.Company.Name == "" {
		rep.Add(p.Issue("company.name.missing", companyFile, "缺 name"))
	}
	if !reID.MatchString(o.Company.ID) {
		rep.Add(p.Issue("company.id.format", companyFile,
			"id=%q 必须是 ASCII 前缀（%s），它会进 launchd label 与项目名前缀", o.Company.ID, reID))
	}
	if o.Company.Platform != "feishu" {
		rep.Add(p.Issue("company.platform.unsupported", companyFile, "platform=%q，v1 只支持 feishu", o.Company.Platform))
	}
	if o.Company.Defaults.Model == "" {
		rep.Add(p.Issue("company.defaults.model.missing", companyFile,
			"缺 defaults.model（成员与角色都没写 model 时没有回退值）"))
	}
	if o.Company.Defaults.Mode == "bypassPermissions" {
		rep.Add(p.Issue("company.defaults.mode.bypass", companyFile,
			"defaults.mode 不许是 bypassPermissions（SPEC §6-5，无豁免开关）"))
	}

	var devbots int
	seenAppID := map[string]string{}
	seenName := map[string]bool{}
	seenOpenID := map[string]string{}
	for _, m := range o.Members {
		where := "members/" + m.Name + "/persona.md"
		if seenName[m.Name] {
			rep.Add(p.Issue("member.name.duplicate", where, "成员 id 重复"))
		}
		seenName[m.Name] = true
		if m.DisplayName == "" {
			rep.Add(p.Issue("member.display_name.missing", where, "缺 display_name"))
		}
		if m.Role == "devbot" && !m.Disabled {
			devbots++
		}
		if m.Feishu.OpenID == "" {
			rep.Add(p.Issue("member.feishu.open_id.missing", where,
				"缺 feishu.open_id（渲染 allow_from 用；发 /whoami 给 bot 可取）"))
		} else if !reOpenID.MatchString(m.Feishu.OpenID) {
			rep.Add(p.Issue("member.feishu.open_id.format", where, "feishu.open_id=%q 不符合 ^ou_", m.Feishu.OpenID))
		} else if prev, dup := seenOpenID[m.Feishu.OpenID]; dup {
			rep.Add(p.Issue("member.feishu.open_id.duplicate", where, "feishu.open_id 与 members/%s 重复", prev))
		} else {
			seenOpenID[m.Feishu.OpenID] = m.Name
		}
		if m.Feishu.AppID == "" {
			rep.Add(p.Issue("member.feishu.app_id.missing", where, "缺 feishu.app_id"))
		} else if prev, dup := seenAppID[m.Feishu.AppID]; dup {
			rep.Add(p.Issue("member.feishu.app_id.duplicate", where, "feishu.app_id 与 members/%s 重复（app_id 必须全局唯一）", prev))
		} else {
			seenAppID[m.Feishu.AppID] = m.Name
		}
		for _, s := range append([]string{m.Feishu.OpenID}, append(m.Feishu.ExtraAllowFrom, m.Feishu.AllowChat...)...) {
			if s == "*" {
				rep.Add(p.Issue("feishu.wildcard", where, "allow_from / allow_chat 出现 \"*\"（等于对所有人开放，SPEC §6-1）"))
			}
		}
		// 角色相关检查放最后：角色不存在时，成员自身的问题也要一并报出来。
		r, ok := o.Roles[m.Role]
		if !ok {
			rep.Add(p.Issue("member.role.missing", where, "role=%q 在 roles/ 下不存在", m.Role))
			continue
		}
		if r.Mode == "bypassPermissions" && m.Role != "devbot" {
			rep.Add(p.Issue("role.bypass.not_devbot", "roles/"+m.Role+"/persona.md",
				"mode=bypassPermissions 只有 devbot 可以有（SPEC §6-5）"))
		}
		if m.Role != "devbot" && len(r.AllowedTools) == 0 {
			rep.Add(p.Issue("role.allowed_tools.empty", "roles/"+m.Role+"/persona.md",
				"allowed_tools 为空；除 devbot 外应给工具白名单（「空 = 全开」尚未实测，故只告警）"))
		}
	}
	if devbots != 1 {
		rep.Add(p.Issue("company.devbot.count", companyFile, "全公司必须恰好一个启用中的 devbot，当前 %d 个", devbots))
	}
	for _, a := range o.Company.Admins {
		m, ok := o.Member(a)
		if !ok {
			rep.Add(p.Issue("company.admins.unknown", companyFile, "admins 里的 %q 不是任何成员", a))
			continue
		}
		if m.Disabled {
			rep.Add(p.Issue("company.admins.disabled", companyFile, "admins 里的 %q 已停用", a))
		}
	}
	for _, m := range o.Members {
		if _, ok := o.Roles[m.Role]; ok && o.ModelFor(m) == "" {
			rep.Add(p.Issue("member.model.unresolved", "members/"+m.Name,
				"没写 model，role 与 company 也没写，无处回退"))
		}
	}
	return rep
}

// InputsHash 是「重渲染判据」的输入指纹：org 树 + 本机层输入。
func (o *Org) InputsHash(h Host) string {
	var b strings.Builder
	b.WriteString("v3\n") // v3：项目表进入指纹（v2 曾补上 member.domains 与域表）—— 输入代次必须跟着变
	fmt.Fprintf(&b, "host|%s|%s|%s\n", h.VaultRoot, h.HomesRoot, h.DataDir)
	c := o.Company
	fmt.Fprintf(&b, "company|%s|%s|%s|%s|%s|%v|%d|%s|%s|%d|%s\n",
		c.Name, c.ID, c.Language, c.Timezone, c.Platform, c.Admins, c.SyncIntervalMin,
		c.Defaults.Model, c.Defaults.Mode, c.Defaults.AutoCompressMaxTokens, c.Body)
	for _, name := range sortedKeys(o.Roles) {
		r := o.Roles[name]
		fmt.Fprintf(&b, "role|%s|%s|%s|%s|%v|%v|%v|%s|%s|%s|%v\n", r.Role, r.Title, r.Model, r.Mode,
			r.AllowedTools, r.VaultScope, r.Skills, r.Sections["职责"], r.Sections["风格"], r.Sections["术语表"], r.Extra)
	}
	for _, m := range o.Members {
		fmt.Fprintf(&b, "member|%s|%s|%s|%s|%v|%v|%v|%s|%s|%v|%v|%s\n", m.Name, m.DisplayName, m.Role, m.Model,
			m.Admin, m.Disabled, m.Domains, m.Feishu.AppID, m.Feishu.OpenID, m.Feishu.ExtraAllowFrom, m.Feishu.AllowChat, m.Body)
	}
	for _, r := range o.Routing {
		fmt.Fprintf(&b, "routing|%s|%s\n", r.Dir, r.Summary)
	}
	for _, d := range o.Domains {
		fmt.Fprintf(&b, "domain|%s|%s|%s|%s|%s|%s|%s\n", d.Slug, d.Name, d.What, d.Who, d.Data, d.Sources, d.Terms)
	}
	for _, pr := range o.Projects {
		fmt.Fprintf(&b, "project|%s|%s|%s|%s|%s|%s\n",
			pr.Slug, pr.Name, pr.Domain, pr.Owner, pr.Period, pr.Source)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func sortedKeys(m map[string]Role) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RoleKeys 是全部角色 id（已排序）。渲染、看板、报错文案共用这一处 ——
// 免得每多一个消费面，就多一份「怎么排序」的小抄。
func (o *Org) RoleKeys() []string { return sortedKeys(o.Roles) }

func readDoc(path string) (*Doc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("缺文件: %s", path)
		}
		return nil, err
	}
	d, err := Parse(string(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return d, nil
}

func subdirs(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("缺目录: %s", dir)
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// splitH2 把正文按 H2 段全量切开，保留声明顺序。
// 段名合不合规由调用方按规则表定档 —— 这里不丢内容：静默丢掉作者写的段，
// 等于偷偷改了真相源。
func splitH2(body string) []Section {
	idx := reH2.FindAllStringSubmatchIndex(body, -1)
	out := make([]Section, 0, len(idx))
	for i, loc := range idx {
		start := loc[1]
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		out = append(out, Section{Name: body[loc[2]:loc[3]], Text: strings.TrimSpace(body[start:end])})
	}
	return out
}

// scanRouting 扫 vault 顶层数据目录，取目录 CLAUDE.md 的首句作说明。
func scanRouting(root string) ([]Routing, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []Routing
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || skipDirs[e.Name()] {
			continue
		}
		summary := ""
		if b, err := os.ReadFile(filepath.Join(root, e.Name(), "CLAUDE.md")); err == nil {
			for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				summary = strings.TrimLeft(line, "-*0123456789. ")
				break
			}
		}
		out = append(out, Routing{Dir: e.Name(), Summary: summary})
	}
	return out, nil
}

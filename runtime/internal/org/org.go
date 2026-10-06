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

// Role 是 roles/<role>/persona.md。
type Role struct {
	Role         string
	Title        string
	Model        string
	Mode         string
	AllowedTools []string
	VaultScope   []string
	Skills       []string
	Sections     map[string]string // 只允许 职责 / 风格 / 术语表
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
	Root    string
	Company Company
	Roles   map[string]Role
	Members []Member // 按 Name 排序
	Routing []Routing
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
		if r.Sections, err = splitH2(d.Body, []string{"职责", "风格", "术语表"}); err != nil {
			return nil, fmt.Errorf("角色 %s: %w", name, err)
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
	if err := o.Validate(); err != nil {
		return nil, err
	}
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

// Validate 校验全量口径。一次返回所有问题，方便一次改完。
func (o *Org) Validate() error {
	var errs []string
	bad := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	if o.Company.Name == "" {
		bad("company/company.md: 缺 name")
	}
	if !reID.MatchString(o.Company.ID) {
		bad("company/company.md: id=%q 必须是 ASCII 前缀（^[a-z][a-z0-9-]*$），它会进 launchd label 与项目名前缀", o.Company.ID)
	}
	if o.Company.Platform != "feishu" {
		bad("company/company.md: platform=%q，v1 只支持 feishu", o.Company.Platform)
	}
	if o.Company.Defaults.Model == "" {
		bad("company/company.md: 缺 defaults.model（成员与角色都没写 model 时没有回退值）")
	}
	if o.Company.Defaults.Mode == "bypassPermissions" {
		bad("company/company.md: defaults.mode 不许是 bypassPermissions（角色 bot 一律不授予 bypass，SPEC §6-5，无豁免开关）")
	}

	var devbots int
	seenAppID := map[string]string{}
	seenName := map[string]bool{}
	seenOpenID := map[string]string{}
	for _, m := range o.Members {
		where := "members/" + m.Name + "/persona.md"
		if seenName[m.Name] {
			bad("%s: 成员 id 重复", where)
		}
		seenName[m.Name] = true
		if m.DisplayName == "" {
			bad("%s: 缺 display_name", where)
		}
		if m.Role == "devbot" && !m.Disabled {
			devbots++
		}
		if m.Feishu.OpenID == "" {
			bad("%s: 缺 feishu.open_id（渲染 allow_from 用；发 /whoami 给 bot 可取）", where)
		} else if !reOpenID.MatchString(m.Feishu.OpenID) {
			bad("%s: feishu.open_id=%q 不符合 ^ou_", where, m.Feishu.OpenID)
		} else if prev, dup := seenOpenID[m.Feishu.OpenID]; dup {
			bad("%s: feishu.open_id 与 members/%s 重复", where, prev)
		} else {
			seenOpenID[m.Feishu.OpenID] = m.Name
		}
		if m.Feishu.AppID == "" {
			bad("%s: 缺 feishu.app_id", where)
		} else if prev, dup := seenAppID[m.Feishu.AppID]; dup {
			bad("%s: feishu.app_id 与 members/%s 重复（app_id 必须全局唯一）", where, prev)
		} else {
			seenAppID[m.Feishu.AppID] = m.Name
		}
		for _, s := range append([]string{m.Feishu.OpenID}, append(m.Feishu.ExtraAllowFrom, m.Feishu.AllowChat...)...) {
			if s == "*" {
				bad("%s: allow_from / allow_chat 出现 \"*\"（等于对所有人开放，SPEC §6-1）", where)
			}
		}
		// 角色相关检查放最后：角色不存在时，成员自身的问题也要一并报出来。
		r, ok := o.Roles[m.Role]
		if !ok {
			bad("%s: role=%q 在 roles/ 下不存在", where, m.Role)
			continue
		}
		if r.Mode == "bypassPermissions" && m.Role != "devbot" {
			bad("roles/%s/persona.md: mode=bypassPermissions 只有 devbot 可以有（SPEC §6-5）", m.Role)
		}
		if m.Role != "devbot" && len(r.AllowedTools) == 0 {
			bad("roles/%s/persona.md: allowed_tools 为空；除 devbot 外必须给工具白名单（空 = 全开）", m.Role)
		}
	}
	if devbots != 1 {
		bad("全公司必须恰好一个启用中的 devbot，当前 %d 个", devbots)
	}
	for _, a := range o.Company.Admins {
		m, ok := o.Member(a)
		if !ok {
			bad("company/company.md: admins 里的 %q 不是任何成员", a)
			continue
		}
		if m.Disabled {
			bad("company/company.md: admins 里的 %q 已停用", a)
		}
	}
	for _, m := range o.Members {
		if _, ok := o.Roles[m.Role]; ok && o.ModelFor(m) == "" {
			bad("members/%s: 没写 model，role 与 company 也没写，无处回退", m.Name)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("org 校验未通过（%d 项）:\n  - %s", len(errs), strings.Join(errs, "\n  - "))
	}
	return nil
}

// InputsHash 是「重渲染判据」的输入指纹：org 树 + 本机层输入。
func (o *Org) InputsHash(h Host) string {
	var b strings.Builder
	b.WriteString("v1\n")
	fmt.Fprintf(&b, "host|%s|%s|%s\n", h.VaultRoot, h.HomesRoot, h.DataDir)
	c := o.Company
	fmt.Fprintf(&b, "company|%s|%s|%s|%s|%s|%v|%d|%s|%s|%d|%s\n",
		c.Name, c.ID, c.Language, c.Timezone, c.Platform, c.Admins, c.SyncIntervalMin,
		c.Defaults.Model, c.Defaults.Mode, c.Defaults.AutoCompressMaxTokens, c.Body)
	for _, name := range sortedKeys(o.Roles) {
		r := o.Roles[name]
		fmt.Fprintf(&b, "role|%s|%s|%s|%s|%v|%v|%v|%s|%s|%s\n", r.Role, r.Title, r.Model, r.Mode,
			r.AllowedTools, r.VaultScope, r.Skills, r.Sections["职责"], r.Sections["风格"], r.Sections["术语表"])
	}
	for _, m := range o.Members {
		fmt.Fprintf(&b, "member|%s|%s|%s|%s|%v|%v|%s|%s|%v|%v|%s\n", m.Name, m.DisplayName, m.Role, m.Model,
			m.Admin, m.Disabled, m.Feishu.AppID, m.Feishu.OpenID, m.Feishu.ExtraAllowFrom, m.Feishu.AllowChat, m.Body)
	}
	for _, r := range o.Routing {
		fmt.Fprintf(&b, "routing|%s|%s\n", r.Dir, r.Summary)
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

// splitH2 把正文按 H2 段切开，只接受白名单里的段名（防止角色层私藏事实）。
func splitH2(body string, allowed []string) (map[string]string, error) {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	idx := reH2.FindAllStringSubmatchIndex(body, -1)
	out := map[string]string{}
	for i, loc := range idx {
		name := body[loc[2]:loc[3]]
		if !ok[name] {
			return nil, fmt.Errorf("正文出现未约定的 H2 段「%s」；只允许 %s —— 角色层不许私藏事实（canonical registry 纪律）",
				name, strings.Join(allowed, " / "))
		}
		start := loc[1]
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		out[name] = strings.TrimSpace(body[start:end])
	}
	return out, nil
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

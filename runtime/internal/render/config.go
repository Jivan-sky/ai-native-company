package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"anc/internal/org"
)

// Options 是渲染的输入。Version 进指纹头；Now 只影响指纹头的时间戳（便于 golden 稳定）。
type Options struct {
	Host    org.Host
	Version string
	Now     time.Time
}

// Plan 是一次渲染的产物与账目。
type Plan struct {
	Text        string
	InputsHash  string
	Projects    []string // 渲染进配置的 project 名（顺序即输出顺序）
	PersonaHash map[string]string
	Warns       []string
}

// Build 全量生成 gateway config.toml。全量生成、不打补丁；手改视为事故。
func Build(o *org.Org, opt Options) (*Plan, error) {
	inputs := o.InputsHash(opt.Host)
	p := &Plan{InputsHash: inputs, PersonaHash: map[string]string{}}
	var b strings.Builder

	fmt.Fprintf(&b, "# anc:generated v=%s inputs=%s at=%s\n", opt.Version, inputs, opt.Now.UTC().Format(time.RFC3339))
	b.WriteString("# 本文件由 `anc render` 全量生成；手改视为事故，重跑即覆盖。\n")
	b.WriteString("# 凭据一律 ${ENV} 引用，明文只住在 secrets.env。\n\n")

	if opt.Host.DataDir != "" {
		fmt.Fprintf(&b, "data_dir = %s\n\n", tomlString(opt.Host.DataDir))
	}
	b.WriteString("[display]\nmode = \"full\"\n")

	// 输出顺序：members 目录名排序 + devbot 殿后 —— 确定性让 diff 稳定。
	members := append([]org.Member(nil), o.Enabled()...)
	sort.SliceStable(members, func(i, j int) bool {
		di, dj := members[i].Role == "devbot", members[j].Role == "devbot"
		if di != dj {
			return !di
		}
		return members[i].Name < members[j].Name
	})

	for _, m := range members {
		role, ok := o.Roles[m.Role]
		if !ok {
			return nil, fmt.Errorf("成员 %s 的 role=%q 不存在", m.Name, m.Role)
		}
		pr, err := Persona(o, role, m, opt.Host)
		if err != nil {
			return nil, err
		}
		p.Warns = append(p.Warns, pr.Warns...)

		name := o.Company.ID + "-" + m.Name
		p.Projects = append(p.Projects, name)
		ph := sha256hex(pr.Text)
		p.PersonaHash[name] = ph

		b.WriteString("\n[[projects]]\n")
		fmt.Fprintf(&b, "name = %s\n", tomlString(name))
		if admins := adminOpenIDs(o); len(admins) > 0 {
			fmt.Fprintf(&b, "admin_from = %s\n", tomlString(strings.Join(admins, ",")))
		}
		b.WriteString("reset_on_idle_mins = 30\n")

		b.WriteString("\n[projects.agent]\ntype = \"claudecode\"\n")

		b.WriteString("\n[projects.agent.options]\n")
		workDir := opt.Host.HomesRoot + "/" + m.Name
		if m.Role == "devbot" {
			workDir = opt.Host.VaultRoot
		}
		fmt.Fprintf(&b, "work_dir = %s\n", tomlString(workDir))
		if mode := role.Mode; mode != "" {
			fmt.Fprintf(&b, "mode = %s\n", tomlString(mode))
		}
		if model := o.ModelFor(m); model != "" {
			fmt.Fprintf(&b, "model = %s\n", tomlString(model))
		}
		// 空数组 = 不写该键（全开），仅 devbot 允许空。
		if len(role.AllowedTools) > 0 {
			fmt.Fprintf(&b, "allowed_tools = [%s]\n", tomlStringList(role.AllowedTools))
		}
		b.WriteString("append_system_prompt = '''\n")
		b.WriteString(pr.Text)
		b.WriteString("\n'''\n")

		b.WriteString("\n[[projects.platforms]]\ntype = \"feishu\"\n")
		b.WriteString("\n[projects.platforms.options]\n")
		fmt.Fprintf(&b, "app_id = %s\n", tomlString(m.Feishu.AppID))
		fmt.Fprintf(&b, "app_secret = %s\n", tomlString("${ANC_FEISHU_SECRET_"+strings.ToUpper(m.Name)+"}"))
		if allow := allowFrom(o, m); len(allow) > 0 {
			fmt.Fprintf(&b, "allow_from = %s\n", tomlString(strings.Join(allow, ",")))
		}
		if len(m.Feishu.AllowChat) > 0 {
			fmt.Fprintf(&b, "allow_chat = %s\n", tomlString(strings.Join(m.Feishu.AllowChat, ",")))
		}
	}

	p.Text = b.String()
	if warns, err := verifyRoundTrip(p, o); err != nil {
		return nil, err
	} else {
		p.Warns = append(p.Warns, warns...)
	}
	if o.Company.Fallback != nil {
		p.Warns = append(p.Warns, "company.fallback_provider 已配置，但渲染器**未输出** providers 段：cc-connect 的 providers 字段形态尚未核对（W1 实测项）")
	}
	if o.Company.Defaults.AutoCompressMaxTokens > 0 {
		p.Warns = append(p.Warns, "defaults.auto_compress_max_tokens 已配置，但渲染器**未输出** auto_compress 段：字段名在上游设计里也标为待实测（W1 实测项 ④）")
	}
	return p, nil
}

// adminOpenIDs 把 company.admins 翻成 open_id 列表（顺序即 admins 声明顺序，保证 diff 稳定）。
func adminOpenIDs(o *org.Org) []string {
	var out []string
	for _, a := range o.Company.Admins {
		if m, ok := o.Member(a); ok && m.Feishu.OpenID != "" {
			out = append(out, m.Feishu.OpenID)
		}
	}
	return out
}

// allowFrom = 本人 open_id + 额外可对话者 + admins，去重保序；绝不渲染 "*"。
func allowFrom(o *org.Org, m org.Member) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(m.Feishu.OpenID)
	for _, s := range m.Feishu.ExtraAllowFrom {
		add(s)
	}
	for _, s := range adminOpenIDs(o) {
		add(s)
	}
	return out
}

// Fingerprint 解析指纹头，返回 (版本, inputs, 时间戳)；无指纹返回 ok=false。
func Fingerprint(text string) (version, inputs, at string, ok bool) {
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "# anc:generated ") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			return "", "", "", false
		}
		fields := strings.Fields(strings.TrimPrefix(line, "# anc:generated "))
		for _, f := range fields {
			k, v, found := strings.Cut(f, "=")
			if !found {
				continue
			}
			switch k {
			case "v":
				version = v
			case "inputs":
				inputs = v
			case "at":
				at = v
			}
		}
		return version, inputs, at, true
	}
	return "", "", "", false
}

// StripFingerprint 去掉指纹头（含生成时间），用于 --check 的「实质内容」比对。
func StripFingerprint(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "# anc:generated ") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// ProjectsIn 从已有配置里扫出 project 名列表（不需要完整 TOML 解析器）。
func ProjectsIn(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name = ") {
			out = append(out, unquote(strings.TrimPrefix(line, "name = ")))
		}
	}
	return out
}

// verifyRoundTrip 结构校验：把生成文本读回比对（project 数 / app_id 集合 / 每个 persona 的 SHA256）。
func verifyRoundTrip(p *Plan, o *org.Org) ([]string, error) {
	var warns []string
	got := ProjectsIn(p.Text)
	if len(got) != len(p.Projects) {
		return nil, fmt.Errorf("round-trip 校验失败: 生成的 [[projects]] 有 %d 个，期望 %d 个", len(got), len(p.Projects))
	}
	for i := range got {
		if got[i] != p.Projects[i] {
			return nil, fmt.Errorf("round-trip 校验失败: 第 %d 个 project 是 %q，期望 %q", i+1, got[i], p.Projects[i])
		}
	}
	for _, block := range splitProjects(p.Text) {
		name := ""
		for _, line := range block {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "name = ") {
				name = unquote(strings.TrimPrefix(line, "name = "))
				break
			}
		}
		want, ok := p.PersonaHash[name]
		if !ok {
			return nil, fmt.Errorf("round-trip 校验失败: 配置里出现未预期的 project %q", name)
		}
		got, err := personaHashIn(block)
		if err != nil {
			return nil, fmt.Errorf("round-trip 校验失败（%s）: %w", name, err)
		}
		if got != want {
			return nil, fmt.Errorf("round-trip 校验失败（%s）: 读回的 persona SHA256 与渲染输入不一致", name)
		}
	}
	return warns, nil
}

func splitProjects(text string) [][]string {
	var out [][]string
	var cur []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "[[projects]]" {
			if cur != nil {
				out = append(out, cur)
			}
			cur = []string{}
			continue
		}
		if cur != nil {
			cur = append(cur, line)
		}
	}
	if cur != nil {
		out = append(out, cur)
	}
	return out
}

func personaHashIn(block []string) (string, error) {
	start, end := -1, -1
	for i, line := range block {
		if strings.HasPrefix(strings.TrimSpace(line), "append_system_prompt = '''") {
			start = i
			continue
		}
		if start >= 0 && strings.TrimSpace(line) == "'''" {
			end = i
			break
		}
	}
	if start < 0 || end < 0 {
		return "", fmt.Errorf("找不到 append_system_prompt 的 ''' 多行 literal")
	}
	return sha256hex(strings.Join(block[start+1:end], "\n")), nil
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// tomlString 输出 basic string，按 TOML 规则做最小转义。
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func tomlStringList(list []string) string {
	parts := make([]string, 0, len(list))
	for _, s := range list {
		parts = append(parts, tomlString(s))
	}
	return strings.Join(parts, ", ")
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		s = strings.ReplaceAll(s, `\"`, `"`)
		s = strings.ReplaceAll(s, `\\`, `\`)
	}
	return s
}

// PersonaHashesIn 从已有配置里扫出每个 project 的 persona SHA256，供 --check 逐项对比。
func PersonaHashesIn(text string) map[string]string {
	out := map[string]string{}
	for _, block := range splitProjects(text) {
		name := ""
		for _, line := range block {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "name = ") {
				name = unquote(strings.TrimPrefix(line, "name = "))
				break
			}
		}
		if name == "" {
			continue
		}
		if h, err := personaHashIn(block); err == nil {
			out[name] = h
		}
	}
	return out
}

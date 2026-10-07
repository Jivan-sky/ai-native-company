package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
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

// ProjectName 是一个成员在 gateway 配置里的 project 名（= `[[projects]] name`）。
//
// 规则只有这一处：任何要显示「同一个 bot」的地方都得走它 —— 各写各的，迟早
// 出现「看板叫 alice、日志叫 demo-alice」这种对不上号的现场。
func ProjectName(companyID, member string) string { return companyID + "-" + member }

// Plan 是一次渲染的产物与账目。
type Plan struct {
	Text        string
	InputsHash  string
	Projects    []string // 渲染进配置的 project 名（顺序即输出顺序）
	PersonaHash map[string]string
	Issues      []org.Issue // 规则表定的校验发现（warn 档必须回显）
	Warns       []string    // 不由规则表管的散装提示（例如未实测字段）
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
	writeDisplaySection(&b, o.Company.DisplayMode())
	writeRelaySection(&b)

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
		p.Issues = append(p.Issues, pr.Issues...)

		name := ProjectName(o.Company.ID, m.Name)
		p.Projects = append(p.Projects, name)
		ph := sha256hex(pr.Text)
		p.PersonaHash[name] = ph

		b.WriteString("\n[[projects]]\n")
		fmt.Fprintf(&b, "name = %s\n", tomlString(name))
		if admins := adminOpenIDs(o); len(admins) > 0 {
			fmt.Fprintf(&b, "admin_from = %s\n", tomlString(strings.Join(admins, ",")))
		}
		// 空闲重置：**0 = 关掉**（2026-10-07 拍板）—— 换新会话由人显式发 /new，不靠计时器猜。
		fmt.Fprintf(&b, "reset_on_idle_mins = %d\n", o.Company.Defaults.ResetOnIdleMins)

		b.WriteString("\n[projects.agent]\ntype = \"claudecode\"\n")

		b.WriteString("\n[projects.agent.options]\n")
		workDir := WorkDir(m, opt.Host)
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
		p.Warns = append(p.Warns, "company.fallback_provider 已配置，但渲染器未输出 providers 段：cc-connect 的 providers 字段形态尚未核对（W1 实测项）")
	}
	if o.Company.Defaults.AutoCompressMaxTokens > 0 {
		p.Warns = append(p.Warns, "defaults.auto_compress_max_tokens 已配置，但渲染器未输出 auto_compress 段：字段名在上游设计里也标为待实测（W1 实测项 ④）")
	}
	return p, nil
}

// writeDisplaySection 出 [display] 段。
//
// 口径：**聊天窗是给人看的观测面，不是 agent 的工作日志。**
// cc-connect 的 full 会把「思考」和每一次「工具调用」各发一条消息出去，
// 窗口被过程塞满之后，人反而看不清结论是什么 —— 所以出厂默认 quiet。
//
// 三档各自的**连带项一起定死在这里**，不做「模式 × 开关」的组合：
//
//	quiet / compact —— 只出结果，连 footer 一起关。
//	                   footer 第二行会把 work_dir（本机绝对路径）推进 IM，
//	                   与 SPEC §2.3「观测面不泄漏本机布局」相冲。
//	full            —— 排查用，全开（思考 + 工具 + footer），要看就得连着看。
//
// 要过程就在真相源 company.md 里写 display: full，别手改这个文件。
func writeDisplaySection(b *strings.Builder, mode string) {
	b.WriteString("# display：聊天窗是给人看的观测面，不是 agent 的工作日志。\n")
	b.WriteString("# 改这里会被 `anc render` 覆盖 —— 要调档位请在 company/company.md 写 display。\n")
	fmt.Fprintf(b, "[display]\nmode = %s\n", tomlString(mode))
	if mode == org.DisplayFull {
		b.WriteString("reply_footer = true\n")
		return
	}
	b.WriteString("reply_footer = false\n")
}

// WorkDir 是这个 bot 的 cwd：成员各自的家目录，devbot 是仓库本体（改坏能回滚）。
// 服务单元里也用它 —— 单元的工作目录必须和配置里写的 work_dir 是同一个地方，
// 不然 bot 起来读的是另一个目录里的 AGENTS.md。
func WorkDir(m org.Member, host org.Host) string {
	if m.Role == "devbot" {
		return host.VaultRoot
	}
	return host.HomesRoot + "/" + m.Name
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

// relayTimeoutSecs / relayVisibility 是 v1 的 bot 间通道口径：机制保留、默认零绑定。
// 依据 SPEC §6 授权模型 —— 通道这个「能力」默认存在，通道里的「数据」默认不通；
// 要通必须在真相源里显式开一条授权，而不是靠上游的默认值。
const (
	relayTimeoutSecs = 0
	relayVisibility  = "summary"
)

// writeRelaySection 显式输出 bot 间通道声明。
//
// 上游 [relay] 段带默认值，且默认是「开着」（timeout_secs = 120）。产物里不写这一段，
// 就等于默认放行 bot 间通道 —— 破一个 bot 就能问另一个 bot，与 SPEC §6 不变量 3 直接冲突。
// 所以这里恒定显式输出：不依赖上游默认，是渲染器的责任。
func writeRelaySection(b *strings.Builder) {
	b.WriteString("\n# 安全相关段必须完全显式：上游 relay 默认 timeout_secs=120（通道开着），不写 = 静默放行。\n")
	b.WriteString("# 口径：通道机制保留、默认零绑定；要通必须在真相源里显式开（SPEC §6 授权模型）。\n")
	b.WriteString("[relay]\n")
	fmt.Fprintf(b, "timeout_secs = %d\n", relayTimeoutSecs)
	fmt.Fprintf(b, "visibility = %s\n", tomlString(relayVisibility))
}

// RelayIn 从产物里读回 [relay] 段的 timeout_secs。
// ok=false 表示这一段根本没被显式写出来 —— 那就是在沿用上游默认值。
func RelayIn(text string) (timeoutSecs int, ok bool) {
	inRelay := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			inRelay = t == "[relay]"
			continue
		}
		if !inRelay || strings.HasPrefix(t, "#") {
			continue
		}
		k, v, found := strings.Cut(t, "=")
		if !found || strings.TrimSpace(k) != "timeout_secs" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// SecurityGaps 报告产物里安全相关的缺口。
//
// 判据（SPEC §6）：上游对安全相关的段有默认值，而那个默认值我们不知道也管不了 ——
// 产物里缺一段，就等于静默接受它的默认。所以缺口有两类，都要报：
//
//	① 没写（沿用上游默认 120 = 通道开着）；
//	② 写了但值不是 v1 口径（= 通道被打开）。
//
// ② 这一条是对「手改产物」的兜底：--check 的指纹比对只看 persona 与 inputs，
// 手改一个 timeout_secs 不会动指纹，只有这条能看见它。
func SecurityGaps(text string) []string {
	var gaps []string
	n, ok := RelayIn(text)
	switch {
	case !ok:
		gaps = append(gaps, "[relay] 未显式声明：上游默认 timeout_secs=120（bot 间通道开着），不写 = 静默放行")
	case n != relayTimeoutSecs:
		gaps = append(gaps, fmt.Sprintf("[relay] timeout_secs = %d，不是 v1 口径的 %d：bot 间通道被打开了；要通必须走授权模型（SPEC §6）", n, relayTimeoutSecs))
	}
	return gaps
}

// AssertSecurityExplicit 是 SecurityGaps 的 error 形态，供渲染器自校验用。
func AssertSecurityExplicit(text string) error {
	if g := SecurityGaps(text); len(g) > 0 {
		return fmt.Errorf("产物缺少显式声明的安全段（%d 项）:\n  - %s", len(g), strings.Join(g, "\n  - "))
	}
	return nil
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

// WorkDirs 从配置里扫出「project → work_dir」（agent 的工作目录）。
//
// trail 拿它把 cc-connect 的 project 对上 harness 的原生记录目录：目录名 = work_dir
// 里每个非字母数字字符换成 '-'（见 internal/trail.Slug，那是实测出来的规则）。
// 这里同样只做行扫，不引 TOML 解析器 —— 这份配置是我们自己生成的，形状是已知的。
func WorkDirs(text string) map[string]string {
	out := map[string]string{}
	project := ""
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "[["):
			project = "" // 新的 [[projects]] 元素开始；只认它自己那一份的 work_dir
		case strings.HasPrefix(t, "name = "):
			project = unquote(strings.TrimPrefix(t, "name = "))
		case strings.HasPrefix(t, "work_dir = ") && project != "":
			out[project] = unquote(strings.TrimPrefix(t, "work_dir = "))
		}
	}
	return out
}

// verifyRoundTrip 结构校验：把生成文本读回比对（project 数 / app_id 集合 / 每个 persona 的 SHA256）。
func verifyRoundTrip(p *Plan, o *org.Org) ([]string, error) {
	var warns []string
	if err := AssertSecurityExplicit(p.Text); err != nil {
		return nil, err
	}
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

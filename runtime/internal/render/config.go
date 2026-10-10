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
// 拼法**只有一个定义处**：`org.ProjectName`（它是 org 模型的事实，不是某个渲染器的口味）。
// 这里留着同名导出，是为了不动既有调用方；要改拼法去 org 那一条。
func ProjectName(companyID, member string) string { return org.ProjectName(companyID, member) }

// DefaultHarness 是「这个 project 没写 harness 时用哪条腿」。
//
// 成员 bot 现在也走它 —— 那条 `type = "claudecode"` 原来写死在 Build 里，
// 现在只有这一处默认值（§4.7 H2/H3：换腿 = 加一张表，不是再加一处写死）。
const DefaultHarness = "claudecode"

// AgentProjectName 是这个**业务 agent** 在 gateway 配置里的 project 名。
//
// 与成员 bot 共用同一条派生规则（`<公司 id>-<slug>`），不另立一套 —— SPEC §4.7 ③
// 「一个业务 agent 就是一个 project」；两套拼法迟早出现「看板叫 bizbot、日志叫 sandbox-bizbot」。
func AgentProjectName(companyID, slug string) string { return ProjectName(companyID, slug) }

// AgentSecretKey 是一个**业务 agent** 的 app_secret 在 secrets.env 里的键名。
//
// 与成员共用同一支派生（secretKeyBody）：一整块牌子只做一次，两边不会漂移。
// 例：`order-bot` → `ANC_FEISHU_SECRET_ORDERx00002dBOT`（'-' 不在直通字符里，就地转义成定长 6 位小写十六进制）。
func AgentSecretKey(slug string) string { return "ANC_FEISHU_SECRET_" + secretKeyBody(slug) }

// UnwiredProjects 是「**声明**了还没接平台凭据」的 project 名。
//
// 两处来源同一条口径：成员上的 `unwired: true`；业务 agent 没写 `app_id`。
// 只算**启用中的**：停用的成员本来就不进 config，多报一个只是噪声。
// 四个消费方（anc probe / anc apply 的回读 / 看板运行态 / 告警）必须用同一个函数算 ——
// 各算各的，就会出现「探针说灰、apply 回读说黄」这种自相矛盾的报告（2026-10-08 实测踩到）。
func UnwiredProjects(o *org.Org) []string {
	var out []string
	for _, m := range o.Enabled() {
		if m.Unwired {
			out = append(out, ProjectName(o.Company.ID, m.Name))
		}
	}
	for _, a := range sortedAgents(o.Agents) {
		if strings.TrimSpace(a.AppID) == "" {
			out = append(out, AgentProjectName(o.Company.ID, a.Slug))
		}
	}
	return out
}

// sortedAgents 按 slug 排 —— 输出顺序确定，diff 才稳（同 members 那一条纪律）。
func sortedAgents(list []org.Agent) []org.Agent {
	out := append([]org.Agent(nil), list...)
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// agentModel 是业务 agent 的模型：自己写了用自己那份，没写走 company 默认。
func agentModel(o *org.Org, a org.Agent) string {
	if m := strings.TrimSpace(a.Model); m != "" {
		return m
	}
	return o.Company.Defaults.Model
}

// agentAllowFrom 是「谁可以和这个业务 agent 说话」：这块业务 `who` 岗位下的成员 + 公司 admins。
//
// 为什么不是「只留 admins 就够」：业务 agent 代的是**岗位**（§2.3），替它办这块业务的人正是
// who 那一岗 —— 只留 admins，等于把它真正的使用者关在门外（10-27 要跑一条真流程，当场就会撞上）。
// 名单从 members/ 现算，与 persona / 看板是同一份派生：**不在这里另存一份名单**。
//
// **绝不渲染 "*"**（同 allowFrom）：一个自己都认不出收件人的 bot，宁可不渲染 ——
// 但这条不是门禁，是渲染期的填不出来：它只让这一个 agent 这一台机器上先没有，
// 且如实报缺（plan.Warns），不拦整份产物。
func agentAllowFrom(o *org.Org, a org.Agent) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	if d, ok := o.Domain(a.Domain); ok {
		for _, m := range o.Enabled() {
			if m.Role == d.Who {
				add(m.Feishu.OpenID)
			}
		}
	}
	for _, s := range adminOpenIDs(o) {
		add(s)
	}
	return out
}

// FeishuSecretKey 是一个成员的 app_secret 在 secrets.env 里的键名。
//
// 规则只有这一处：渲染器按它写引用、体检按它要键、现场按它填凭据 —— 各算各的迟早对不上号。
//
// 这一步必须是**全函数**：键名要写进环境（装载侧只认 [A-Za-z_][A-Za-z0-9_]*），而成员名是人的
// 名字 —— 中文、空格、- 、. 都合法（见 org 规则 member.name.format：它只拦「当不了目录名」的字符）。
// 所以：纯 ASCII 字母数字下划线 → 直接大写，**存量键一个字节都不变**；出现别的字符 → 整个名字
// 按 rune 转义（x + 定长 6 位**小写**十六进制）。
//
// 两段按构造不相交：直通段只产大写 [A-Z0-9_]，转义段必含小写 x。所以不同的名不会撞成同一个键；
// 而「白 + 5」与单字 U+767D5 这类也不会撞 —— 转义段是定长的。
//
// 例：alice → ANC_FEISHU_SECRET_ALICE；alice-2 → ANC_FEISHU_SECRET_ALICEx00002d2。
func FeishuSecretKey(member string) string { return "ANC_FEISHU_SECRET_" + secretKeyBody(member) }

// plainKeyChars 是键名**直通段**允许的字符：ASCII 字母 / 数字 / 下划线。
// 非 ASCII 的字母不算直通 —— 环境变量名是 ASCII 的世界。
const plainKeyChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_"

func isPlainKeyRune(r rune) bool { return r < 128 && strings.IndexRune(plainKeyChars, r) >= 0 }

// secretKeyBody 是 FeishuSecretKey 去掉前缀的那一段：能直通的直通（大写），其余按 rune 转义。
// 判据与转义规则只在 FeishuSecretKey 那一处注释里 —— 别在这儿另立一套。
func secretKeyBody(name string) string {
	plain := true
	for _, r := range name {
		if !isPlainKeyRune(r) {
			plain = false
			break
		}
	}
	if plain {
		return strings.ToUpper(name)
	}
	var b strings.Builder
	for _, r := range name {
		if isPlainKeyRune(r) {
			b.WriteString(strings.ToUpper(string(r)))
			continue
		}
		fmt.Fprintf(&b, "x%06x", r)
	}
	return b.String()
}

// Plan 是一次渲染的产物与账目。
type Plan struct {
	Text        string
	InputsHash  string
	Projects    []string // 渲染进配置的 project 名（顺序即输出顺序）
	PersonaHash map[string]string
	Issues      []org.Issue // 规则表定的校验发现（warn 档必须回显）
	Warns       []string    // 不由规则表管的散装提示（例如未实测字段）

	// SecretKeys 是这份产物**要求**的凭据键（顺序即 project 顺序），装载前的体检吃它。
	//
	// 它是账，不是回读：键名在写 app_secret 那一行当场记下。为什么不用正则回读产物文本 ——
	// 2026-10-08 之前键名就是「成员名大写」，中文名拼出的键**回读不到**（那把正则只认 ASCII），
	// 于是体检看着产物却看不见这个键、报「✅ 齐」，现场等来的却是一个没有凭据、静默起不来的 bot。
	// 现在键名是全函数派生（见 FeishuSecretKey），对任何名字都写得出合法环境变量名 ——
	// 回读与这份账**对任何名字都该一致**，回读只在用例里当交叉校验（不一致 = 派生漏了字符）。
	// 键名唯一跟着成员名唯一走（member.name.duplicate 是红档），与 Projects 一一对应。
	SecretKeys []string
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

		fmt.Fprintf(&b, "\n[projects.agent]\ntype = %s\n", tomlString(DefaultHarness))

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
		key := FeishuSecretKey(m.Name)
		p.SecretKeys = append(p.SecretKeys, key)
		fmt.Fprintf(&b, "app_secret = %s\n", tomlString("${"+key+"}"))
		if allow := allowFrom(o, m); len(allow) > 0 {
			fmt.Fprintf(&b, "allow_from = %s\n", tomlString(strings.Join(allow, ",")))
		}
		if len(m.Feishu.AllowChat) > 0 {
			fmt.Fprintf(&b, "allow_chat = %s\n", tomlString(strings.Join(m.Feishu.AllowChat, ",")))
		}
	}

	// 业务 agent：一个 agent 一个 project（SPEC §4.7 ③），与成员 bot 共用同一套机制。
	// 三处**刻意**的差别，写在各自该写的地方：
	//   1. 没有「人写的 persona」那一段 —— FDE 的东西住在它的 cwd 里（见 AgentPersona）；
	//   2. 收件人不是「一个人」，而是这块业务 who 岗位下的成员 + admins（见 agentAllowFrom）；
	//   3. 不发 admin_from —— 业务 agent 不是给人下命令用的，少一个入口就少一类事故。
	//
	// 三种「这一台机器上先不渲染它」都**如实报缺**（plan.Warns），不猜、不静默跳过：
	// role 取不到、本机 cwd 没配、解不出收件人。
	for _, a := range sortedAgents(o.Agents) {
		role, ok := o.Roles[a.Role]
		if !ok {
			p.Warns = append(p.Warns, fmt.Sprintf(
				"业务 agent %s 的 role=%q 在 roles/ 里找不到 —— 这一台机器上不渲染它（persona 取不到职责那一段）",
				a.Slug, a.Role))
			continue
		}
		// 本机事实（这台机器上它落在哪）**不许猜**：拿 HomesRoot 拼一个看着像的路径，
		// 会在别人家目录里建夹子（同 org.Host.AgentHome 的注释）。
		workDir, ok := opt.Host.AgentHome(a.Slug)
		if !ok {
			p.Warns = append(p.Warns, fmt.Sprintf(
				"业务 agent %s 在本机没有 cwd —— 这一台机器上不渲染它（配 --agent-home %s=<目录> 才落）", a.Slug, a.Slug))
			continue
		}
		allow := agentAllowFrom(o, a)
		if len(allow) == 0 {
			p.Warns = append(p.Warns, fmt.Sprintf(
				"业务 agent %s 解不出收件人（域 %q 的 who 岗位下没有启用中的成员，公司 admins 也没有 open_id）—— 这一台机器上不渲染它",
				a.Slug, a.Domain))
			continue
		}
		pr, err := AgentPersona(o, role, a, opt.Host)
		if err != nil {
			return nil, err
		}
		p.Issues = append(p.Issues, pr.Issues...)

		name := AgentProjectName(o.Company.ID, a.Slug)
		p.Projects = append(p.Projects, name)
		p.PersonaHash[name] = sha256hex(pr.Text)

		b.WriteString("\n[[projects]]\n")
		fmt.Fprintf(&b, "name = %s\n", tomlString(name))
		fmt.Fprintf(&b, "reset_on_idle_mins = %d\n", o.Company.Defaults.ResetOnIdleMins)

		harness := strings.TrimSpace(a.Harness)
		if harness == "" {
			harness = DefaultHarness
		}
		fmt.Fprintf(&b, "\n[projects.agent]\ntype = %s\n", tomlString(harness))

		b.WriteString("\n[projects.agent.options]\n")
		fmt.Fprintf(&b, "work_dir = %s\n", tomlString(workDir))
		if mode := role.Mode; mode != "" {
			fmt.Fprintf(&b, "mode = %s\n", tomlString(mode))
		}
		if model := agentModel(o, a); model != "" {
			fmt.Fprintf(&b, "model = %s\n", tomlString(model))
		}
		// 空的那条在 org 层就是红档（agent.tools.empty）；真被降档放行时，与成员同一条写法：
		// 不写这个键。写一个 `[]` 出去等于替上游猜「空数组 = 全开还是全禁」—— 那次猜错的代价是现场。
		if tools := o.AgentTools(a); len(tools) > 0 {
			fmt.Fprintf(&b, "allowed_tools = [%s]\n", tomlStringList(tools))
		}
		b.WriteString("append_system_prompt = '''\n")
		b.WriteString(pr.Text)
		b.WriteString("\n'''\n")

		b.WriteString("\n[[projects.platforms]]\ntype = \"feishu\"\n")
		b.WriteString("\n[projects.platforms.options]\n")
		fmt.Fprintf(&b, "app_id = %s\n", tomlString(a.AppID))
		key := AgentSecretKey(a.Slug)
		p.SecretKeys = append(p.SecretKeys, key)
		fmt.Fprintf(&b, "app_secret = %s\n", tomlString("${"+key+"}"))
		fmt.Fprintf(&b, "allow_from = %s\n", tomlString(strings.Join(allow, ",")))
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

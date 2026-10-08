package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"anc/internal/envelope"
	"anc/internal/mcp"
	"anc/internal/org"
)

const envelopeUsage = `anc envelope —— 接入面：递进来（信封）+ 要出去（读出口）

出站读出口的用法在 serve 里 —— 它挂在接入面的 MCP 服务端上，工具名 anc_read_context：
只读、按可见范围过滤（自己的域全行 / 别人的域只给「找谁」）、每次调用留一条痕进 audit。

用法：
  anc envelope check <vault 目录> <信封.json> [--json]
  anc envelope serve <vault 目录> [--addr 127.0.0.1:8791] [--data <data 目录>]
                    起接入面：harness 通过 MCP（streamable HTTP）调 anc_send_envelope 把信封递进来

它答两件事：
  1 结构   读不读得懂（缺 id / ts 不是 RFC3339 / 缺 who → 拒收，退出码 1）
  2 绑定   who / on_behalf_of / scope 指向的东西在真相源里**到底存不存在**
           发现走生效规则表的档位：默认 warn（只回显，不拦）；提红 / 关掉都写在
           company.md 的 policy 段 —— 门禁是数据，不是代码里的 if

顺带把判据那六问摊出来：谁 / 代谁 / 哪块业务 / 要什么 / 证据在哪 / 要不要人拍。

它**不**做：不发、不写、不拦。想看待生效的档位：anc org check <vault> --rules。
`

// envelopeFinding 是发现对外的形状（与 board.Finding 同形）：规则 id / 档位 / 定位 / 文案。
type envelopeFinding struct {
	Rule  string `json:"rule"`
	Level string `json:"level"`
	Where string `json:"where"`
	Msg   string `json:"msg"`
}

type envelopeCheckOut struct {
	OK       bool              `json:"ok"`
	Envelope envelope.Envelope `json:"envelope"`
	Brief    []envelope.Line   `json:"brief"`
	Fatal    []envelopeFinding `json:"fatal"`
	Warn     []envelopeFinding `json:"warn"`
}

func cmdEnvelope(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, envelopeUsage)
		return 2
	}
	switch args[0] {
	case "check":
		return cmdEnvelopeCheck(args[1:])
	case "serve":
		return cmdEnvelopeServe(args[1:])
	}
	fmt.Fprintf(os.Stderr, "错误：不认识子命令 %q\n\n", args[0])
	fmt.Fprint(os.Stderr, envelopeUsage)
	return 2
}

func cmdEnvelopeCheck(args []string) int {
	fs := flag.NewFlagSet("envelope check", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "出 JSON（默认人读的六问六答）")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 2 {
		fmt.Fprint(os.Stderr, envelopeUsage)
		return 2
	}
	vault, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 2
	}
	raw, err := os.ReadFile(posArgs[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：读不了信封文件：%v\n", err)
		return 1
	}
	e, err := envelope.Parse(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "信封读不懂：%v\n", err)
		return 1
	}
	o, err := org.Load(vault)
	if err != nil {
		// 真相源本身就是红的：绑不了。不编一份绿灯，直接说清去哪儿修。
		fmt.Fprintf(os.Stderr, "真相源加载失败：%v\n先修 org：anc org check %s\n", err, vault)
		return 1
	}
	issues := envelope.Bind(o, e, o.Policy)
	fatal, warn := splitIssues(issues)

	if *asJSON {
		out := envelopeCheckOut{
			OK: len(fatal) == 0, Envelope: e, Brief: e.Brief(),
			Fatal: toEnvelopeFindings(fatal), Warn: toEnvelopeFindings(warn),
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：序列化失败：%v\n", err)
			return 1
		}
		fmt.Println(string(b))
	} else {
		printEnvelopeCheck(e, fatal, warn)
	}
	if len(fatal) > 0 {
		return 1
	}
	return 0
}

func printEnvelopeCheck(e envelope.Envelope, fatal, warn []org.Issue) {
	fmt.Printf("信封 %s  ·  %s\n\n", e.ID, e.TS)
	for _, l := range e.Brief() {
		fmt.Printf("  %s %s\n", padRight(l.Q, 14), l.A)
	}
	fmt.Println()
	if len(fatal) == 0 && len(warn) == 0 {
		fmt.Println("绑定：0 项发现（生效档位看 `anc org check <vault> --rules`）")
		return
	}
	fmt.Printf("绑定：%d 项红 / %d 项非红档发现\n", len(fatal), len(warn))
	for _, i := range fatal {
		fmt.Printf("  🔴 [%s] %s\n", i.Rule, i.Msg)
	}
	for _, i := range warn {
		fmt.Printf("  🟡 [%s] %s\n", i.Rule, i.Msg)
	}
	if len(fatal) == 0 {
		fmt.Println("  （全是非红档：不拦，但请过目 —— 想提红就写进 company.md 的 policy 段）")
	}
}

func splitIssues(issues []org.Issue) (fatal, warn []org.Issue) {
	for _, i := range issues {
		if i.Level == org.LevelFatal {
			fatal = append(fatal, i)
		} else {
			warn = append(warn, i)
		}
	}
	return fatal, warn
}

func toEnvelopeFindings(in []org.Issue) []envelopeFinding {
	out := make([]envelopeFinding, 0, len(in))
	for _, i := range in {
		out = append(out, envelopeFinding{Rule: i.Rule, Level: string(i.Level), Where: i.Where, Msg: i.Msg})
	}
	return out
}

// ---------- 接入面：MCP 入口（anc envelope serve） ----------

const envelopeServeUsage = `anc envelope serve —— 起 ANC 的接入面（MCP，streamable HTTP）

用法：
  anc envelope serve <vault 目录> [--addr 127.0.0.1:8791] [--data <data 目录>]

harness（Claude Code / Codex / …）通过 MCP 调工具。桥的两半都在这一个入口上：

  入站（agent 递给 ANC）  anc_send_envelope(who, on_behalf_of?, kind?, body, scope_domain?, scope_project?, refs?, needs?)
  出站（agent 向 ANC 要）  anc_read_context(who, on_behalf_of?, domain?)

入站服务端做的事，一件不多：
  1 读工具参数 → 拼成一份信封 JSON → **走和线上同一条 Parse**（不给工具路径单开一套校验）
  2 绑真相源（Bind）：who / on_behalf_of / scope 指的东西存不存在
  3 落运行态日志：<data>/envelope/<YYYY-MM>.<who>.jsonl（按 who 分片，append-only）
  4 把「六问六答 + 发现」原样回给 harness

出站（W2 · 只读，一个字都不写回真相源）：
  问「这块业务是什么 / 数据在哪 / 卡住找谁 / 证据在哪」，回一份 JSON。
  可见范围 = render.VisibleDomains（与 persona 段 8 同一把尺子）：自己的域全行，
  别人的域只给「找谁」；要别人的域**拒**，但指路。身份不许自报：解不出 = 这个人不存在。
  每次调用留一条痕进 audit（谁 / 要什么 / 给没给 / 为什么拒）。

门禁口径（与 envelope check 完全一致，因为走的是同一份规则表）：
  发现默认 warn = **照收**（落盘 + 回话）；被 company.md 的 policy 段提成 fatal 的，
  按 fatal 的定义（阻止落盘）**拒收**：不写日志、回 isError=true。想开就开、想关就关，
  开关在数据里，不在这段代码里。

无状态：不发 Mcp-Session-Id、不记会话、不提供 SSE 流 —— 状态在真相源与日志里，不在连接里。

安全口径：默认只绑本机（127.0.0.1）。要给别人用请走 ssh 端口转发，别把 0.0.0.0 开出去。

退出：Ctrl-C。
`

// envelopeIngress 是接入面本身：一个 vault + 一个 data 目录 + 一个时钟。
type envelopeIngress struct {
	Vault   string
	DataDir string
	Now     func() time.Time
}

func (g *envelopeIngress) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// srv 装配 MCP 服务端：**桥的两半挂在这一个入口上** —— 递进来（信封）与要出去（读出口）。
// 两个工具都是「一次调用 = 一条痕」，所以 #44「入口唯一性」没被破：多的不是入口，
// 是同一个入口上的第二个动作。
func (g *envelopeIngress) srv() *mcp.Server {
	return &mcp.Server{
		Name: "anc-envelope", Version: version,
		Tools: []mcp.Tool{{
			Name: "anc_send_envelope",
			Description: "把一个信封递给 ANC（接入面）。who 必须是 vault 里真实存在的成员/bot 名 —— " +
				"身份不许自报：解不出来 ANC 会如实报出来（默认只告警、不拦）。" +
				"on_behalf_of 写你代谁：member:名字 | role:岗位 | domain:slug，或直接写名字。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"who":           map[string]any{"type": "string", "description": "发件人（vault 里真实存在的成员/bot 名）"},
					"on_behalf_of":  map[string]any{"type": "string", "description": "代谁：member:名字 | role:岗位 | domain:slug，或裸名字"},
					"kind":          map[string]any{"type": "string", "description": "ask / report / notify / ingest / proposal（认不出的照收）"},
					"body":          map[string]any{"type": "string", "description": "正文"},
					"scope_domain":  map[string]any{"type": "string", "description": "哪块业务（domains.md 的 slug）"},
					"scope_project": map[string]any{"type": "string", "description": "哪个项目（projects.md 的 slug）"},
					"refs":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "证据在哪"},
					"needs":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "要不要人拍（自由文本）"},
				},
				"required": []string{"who", "body"},
			},
			Call: g.sendEnvelope,
		}, {
			Name: "anc_read_context",
			Description: "向 ANC 要上下文（**只读**）：这块业务是什么、数据在哪、卡住找谁。" +
				"who 必须是 vault 里真实存在的成员名 —— 身份不许自报，解不出 ANC 会说没有这个人。" +
				"domain 可选（domains.md 的 slug）：不写 = 你负责的域；写别人的域会被拒，" +
				"但会告诉你要找谁（数据默认不通，跨域按「找谁」接头）。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"who":          map[string]any{"type": "string", "description": "你是谁（vault 里真实存在的成员名）"},
					"on_behalf_of": map[string]any{"type": "string", "description": "可选：你代谁（member:名字 | role:岗位 | domain:slug，或裸名字）"},
					"domain":       map[string]any{"type": "string", "description": "可选：只问这一块业务（domains.md 的 slug）；不写 = 你负责的域"},
				},
				"required": []string{"who"},
			},
			Call: g.readContext,
		}},
		Log: func(format string, a ...any) { fmt.Printf("  · "+format+"\n", a...) },
	}
}

// sendEnvelope 收一封信：拼信封 → Parse → Bind → 落盘 → 回六问六答。
func (g *envelopeIngress) sendEnvelope(args map[string]any) (string, bool) {
	now := g.now()
	id := strArg(args, "id")
	if id == "" {
		// 服务端补 id 与 ts：harness 不该为「这封信叫什么」操心。
		id = fmt.Sprintf("e-%d", now.UnixNano())
	}
	wire := map[string]any{"id": id, "ts": now.Format(time.RFC3339)}
	for _, k := range []string{"who", "on_behalf_of", "kind", "body"} {
		if v := strArg(args, k); v != "" {
			wire[k] = v
		}
	}
	scope := map[string]any{}
	if v := strArg(args, "scope_domain"); v != "" {
		scope["domain"] = v
	}
	if v := strArg(args, "scope_project"); v != "" {
		scope["project"] = v
	}
	if len(scope) > 0 {
		wire["scope"] = scope
	}
	if v := listArg(args, "refs"); len(v) > 0 {
		wire["refs"] = v
	}
	if v := listArg(args, "needs"); len(v) > 0 {
		wire["needs"] = v
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return "拼信封失败：" + err.Error(), true
	}
	e, err := envelope.Parse(raw)
	if err != nil {
		return "这封信读不懂，没收：" + err.Error(), true
	}
	o, err := org.Load(g.Vault)
	if err != nil {
		return "真相源加载失败，绑不了：" + err.Error() + "\n（先修 org：anc org check <vault>）", true
	}
	issues := envelope.Bind(o, e, o.Policy)
	fatal, warn := splitIssues(issues)

	var b strings.Builder
	if len(fatal) > 0 {
		// fatal 在这个库里的定义就是「阻止落盘」（org/rules.go）—— 所以拒收：不写日志。
		fmt.Fprintf(&b, "拒收（%d 项红档；日志没写）：\n", len(fatal))
		for _, i := range fatal {
			fmt.Fprintf(&b, "  🔴 [%s] %s\n", i.Rule, i.Msg)
		}
		return b.String(), true
	}
	rec := envelope.Record{Envelope: e, At: now.Format(time.RFC3339), Findings: envelope.ToFindings(issues)}
	path, err := envelope.Append(g.DataDir, rec)
	if err != nil {
		return "收了，但落不了盘（这次不算数）：" + err.Error(), true
	}
	fmt.Fprintf(&b, "收了，已落 %s\n\n", path)
	for _, l := range e.Brief() {
		fmt.Fprintf(&b, "  %s：%s\n", l.Q, l.A)
	}
	if len(warn) == 0 {
		b.WriteString("\n绑定：0 项发现。")
		return b.String(), false
	}
	fmt.Fprintf(&b, "\n绑定：%d 项非红档发现（照收，只是记下来）：\n", len(warn))
	for _, i := range warn {
		fmt.Fprintf(&b, "  🟡 [%s] %s\n", i.Rule, i.Msg)
	}
	return b.String(), false
}

func strArg(args map[string]any, k string) string {
	if v, ok := args[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func listArg(args map[string]any, k string) []string {
	raw, ok := args[k].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func cmdEnvelopeServe(args []string) int {
	fs := flag.NewFlagSet("envelope serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8791", "监听地址")
	data := fs.String("data", "", "运行态 data 目录（信封日志落这儿）")
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 {
		fmt.Fprint(os.Stderr, envelopeServeUsage)
		return 2
	}
	vault, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 2
	}
	if st, err := os.Stat(vault); err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "错误：vault 目录不存在或不是目录：%s\n", vault)
		return 1
	}
	dataDir := *data
	if dataDir == "" {
		dataDir = filepath.Join(filepath.Dir(vault), "data")
	} else if a, err := filepath.Abs(dataDir); err == nil {
		dataDir = a
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "错误：建不了 data 目录：%v\n", err)
		return 1
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：监听 %s 失败：%v\n", *addr, err)
		return 1
	}
	defer func() { _ = ln.Close() }()

	urlHost := ln.Addr().String()
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		if tcp.IP.IsUnspecified() {
			urlHost = net.JoinHostPort("127.0.0.1", strconv.Itoa(tcp.Port))
		}
		if !tcp.IP.IsLoopback() {
			fmt.Fprintf(os.Stderr,
				"⚠️  绑到了 %s：接入面只该给本机（或经 ssh 端口转发的人）用。\n", ln.Addr())
		}
	}

	// 起之前先看一眼真相源：红的照样起（接入面正是用来看「哪里坏了」的），但要说出来。
	if o, err := org.Load(vault); err == nil {
		printIssues(o.Warnings)
	} else {
		fmt.Fprintf(os.Stderr, "⚠️  真相源现在有红档发现（绑定会带上它们）：\n%v\n", err)
	}

	g := &envelopeIngress{Vault: vault, DataDir: dataDir, Now: time.Now}
	fmt.Printf("ANC 接入面已起（MCP · streamable HTTP · 无状态）：\n")
	fmt.Printf("  地址   http://%s/mcp\n", urlHost)
	fmt.Printf("  vault  %s\n", vault)
	fmt.Printf("  日志   %s\n", filepath.Join(dataDir, envelope.DirName))
	fmt.Printf("  工具   anc_send_envelope（递进来）· anc_read_context（要出去）\n")
	fmt.Printf("  接入   claude mcp add --transport http anc http://%s/mcp\n", urlHost)
	fmt.Printf("  退出   Ctrl-C\n\n")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	srv := &http.Server{Handler: g.srv().Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		fmt.Println("\n收到中断，正在关闭接入面…")
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			fmt.Fprintf(os.Stderr, "关闭时出错：%v\n", err)
			return 1
		}
		return 0
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
}

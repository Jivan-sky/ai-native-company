package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"anc/internal/envelope"
	"anc/internal/org"
)

const envelopeUsage = `anc envelope —— 接入面的信封：解析 + 绑真相源（只读）

用法：
  anc envelope check <vault 目录> <信封.json> [--json]

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

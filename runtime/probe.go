package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	renderpkg "anc/internal/render"
)

const probeUsage = `anc probe —— 运行态探针（防假绿，只读）

用法：
  anc probe <vault 目录> [选项]

选项：
  --config <文件>   gateway config（默认 <vault>/../gateway/config.toml）
  --data <目录>     gateway data_dir（默认 <vault>/../data）
  --stale <时长>    多久没有成功交互就算「不新鲜」（默认 24h）
  --stall <时长>    「最后一轮没人回」超过多久算卡住（默认 10m）
  --json            给看板 / watchdog 消费的 JSON（打到 stdout）

为什么不是「看进程在不在」：
  今天已经踩过两次 —— 进程活着、engine started 打着绿字、连接也建着，
  但每条消息都起不来 agent。所以判据只能是「真回话过没有」。

只读两样东西，都在磁盘上、跨平台、不烧 token：
  1. <data>/run/api.sock —— **真拨一次**，不看文件在不在
     （残留的 socket 文件会把「已经挂了」看成「在跑」）。
  2. <data>/sessions/*.json —— 会话事实（谁说过话、agent 起没起来、最后谁回的）。

三档：
  绿  窗口内有过**真实回复**（assistant 真回了话），不是「引擎起来了」。
  黄  没观测到 / 久无成功交互。「没消息 = 没事」是反模式，所以这一档不许报绿。
  红  有消息但 agent 起不来，或最后一轮迟迟不回。

退出码：0 全绿；1 有黄或红；2 用法错误
`

// probeFinding 是一个 bot 的运行态判定。
type probeFinding struct {
	Project string `json:"project"`
	State   string `json:"state"` // ok / warn / fail
	Why     string `json:"why"`
}

type probeReport struct {
	Vault      string         `json:"vault"`
	Config     string         `json:"config"`
	DataDir    string         `json:"data_dir"`
	Gateway    string         `json:"gateway"` // up / down
	GatewayWhy string         `json:"gateway_why,omitempty"`
	Findings   []probeFinding `json:"findings"`
	Extras     []string       `json:"extras,omitempty"`
}

// ccSessions 只取探针要用的那几个字段；别的字段上游会变，不跟着它跑。
type ccSessions struct {
	Sessions map[string]struct {
		AgentSessionID string `json:"agent_session_id"`
		History        []struct {
			Role      string `json:"role"`
			Timestamp string `json:"timestamp"`
		} `json:"history"`
	} `json:"sessions"`
}

func cmdProbe(args []string) int {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	cfg := fs.String("config", "", "gateway config")
	data := fs.String("data", "", "gateway data_dir")
	stale := fs.Duration("stale", 24*time.Hour, "多久没有成功交互算不新鲜")
	stall := fs.Duration("stall", 10*time.Minute, "最后一轮没人回多久算卡住")
	asJSON := fs.Bool("json", false, "输出 JSON")
	vault := fs.String("vault", "", "vault 目录（也可用位置参数）")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	root := strings.TrimSpace(*vault)
	if root == "" && len(posArgs) > 0 {
		root = posArgs[0]
	}
	if root == "" {
		fmt.Fprint(os.Stderr, probeUsage)
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 2
	}
	cfgPath := *cfg
	if cfgPath == "" {
		cfgPath = filepath.Join(filepath.Dir(abs), "gateway", "config.toml")
	}
	dataPath := *data
	if dataPath == "" {
		dataPath = filepath.Join(filepath.Dir(abs), "data")
	}

	rep := probeReport{Vault: abs, Config: cfgPath, DataDir: dataPath}
	now := time.Now()

	// 源：配置里**声明**了哪些 project。声明了但没有任何会话 = 「没观测到」，
	// 不是「没事」—— 那是假绿的另一种长相。
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: 读不到 config %s: %v\n", cfgPath, err)
		fmt.Fprintf(os.Stderr, "FIX: 先 `anc render <vault> --apply`\n")
		return 1
	}
	declared := renderpkg.ProjectsIn(string(raw))

	// 源 1：gateway 活着吗。真拨一次，不靠文件存在性。
	sock := filepath.Join(dataPath, "run", "api.sock")
	if why := dialUnix(sock); why == "" {
		rep.Gateway = "up"
	} else {
		rep.Gateway = "down"
		rep.GatewayWhy = why
	}

	// 源 2：会话事实。
	sessDir := filepath.Join(dataPath, "sessions")
	files, _ := filepath.Glob(filepath.Join(sessDir, "*.json"))
	byProject := map[string][]string{} // project → 会话文件
	for _, f := range files {
		p := projectOfSessionFile(filepath.Base(f))
		byProject[p] = append(byProject[p], f)
	}

	for _, p := range declared {
		if rep.Gateway == "down" {
			rep.Findings = append(rep.Findings, probeFinding{p, "fail", "gateway 没在跑，这个 bot 必然回不了话"})
			continue
		}
		rep.Findings = append(rep.Findings, judgeProject(p, byProject[p], now, *stale, *stall))
		delete(byProject, p)
	}
	// 声明里没有、但磁盘上有会话 → 配置删过 bot 却留着它的记录。
	var orphans []string
	for p, fs := range byProject {
		orphans = append(orphans, fmt.Sprintf("%s（%d 个会话文件，配置里已无此 project）", p, len(fs)))
	}
	sort.Strings(orphans)
	rep.Extras = orphans

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else {
		printProbe(rep)
	}
	for _, f := range rep.Findings {
		if f.State != "ok" {
			return 1
		}
	}
	if len(rep.Extras) > 0 || rep.Gateway != "up" {
		return 1
	}
	return 0
}

// dialUnix 判活：**拨一次**，不靠 socket 文件在不在。
// 形态到这一步已经统一了 —— DESIGN §7 说过不许假定 Windows(文件) / Linux(unix socket)，
// 而「存在性」两边都成立，所以它反而是错判据：进程崩了文件会留下。
func dialUnix(path string) string {
	if _, err := os.Stat(path); err != nil {
		return "socket 不存在：" + path
	}
	c, err := net.DialTimeout("unix", path, 3*time.Second)
	if err != nil {
		return "socket 在但拨不通（多半是进程已经没了，留下的是残留文件）：" + err.Error()
	}
	c.Close()
	return ""
}

// projectOfSessionFile 从 `demo-alice_00a71faa.json` 取出 project 名。
func projectOfSessionFile(base string) string {
	name := strings.TrimSuffix(base, ".json")
	if i := strings.LastIndex(name, "_"); i > 0 {
		return name[:i]
	}
	return name
}

// judgeProject 只看会话事实，不看进程不烧 token。
func judgeProject(project string, files []string, now time.Time, stale, stall time.Duration) probeFinding {
	if len(files) == 0 {
		return probeFinding{project, "warn", "没观测到任何会话 —— 「没消息 = 没事」是反模式，所以这里不许报绿"}
	}
	var lastUser, lastAssistant time.Time
	neverStarted := false
	var msgs int
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s ccSessions
		if err := json.Unmarshal(b, &s); err != nil {
			return probeFinding{project, "warn", fmt.Sprintf("会话文件读不动（%s）：%v", filepath.Base(f), err)}
		}
		for _, one := range s.Sessions {
			for _, h := range one.History {
				msgs++
				ts, err := time.Parse(time.RFC3339Nano, h.Timestamp)
				if err != nil {
					continue
				}
				switch h.Role {
				case "user":
					if ts.After(lastUser) {
						lastUser = ts
					}
				case "assistant":
					if ts.After(lastAssistant) {
						lastAssistant = ts
					}
				}
			}
			// 有消息、却从没拿到过 agent 会话 id = agent 一次都没起来过。
			// 这正是「进程活着 / engine started 是绿的 / 但回不了话」的签名。
			if one.AgentSessionID == "" && len(one.History) > 0 {
				neverStarted = true
			}
		}
	}
	if msgs == 0 {
		return probeFinding{project, "warn", "会话文件是空的 —— 没观测到实际交互"}
	}
	if neverStarted {
		return probeFinding{project, "fail", "有消息、但 agent 从没起来过（agent_session_id 为空）—— 多半是 work_dir / 工具白名单 / agent 二进制的问题，查 gateway 日志"}
	}
	if !lastUser.IsZero() && lastUser.After(lastAssistant) {
		if since := now.Sub(lastUser); since > stall {
			return probeFinding{project, "fail", fmt.Sprintf("最后一轮消息发出 %s 还没有回复（超过 --stall %s）", since.Round(time.Second), stall)}
		}
		return probeFinding{project, "warn", fmt.Sprintf("最后一轮正在处理中（发出 %s）", now.Sub(lastUser).Round(time.Second))}
	}
	switch {
	case lastAssistant.IsZero():
		return probeFinding{project, "warn", "只有用户消息，还没有过一次回复"}
	case now.Sub(lastAssistant) > stale:
		return probeFinding{project, "warn", fmt.Sprintf("最近一次成功回复在 %s 前，超过 --stale %s —— 没人打扰它的话，这个窗口里它到底能不能回话是不知道的",
			now.Sub(lastAssistant).Round(time.Minute), stale)}
	default:
		return probeFinding{project, "ok", fmt.Sprintf("最近一次成功回复在 %s 前", now.Sub(lastAssistant).Round(time.Second))}
	}
}

func printProbe(rep probeReport) {
	mark := map[string]string{"ok": "🟢", "warn": "🟡", "fail": "🔴"}
	fmt.Printf("anc probe —— 运行态探针（只读）\n")
	fmt.Printf("  vault    %s\n", rep.Vault)
	fmt.Printf("  config   %s\n", rep.Config)
	fmt.Printf("  data     %s\n", rep.DataDir)
	if rep.Gateway == "up" {
		fmt.Printf("  gateway  🟢 在跑（socket 拨得通）\n")
	} else {
		fmt.Printf("  gateway  🔴 %s\n", rep.GatewayWhy)
	}
	fmt.Println()
	if len(rep.Findings) == 0 {
		fmt.Println("  （config 里一个 project 都没有）")
	}
	for _, f := range rep.Findings {
		fmt.Printf("  %s %-18s %s\n", mark[f.State], f.Project, f.Why)
	}
	if len(rep.Extras) > 0 {
		fmt.Println("\n⚠️  配置外的残留（不是运行态问题，但该清）")
		for _, e := range rep.Extras {
			fmt.Printf("  - %s\n", e)
		}
	}
	fmt.Println()
	fmt.Println("口径：这里只读磁盘事实（socket 拨通 + 会话记录），不烧 token。")
	fmt.Println("      真要「现在这一秒能不能回话」，得发一条真消息 —— 那是 `--probe` 级的事，还没做。")
}

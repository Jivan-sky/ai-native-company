// Package probe 观测**运行态**：不烧 token、不往机器上装东西，只读磁盘事实，
// 回答一个问题 —— 「这个 bot 到底能不能回话」。
//
// 为什么不是「看进程在不在」：2026-10-07 一天踩了两次 —— 进程活着、
// `engine started` 打着绿字、长连接也建着，但每条消息都起不来 agent。
// 所以判据只能是「真回话过没有」。
//
// 只读两源，都在磁盘上、跨平台、零依赖：
//
//  1. <data>/run/api.sock —— **真拨一次**，不看文件在不在
//     （残留的 socket 文件会把「已经挂了」看成「在跑」，这正是假绿的经典长相）；
//  2. <data>/sessions/*.json —— 会话事实：谁说过话、agent 起没起来、最后谁回的。
//
// 三档，且刻意让「绿」很难拿到（「没消息 = 没事」是反模式，见 DESIGN §「观测三源交叉」）：
//
//	ok   窗口内有过**真实回复**（assistant 真回了话）；
//	warn 没观测到 / 久无成功交互 —— 这一档**不许**报绿；
//	fail 有消息但 agent 起不来，或最后一轮迟迟不回。
package probe

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	renderpkg "anc/internal/render"
)

// Schema 是这个报告的版本。有消费方（看板、watchdog）之后，字段增删一律改它。
const Schema = "anc.runtime/v1"

// State 是三档判定。
type State string

const (
	StateOK   State = "ok"
	StateWarn State = "warn"
	StateFail State = "fail"
)

// Finding 是一个 bot 的运行态判定。
type Finding struct {
	Project string `json:"project"`
	State   State  `json:"state"`
	Why     string `json:"why"`
}

// Report 是一次观测的全量结果。
type Report struct {
	Schema     string    `json:"schema"`
	Vault      string    `json:"vault"`
	Config     string    `json:"config"`
	DataDir    string    `json:"data_dir"`
	Gateway    string    `json:"gateway"` // up / down
	GatewayWhy string    `json:"gateway_why,omitempty"`
	Bots       []Finding `json:"bots"`
	Extras     []string  `json:"extras,omitempty"`
	// Handlers：探针只做观测，**裁判权留给人工**。所以要顺手说清「出问题该交给谁」，
	// 否则一份红报告等于把问题丢在地上。来源是 company.admins（SPEC §「谁能发特权命令」）。
	Handlers   []string `json:"handlers"`
	HandlerWhy string   `json:"handler_why,omitempty"`
}

// Options 是观测输入。Projects / Admins 由调用方从真相源算好传进来 —— 这个包不认识 org。
type Options struct {
	Vault    string
	Config   string
	DataDir  string
	Stale    time.Duration // 多久没有成功交互算「不新鲜」
	Stall    time.Duration // 最后一轮没人回多久算卡住
	Now      time.Time
	Admins   []string // 出问题该交给谁（显示用）
	Projects []string // 该有哪些 project；nil = 从 config 读
}

// DefaultStale / DefaultStall 是出厂阈值。
// 两个数都是按 2026-10-07 的实测手感取的（一次真回复 5~12s），不是算出来的 ——
// 客户现场大概率要按业务节奏调，所以做成参数而不是常量。
const (
	DefaultStale = 24 * time.Hour
	DefaultStall = 10 * time.Minute
)

// ccSessions 只取探针要用的字段；别的字段上游会变，不跟着它跑。
type ccSessions struct {
	Sessions map[string]struct {
		AgentSessionID string `json:"agent_session_id"`
		History        []struct {
			Role      string `json:"role"`
			Timestamp string `json:"timestamp"`
		} `json:"history"`
	} `json:"sessions"`
}

// Run 做一次观测。返回 error 只在「连该有哪些 bot 都不知道」时发生（config 读不到）。
func Run(opt Options) (Report, error) {
	if opt.Stale <= 0 {
		opt.Stale = DefaultStale
	}
	if opt.Stall <= 0 {
		opt.Stall = DefaultStall
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	rep := Report{
		Schema:   Schema,
		Vault:    opt.Vault,
		Config:   opt.Config,
		DataDir:  opt.DataDir,
		Bots:     []Finding{},
		Handlers: opt.Admins,
	}

	// 声明：该有哪些 bot。声明了但没有任何会话 = 「没观测到」，不是「没事」——
	// 那是假绿的另一种长相。
	declared := opt.Projects
	if declared == nil {
		raw, err := os.ReadFile(opt.Config)
		if err != nil {
			return rep, fmt.Errorf("读不到 config %s: %w", opt.Config, err)
		}
		declared = renderpkg.ProjectsIn(string(raw))
	}

	if why := DialUnix(filepath.Join(opt.DataDir, "run", "api.sock")); why == "" {
		rep.Gateway = "up"
	} else {
		rep.Gateway = "down"
		rep.GatewayWhy = why
	}

	files, _ := filepath.Glob(filepath.Join(opt.DataDir, "sessions", "*.json"))
	byProject := map[string][]string{}
	for _, f := range files {
		p := ProjectOfSessionFile(filepath.Base(f))
		byProject[p] = append(byProject[p], f)
	}

	for _, p := range declared {
		if rep.Gateway == "down" {
			rep.Bots = append(rep.Bots, Finding{p, StateFail, "gateway 没在跑，这个 bot 必然回不了话"})
			continue
		}
		rep.Bots = append(rep.Bots, Judge(p, byProject[p], opt.Now, opt.Stale, opt.Stall))
		delete(byProject, p)
	}
	// 配置里没有、磁盘上却有会话 → 删过 bot 却留着它的记录。
	for p, fs := range byProject {
		rep.Extras = append(rep.Extras, fmt.Sprintf("%s（%d 个会话文件，配置里已无此 project）", p, len(fs)))
	}
	sort.Strings(rep.Extras)
	return rep, nil
}

// DialUnix 判活：**拨一次**，不靠 socket 文件在不在。
// 「不许假定 socket 形态」（DESIGN §7）到这一步已经统一了：Windows 是文件、Linux 是 unix
// socket 节点，而**存在性**两边都成立 —— 所以它反而是错判据，进程崩了文件会留下。
func DialUnix(path string) string {
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

// ProjectOfSessionFile 从 `demo-alice_00a71faa.json` 取出 project 名。
func ProjectOfSessionFile(base string) string {
	name := strings.TrimSuffix(base, ".json")
	if i := strings.LastIndex(name, "_"); i > 0 {
		return name[:i]
	}
	return name
}

// Judge 只看会话事实，不看进程、不烧 token。
func Judge(project string, files []string, now time.Time, stale, stall time.Duration) Finding {
	if len(files) == 0 {
		return Finding{project, StateWarn, "没观测到任何会话 —— 「没消息 = 没事」是反模式，所以这里不许报绿"}
	}
	var lastUser, lastAssistant time.Time
	neverStarted := false
	msgs := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s ccSessions
		if err := json.Unmarshal(b, &s); err != nil {
			return Finding{project, StateWarn, fmt.Sprintf("会话文件读不动（%s）：%v", filepath.Base(f), err)}
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
		return Finding{project, StateWarn, "会话文件是空的 —— 没观测到实际交互"}
	}
	if neverStarted {
		return Finding{project, StateFail, "有消息、但 agent 从没起来过（agent_session_id 为空）—— 多半是 work_dir / 工具白名单 / agent 二进制的问题，查 gateway 日志"}
	}
	if !lastUser.IsZero() && lastUser.After(lastAssistant) {
		if since := now.Sub(lastUser); since > stall {
			return Finding{project, StateFail, fmt.Sprintf("最后一轮消息发出 %s 还没有回复（超过 --stall %s）", since.Round(time.Second), stall)}
		}
		return Finding{project, StateWarn, fmt.Sprintf("最后一轮正在处理中（发出 %s）", now.Sub(lastUser).Round(time.Second))}
	}
	switch {
	case lastAssistant.IsZero():
		return Finding{project, StateWarn, "只有用户消息，还没有过一次回复"}
	case now.Sub(lastAssistant) > stale:
		return Finding{project, StateWarn, fmt.Sprintf("最近一次成功回复在 %s 前，超过 --stale %s —— 没人打扰它的话，这个窗口里它到底能不能回话是不知道的",
			now.Sub(lastAssistant).Round(time.Minute), stale)}
	default:
		return Finding{project, StateOK, fmt.Sprintf("最近一次成功回复在 %s 前", now.Sub(lastAssistant).Round(time.Second))}
	}
}

// AllGreen 只在「没有 fail / warn / 残留 / 网关不在跑」时为真。
func (r Report) AllGreen() bool {
	if r.Gateway != "up" || len(r.Extras) > 0 {
		return false
	}
	for _, f := range r.Bots {
		if f.State != StateOK {
			return false
		}
	}
	return true
}

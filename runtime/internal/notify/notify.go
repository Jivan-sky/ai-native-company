// Package notify 把「运行态观测」变成「送到人面前的动作」。
//
// 它与 probe 之间是一条**硬边界**：
//
//	probe  —— 只观测、只读，永远不产生任何副作用；
//	notify —— 有副作用：通过 cc-connect 的 socket 往人的聊天里推消息。
//
// 合在一起会毁掉 probe 的立身之本（一个只读命令被人当只读工具用、随手跑），
// 所以分成两条命令：probe 出事实，notify 出动作。
//
// 通知的判据只有两条，都是第一性原理：
//
//  1. **只看红。** 状态色口径是「要不要人动手」：绿 = 已完成，红 = 失败 / 卡点需介入，
//     黄 = 运行中 / 这一窗口没观测到。黄不要人动手。把黄也推给人 → 人被吵烦 → 静音 →
//     红的告警也一起死。所以宁可少喊。
//  2. **边沿触发 + 冷却。** 状态没变就不喊；一直红着则每过一个冷却期提醒一次
//     （否则就变成「没消息 = 没事」那个反模式）；红转绿喊一次「已恢复」，然后清账。
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"anc/internal/probe"
)

// Schema 是游标文件的版本。游标只记「因为哪件事、什么时候喊过人」——
// 不记观测结果（观测结果每次现算，落了盘反而会跟现场对不上）。
const Schema = "anc.notify/v1"

// KeyGateway 是网关这个可独立恶化对象的键。网关挂了，底下每个 bot 都会跟着判红，
// 但那是**同一个根因**，所以只喊网关一条 —— 否则一次网关重启会往人脸上糊 N 条。
const KeyGateway = "gateway"

// DefaultCooldown 是出厂冷却：同一个问题 30 分钟内不重复喊。
// 这个数按「人来得及动手」与「不会被吵到静音」之间取，是手感不是算出来的，
// 所以做成参数（同 probe 的 --stale / --stall）。
const DefaultCooldown = 30 * time.Minute

// Mark 是上一次因为某个 key 喊过人的记录。
type Mark struct {
	Level string    `json:"level"`
	At    time.Time `json:"at"`
	Why   string    `json:"why,omitempty"`
}

// Cursor 是通知游标。它**不进 git**（DESIGN §2 的 state/），
// 位置在 <data_dir>/state/notify.json —— 与它描述的那份现场待在一起。
type Cursor struct {
	Schema string          `json:"schema"`
	Keys   map[string]Mark `json:"keys"`
}

// CursorPath 是游标落点。跟 gateway 的 socket、会话记录同一个 data 目录 ——
// 换个 data 目录就是换一份现场，游标也该跟着换。
func CursorPath(dataDir string) string {
	return filepath.Join(dataDir, "state", "notify.json")
}

// LoadCursor 读游标。文件不存在 = 空游标（第一次跑本来就没有）。
//
// 但**读得动却解析不了**要报错、不许当空游标静默吞掉：那会把「已经喊过了」当成
// 「没喊过」，于是同一件事被反复推给人。宁可让运维看见这个错。
func LoadCursor(path string) (Cursor, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Cursor{Schema: Schema, Keys: map[string]Mark{}}, nil
		}
		return Cursor{}, err
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return Cursor{}, fmt.Errorf("游标 %s 解析不了（删掉它可以从头开始）：%w", path, err)
	}
	if c.Keys == nil {
		c.Keys = map[string]Mark{}
	}
	c.Schema = Schema
	return c, nil
}

// Save 原子写游标：临时文件 + rename，免得定时任务读到写了一半的文件。
func (c Cursor) Save(path string) error {
	if c.Keys == nil {
		c.Keys = map[string]Mark{}
	}
	c.Schema = Schema
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Kind 是这一轮动作的性质。
type Kind string

const (
	KindAlert    Kind = "alert"    // 刚变红 / 第一次看见红
	KindReminder Kind = "reminder" // 一直红着，过了冷却期再提醒一次
	KindRecovery Kind = "recovery" // 红转绿：把账清掉，并告诉人一声
)

// Action 是一条待送出的通知。
type Action struct {
	Key   string `json:"key"`
	Kind  Kind   `json:"kind"`
	Level string `json:"level"`
	Why   string `json:"why"`
}

// Plan 是一轮决策的结果。
//
// Silent 单独留着，是为了让「没喊」这件事**说得出来** —— 否则「没通知」看起来
// 和「没事」一模一样，那正是我们一直在防的反模式。冷却压下的会落在这一栏。
type Plan struct {
	Actions []Action `json:"actions"`
	Silent  []Action `json:"silent,omitempty"`
	Next    Cursor   `json:"next"`
}

// Decide 算出「这一轮该喊谁、喊什么」，并给出新的游标。
// 纯函数：不读盘、不发消息、不看时钟（now 由调用方给）—— 所以档位用例可以定时定刻地测。
func Decide(cur Cursor, rep probe.Report, cooldown time.Duration, now time.Time) Plan {
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}
	next := Cursor{Schema: Schema, Keys: map[string]Mark{}}
	for k, v := range cur.Keys { // 先整份抄过来：没被评估到的 key 原样留着
		next.Keys[k] = v
	}
	p := Plan{Next: next}

	// 网关挂了就**只喊网关**：底下每个 bot 判红是同一个根因的下游后果，
	// 逐个喊一遍只是在往人脸上刷屏。网关恢复之前，不评估也不动 bot 的账。
	if rep.Gateway != "up" {
		step(&p, next, cur, KeyGateway, probe.StateFail, rep.GatewayWhy, cooldown, now)
		return p
	}
	// 网关没挂，但它的账还欠着 → 补一条「已恢复」，然后清账。
	step(&p, next, cur, KeyGateway, probe.StateOK, "socket 拨得通", cooldown, now)

	// 固定顺序（项目名排序）出动作：diff 稳定，人看着也不跳。
	findings := append([]probe.Finding(nil), rep.Bots...)
	sort.Slice(findings, func(i, j int) bool { return findings[i].Project < findings[j].Project })
	for _, f := range findings {
		step(&p, next, cur, f.Project, f.State, f.Why, cooldown, now)
	}
	return p
}

// step 处理一个 key 的游标与动作。三个分支就是全部判据。
func step(p *Plan, next Cursor, cur Cursor, key string, level probe.State, why string, cooldown time.Duration, now time.Time) {
	prev, seen := cur.Keys[key]

	if level != probe.StateFail {
		if !seen {
			return // 没喊过的账，转绿不欠谁一声
		}
		delete(next.Keys, key) // 红账已清
		p.Actions = append(p.Actions, Action{Key: key, Kind: KindRecovery, Level: string(level), Why: why})
		return
	}
	if !seen {
		next.Keys[key] = Mark{Level: string(level), At: now, Why: why}
		p.Actions = append(p.Actions, Action{Key: key, Kind: KindAlert, Level: string(level), Why: why})
		return
	}
	if now.Sub(prev.At) >= cooldown {
		next.Keys[key] = Mark{Level: string(level), At: now, Why: why}
		p.Actions = append(p.Actions, Action{Key: key, Kind: KindReminder, Level: string(level), Why: why})
		return
	}
	next.Keys[key] = prev // 冷却期内：留着，等下一轮
	p.Silent = append(p.Silent, Action{Key: key, Kind: KindReminder, Level: string(level), Why: why})
}

// SendRequest 是 cc-connect `POST /send` 的请求体（只取我们要用的两个字段）。
type SendRequest struct {
	Project string `json:"project"`
	Message string `json:"message"`
}

// Push 通过 cc-connect 的 socket 往某个 project 的聊天里推一条消息。
//
// 为什么不直连飞书 API：这条通道要走**平台无关**的那一层。cc-connect 自己支持
// 飞书 / 钉钉 / 企业微信 / Slack……，换平台是它那一侧的事，这里一行都不用改。
// 用的还是探针判活时拨的那个 socket —— 判活和喊人走同一条通道，不会出现
// 「探针说活着、可消息推不出去」这种两套口径。
func Push(sockPath, project, message string) error {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, "unix", sockPath)
		},
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}

	body, err := json.Marshal(SendRequest{Project: project, Message: message})
	if err != nil {
		return err
	}
	resp, err := client.Post("http://cc-connect/send", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("推不出去（多半是 gateway 没在跑）：%w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gateway 拒了（HTTP %d）：%s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// Text 是一条通知的正文。人一眼要看出来三件事：**谁的、什么毛病、该谁动手**。
// 故意不带本机路径、不带内部标识符 —— 这条消息会落到 IM 里，是**观测面**。
func Text(a Action, who []string, cooldown time.Duration) string {
	var b strings.Builder
	name := a.Key
	if a.Key == KeyGateway {
		name = "gateway"
	}
	if a.Kind == KindRecovery {
		fmt.Fprintf(&b, "🟢 %s 已恢复\n%s", name, a.Why)
		return b.String()
	}
	head := "🔴 " + name + " 回不了话"
	if a.Kind == KindReminder {
		head = "🔴（仍未解决）" + name + " 回不了话"
	}
	fmt.Fprintf(&b, "%s\n%s", head, a.Why)
	if len(who) > 0 {
		fmt.Fprintf(&b, "\n该处理：%s", strings.Join(who, "、"))
	}
	fmt.Fprintf(&b, "\n\n（anc notify · 冷却 %s 内不重复；修好后会自动收到一条「已恢复」）", cooldown)
	return b.String()
}

// Package gateway —— relay.go：「第二条网关」的收口环。
//
// 一次按键回程要走的顺序是**数据**，就写在这张表里：
//
//	认事件 → 幂等 → 走同一个信号入口落痕 → 拿终态卡按同一条 message_id 覆盖
//
// 碰外面的两步是接口（Sink）：热层那一侧（落痕）与平台那一侧（收口）换一家就变，
// 而「什么算同一件事」「拒了要不要喊」「收口失败要不要回退」是 ANC 的判据，不该跟着换。
// 拆开之后，这一包能整份被测试对着比 —— 一次点击的结局只有五种，名字只在这一处定义。
//
// 为什么现在只有一条腿（事件从 stdin 进、收口从 stdout 出）：
// 2026-10-11 对着 cc-connect origin/main 逐条核过 —— 它**没有**「卡片事件进来」的公开口。
// unix socket `POST /send` 的请求体只有 message / images / files，没有卡片；
// `POST :9111/hook` 是**反向下发**（往某个会话里塞一条 prompt 或一条命令），不是事件入口，
// 也换不了卡；bridge 协议的入站 `card_action` 载荷不带 operator（谁点的丢在适配器那一侧）。
// 上游能做这件事的只有 open PR #1828（按键带 hook，点击时同步跑一个本地脚本，
// 脚本 stdout 当替卡）—— 它缺的正是 operator。那条腿落地之前，relay 走 stdin/stdout，
// 两端由部署侧那个适配器（`lark-cli event consume` 那条长连接）接上。
//
// 与「收口不归 ANC」的关系：这一包**不碰平台 API**。收口那一步在 Sink.Patch 后面，
// 由部署侧给的适配器去干（SPEC §4.7 ① 的「换平台是网关的事」）。
package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"anc/internal/card"
)

// 一次回程的结局只有这五种。别的名字不许在这一包外面冒出来 ——
// 报告、日志、测试都对着这几个字。
const (
	KindSettled   = "settled"   // 落痕成功（收口另算，看 Patched）
	KindDuplicate = "duplicate" // 同一件事又递了一次
	KindRejected  = "rejected"  // 认得出是一次按键，但判据说不（信号认不出 / 不在队列 / 没带人）
	KindIgnored   = "ignored"   // 根本不是一次按键（别的卡片事件 / 别的形状）
	KindError     = "error"     // 故障（热层连不上之类）
)

// Settle 是「落痕」那一步的产物。字段对着 `anc approvals card-action --json` 那份回执 ——
// 别在这一侧另立一套名字：两份报告对不上的时候，人只能靠数名字来找错。
type Settle struct {
	Proposal  string          `json:"proposal"`
	Signal    string          `json:"signal"`
	By        string          `json:"by"`
	Via       string          `json:"via"`
	At        string          `json:"at"`
	ChatID    string          `json:"chat_id"`
	MessageID string          `json:"message_id"`
	DoneCard  json.RawMessage `json:"done_card,omitempty"`
	Notes     []string        `json:"notes,omitempty"`
	Timeline  string          `json:"timeline"`
	Audit     string          `json:"audit"`
}

// Reject 是「认了、但不能落痕」那种结局：不是故障，是判据说不。
// 分开喊是有用的 —— 拒要给人看原因，故障要报热层连不上。两种都 exit 1，但话不一样。
type Reject struct {
	Reason string
	// Cause 是底下那一层判据（比如 approvals.ErrNotQueued）—— 让人还能用 errors.Is 追下去。
	Cause error
}

func (r Reject) Error() string { return r.Reason }

// Unwrap 让 errors.Is / errors.As 能穿过 Reject 看底下那一层。
func (r Reject) Unwrap() error { return r.Cause }

// IsReject 认 Reject（含被外面 wrap 过的）。
func IsReject(err error) bool {
	var r Reject
	return errors.As(err, &r)
}

// Sink 是那两件要碰外面的事。「换一家网关」换的就是它。
type Sink interface {
	// Settle 走与 CLI / 看板**同一个**信号入口（approvals.New + approvals.Settle）。
	// 判据说不（信号认不出 / 缺键 / 没带人 / 不在队列）返回 Reject。
	Settle(raw []byte) (*Settle, error)
	// Patch 收口：拿终态卡按同一条 message_id 覆盖。它失败**不回退**已经落的痕 ——
	// 痕是账，卡是面子；账不能因为面子丢了。
	Patch(s *Settle) error
}

// Config 是这一层的三个口子。默认值都在这儿，别处不许再出现。
type Config struct {
	// NoPatch 为 true 时只落痕不收口（调用方自己就是应答方时用得上，比如上游那个 hook）。
	// 零值 = 收口开着 —— 网关的本分就是把环闭上。
	NoPatch bool
	// Capacity / TTL 是幂等表的两个边界：最多记多少条、每条保鲜多久。
	// 它**不是正确性的闸**（队列那一侧本来就拒重复），只是少喊一声的减噪器。
	Capacity int
	TTL      time.Duration
	// Now 可注入（测试用）。
	Now func() time.Time
}

const (
	defaultCapacity = 4096
	defaultTTL      = 30 * time.Minute
)

// Relay 是那一环。它**不是并发安全的顺序保证**：同一条消息的两次点击可能并发进来，
// 兜底在队列那一侧（ErrNotQueued）。这里只需保证「同一件事只喊一次」。
type Relay struct {
	cfg  Config
	sink Sink

	mu    sync.Mutex
	mark  map[string]time.Time
	order []string
}

// New 造一个 relay。sink 为 nil 时报错 —— 没有落痕那一半，收口无从谈起。
func New(cfg Config, sink Sink) (*Relay, error) {
	if sink == nil {
		return nil, errors.New("没有 sink：relay 至少要走「落痕」那一半")
	}
	if cfg.Capacity <= 0 {
		cfg.Capacity = defaultCapacity
	}
	if cfg.TTL <= 0 {
		cfg.TTL = defaultTTL
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Relay{cfg: cfg, sink: sink, mark: map[string]time.Time{}}, nil
}

// Outcome 是一次 Handle 的结局。Kind 是那五个常量之一，别的都由它推出来。
type Outcome struct {
	Kind     string  `json:"kind"`
	Key      string  `json:"key,omitempty"`
	Detail   string  `json:"detail,omitempty"`
	Settle   *Settle `json:"settle,omitempty"`
	Patched  bool    `json:"patched,omitempty"`
	PatchErr string  `json:"patch_error,omitempty"`
}

// Line 是这一类结局的**一行话**（进日志、进报告都对着它）。
// 时间不写在这里 —— 谁来记谁填，日志那一侧有自己的钟。
func (o Outcome) Line(now time.Time) string {
	tail := o.Detail
	if o.Kind == KindSettled && o.Settle != nil {
		tail = fmt.Sprintf("%s · %s → %s（%s）", o.Settle.Proposal, o.Settle.By, o.Settle.Signal, o.Settle.Via)
		switch {
		case o.PatchErr != "":
			tail += " · 收口没成：" + o.PatchErr
		case o.Patched:
			tail += " · 已收口"
		}
	}
	if o.Key != "" {
		return fmt.Sprintf("%s %s %s %s", now.Format(time.RFC3339), o.Kind, o.Key, tail)
	}
	return fmt.Sprintf("%s %s %s", now.Format(time.RFC3339), o.Kind, tail)
}

// Handle 吃**一行**事件体（NDJSON 的一行），把它走完那一环。
//
// 它永远不返回 error：门外是一条流，一行坏掉不该把整条流带下去 ——
// 结局都装在 Outcome 里，调用方按 Kind 出日志。
func (r *Relay) Handle(raw []byte) Outcome {
	key := EventKey(raw)
	if key != "" && r.seenBefore(key) {
		return Outcome{Kind: KindDuplicate, Key: key,
			Detail: "同一件事又递了一次（幂等表里已经有它）"}
	}

	// 解析这一步与 Sink 里那一步是**两份**（同一个 raw 解两遍）——
	// 换来的是这里的边界干净：不是一次按键的，根本不进落痕那一侧。
	click, err := card.ParseClick(raw)
	if err != nil {
		return Outcome{Kind: KindIgnored, Key: key, Detail: err.Error()}
	}
	if _, _, ok := click.Point(); !ok {
		return Outcome{Kind: KindRejected, Key: key, Detail: fmt.Sprintf(
			"这条回调没带齐对哪一条、点的什么（要 %s 与 %s 两个键都在）", card.ClickIDKey, card.ClickSignalKey)}
	}

	st, err := r.sink.Settle(raw)
	if err != nil {
		if IsReject(err) {
			return Outcome{Kind: KindRejected, Key: key, Detail: err.Error()}
		}
		return Outcome{Kind: KindError, Key: key, Detail: err.Error()}
	}
	o := Outcome{Kind: KindSettled, Key: key, Settle: st}
	if r.cfg.NoPatch {
		return o
	}
	if st == nil || len(st.DoneCard) == 0 {
		o.PatchErr = "落痕成了，但没拿到终态卡（done_card 空）—— 这一条卡就停在原样子了"
		return o
	}
	if err := r.sink.Patch(st); err != nil {
		o.PatchErr = err.Error()
		return o
	}
	o.Patched = true
	return o
}

// EventKey 是「什么算同一件事」的钥匙。
//
// 优先 `event_id`（飞书长连接那份真事件带它，顶层与 header 两处都认）；
// 没带就退到一枚指纹：哪条消息 + 谁点的 + 点的什么。两样都取不到就返回空 ——
// 那时**不去重**（宁可能多喊一声，也不把两件真不同的事当成一件）。
func EventKey(raw []byte) string {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return ""
	}
	if id := topString(top["event_id"]); id != "" {
		return "e:" + id
	}
	if hdr, ok := top["header"].(map[string]any); ok {
		if id := topString(hdr["event_id"]); id != "" {
			return "e:" + id
		}
	}
	c, err := card.ParseClick(raw)
	if err != nil {
		return ""
	}
	id, signal, ok := c.Point()
	if !ok && c.MessageID == "" && c.OpenID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		c.MessageID, c.OpenID, id, signal,
	}, "\x1f")))
	return "f:" + hex.EncodeToString(sum[:8])
}

// seenBefore 记一笔并答「之前见过没」。过期的先从老的那头倒掉（order 就是时间序）。
func (r *Relay) seenBefore(key string) bool {
	now := r.cfg.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.order) > 0 {
		k := r.order[0]
		t, ok := r.mark[k]
		if ok && now.Sub(t) <= r.cfg.TTL {
			break
		}
		delete(r.mark, k)
		r.order = r.order[1:]
	}
	if _, ok := r.mark[key]; ok {
		return true
	}
	r.mark[key] = now
	r.order = append(r.order, key)
	for len(r.order) > r.cfg.Capacity {
		k := r.order[0]
		delete(r.mark, k)
		r.order = r.order[1:]
	}
	return false
}

func topString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

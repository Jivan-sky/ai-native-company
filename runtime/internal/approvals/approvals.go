// Package approvals —— 提案的「待批 → 点头 → 结账」这一段。
//
// 它在整条链里的位置（SPEC §6 授权模型 / §7 写路径；2026-10-10 拍板 —— 本包不新定口径）：
//
//	提案（信封 kind: proposal）→ 待批队列（本包）→ 人给一个可识别信号 → 留痕
//	                                                          ↓
//	                   grants/ 那个文件由**人侧的管理者 bot** 写，不是 ANC 本体
//
// 四条口径，都是已经拍过的：
//
//  1. **ANC 只做三件**：收提案、送给该批的人、留痕 —— 不写真相源，也不替人判断该不该给。
//  2. **点头只认三个字**：approve / reject / hold，别的一律拒。把「他这句算不算批准」
//     交给模型猜，就是把门交给一个能被说服的东西（§6）。
//  3. **不校「点的人是不是该批的人」**：先原样留痕，判断交给 agent。要校，是后面的事 ——
//     现在写进代码，等于把还没定的口径钉死。
//  4. **待批是「进行中的事实」→ 热层；点头是「达标」→ 先落库、再清热层**（反序会丢东西）。
package approvals

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"anc/internal/audit"
	"anc/internal/envelope"
	"anc/internal/hot"
	"anc/internal/org"
	"anc/internal/timeline"
)

// QueuePrefix 是待批条目在热层里的 id 前缀。
//
// 为什么要前缀：热层里还躺着「进行中的任务」，两种东西共用一个命名空间，只靠 status
// 分不出来 —— 分不出来就等于没有队列。
const QueuePrefix = "prop:"

// StatusPending 是待批的当前态。词表沿用 timeline（认不出的态照列，见 FromState）。
const StatusPending = "pending"

// KindDecision 是结账落在 timeline 上的 kind（timeline 的约定词，不强卡）。
const KindDecision = "decision"

// StatusDone 用 timeline 的词表原话：这一条已经落定了。
const StatusDone = "done"

// Signal 是「点头」—— 三个**可识别**的信号。
type Signal string

const (
	SignalApprove Signal = "approve"
	SignalReject  Signal = "reject"
	SignalHold    Signal = "hold"
)

// Signals 顺序固定：提示、用例、对拍都照这一份，不再各排一遍。
var Signals = []Signal{SignalApprove, SignalReject, SignalHold}

// ParseSignal 只认这三个字（大小写与前后空白不算数）。**其余一律不认，不猜。**
func ParseSignal(s string) (Signal, bool) {
	for _, k := range Signals {
		if strings.EqualFold(strings.TrimSpace(s), string(k)) {
			return k, true
		}
	}
	return "", false
}

// Verdict 是信号的人话。展示用；判据永远只看 Signal 这三个字。
func (s Signal) Verdict() string {
	switch s {
	case SignalApprove:
		return "同意"
	case SignalReject:
		return "驳回"
	case SignalHold:
		return "挂起"
	}
	return string(s)
}

// Pending 是一条待批提案。
//
// 提案正文**不解析、不截断、不结构化**（2026-10-10 撤回「给提案定结构化字段」那一版：
// 起点是人话；结构只出现在终点 —— grants/ 那七个字段）。
type Pending struct {
	ID       string // 提案 id（信封 id）
	Who      string // 谁提的（bot 名）
	To       string // 该谁批：域表 who 那一列 —— **岗位**，不是人名（人名在展示层现算）
	Domain   string // 客体：哪块业务
	Project  string // 客体：哪个项目
	Title    string // 队列里那一行的一句话（含「代谁」）
	Body     string // 提案原文
	Enqueued string // RFC3339：什么时候进的队列
	Status   string // 当前态；从热层读回来是原样，不在这里改写
}

// ProposalTitle 是队列里的一句话。**带上「代谁」** —— 审批的人第一眼要知道是谁在要权。
func ProposalTitle(who, onBehalfOf, domain, project string) string {
	subj := firstNonEmpty(onBehalfOf, who, "（没写谁）")
	where := firstNonEmpty(domain, project, "（没写哪块业务）")
	return subj + " 申请 " + where + " 的权"
}

// QueueID 是待批在热层里的 id（热层自己还会再加一层 key 前缀）。
func QueueID(proposalID string) string { return QueuePrefix + strings.TrimSpace(proposalID) }

// ProposalID 把热层 id 还原成提案 id；不属于本队列的返回 false。
func ProposalID(queueID string) (string, bool) {
	id := strings.TrimSpace(queueID)
	if !strings.HasPrefix(id, QueuePrefix) {
		return "", false
	}
	if id = strings.TrimPrefix(id, QueuePrefix); id == "" {
		return "", false
	}
	return id, true
}

// FromEnvelope 把一份提案信封变成待批条目。
//
// **只收 proposal**：ask / report / notify / ingest 的去处都不是「等人点头」，
// 收进来只会让队列变成一个什么都往里塞的筐。
func FromEnvelope(e envelope.Envelope, at time.Time) (Pending, error) {
	if !strings.EqualFold(strings.TrimSpace(e.Kind), envelope.KindProposal) {
		return Pending{}, fmt.Errorf("只收 kind: %s 的信封 —— 这一份是 %q", envelope.KindProposal, e.Kind)
	}
	id := strings.TrimSpace(e.ID)
	if id == "" {
		return Pending{}, errors.New("信封没有 id —— 队列靠它指认，没有 id 就点不了头")
	}
	return Pending{
		ID:       id,
		Who:      strings.TrimSpace(e.Who),
		Domain:   strings.TrimSpace(e.Scope.Domain),
		Project:  strings.TrimSpace(e.Scope.Project),
		Title:    ProposalTitle(e.Who, e.OnBehalfOf, e.Scope.Domain, e.Scope.Project),
		Body:     e.Body,
		Enqueued: at.Format(time.RFC3339),
		Status:   StatusPending,
	}, nil
}

// Approver 从域表解出「该谁批」：批的人 = 该业务在域表里写的那个**岗**（口径已定 ——
// 域表只答「找谁」，不答「谁有权批」，所以这里解出来的是岗位，人名由展示层现算）。
//
// 返回 (岗位, 说明)：解不出岗位时，说明里写为什么。队列里宁可带一句「不知道该谁批」，
// 也不要一个空的 to 让人以为只是没填。
func Approver(o *org.Org, domain string) (string, string) {
	slug := strings.TrimSpace(domain)
	if slug == "" {
		return "", "信封没写 scope.domain —— 不知道该谁批"
	}
	if o == nil {
		return "", "没加载到 org 真相源 —— 不知道该谁批"
	}
	d, ok := o.Domain(slug)
	if !ok {
		return "", fmt.Sprintf("域表里没有 %q 这个域 —— 不知道该谁批", slug)
	}
	who := strings.TrimSpace(d.Who)
	if who == "" {
		return "", fmt.Sprintf("域 %q 的 who 那一列是空的 —— 不知道该谁批", slug)
	}
	return who, ""
}

// Queue 是本包要的热层那几个动作。`*hot.Store` 满足它 ——
// 接口留着只为一件事：用例能用一个假队列把「先落库、再清热层」这条顺序测出来，不必起 Redis。
type Queue interface {
	Get(id string) (hot.State, bool, error)
	Put(s hot.State) (hot.State, error)
	Drop(id string) error
	List() ([]hot.State, error)
}

// ErrNotQueued 是「这条提案不在待批队列里」：要么没提过，要么已经结过账。
var ErrNotQueued = errors.New("待批队列里没有这条提案")

// State 转成热层那条。Note 放**提案原文** —— 队列里必须能直接读到人话，
// 否则审批的人还得回去翻信封，中间就多了一次「猜」。
func (p Pending) State() hot.State {
	status := strings.TrimSpace(p.Status)
	if status == "" {
		status = StatusPending
	}
	return hot.State{
		ID:      QueueID(p.ID),
		Status:  status,
		By:      p.Who,
		To:      p.To,
		Domain:  p.Domain,
		Project: p.Project,
		Title:   p.Title,
		Note:    p.Body,
		AsOf:    p.Enqueued,
	}
}

// FromState 是 State 的逆。
//
// **status 不是 pending 的照收** —— 队列里出现别的态（有人手工改过），宁可列出来让人看见：
// 静默丢掉的那一条，在审批人眼里就是「压根没提过」。
func FromState(s hot.State) (Pending, error) {
	id, ok := ProposalID(s.ID)
	if !ok {
		return Pending{}, fmt.Errorf("热层条目 %q 不在待批队列里（前缀 %q）", s.ID, QueuePrefix)
	}
	return Pending{
		ID:       id,
		Who:      s.By,
		To:       s.To,
		Domain:   s.Domain,
		Project:  s.Project,
		Title:    s.Title,
		Body:     s.Note,
		Enqueued: s.AsOf,
		Status:   s.Status,
	}, nil
}

// Enqueue 把提案放进队列。
//
// **已经在队里就不重复入队**（幂等），并把队里那条原样回给调用方 ——
// 重入会把「等了多久」的时间戳冲掉，让人以为这是刚提的。
func Enqueue(q Queue, p Pending) (Pending, bool, error) {
	if q == nil {
		return Pending{}, false, errors.New("没有队列 —— 热层没接上")
	}
	if s, ok, err := q.Get(QueueID(p.ID)); err != nil {
		return Pending{}, false, err
	} else if ok {
		cur, err := FromState(s)
		if err != nil {
			return Pending{}, false, err
		}
		return cur, false, nil
	}
	saved, err := q.Put(p.State())
	if err != nil {
		return Pending{}, false, err
	}
	out, err := FromState(saved)
	if err != nil {
		return Pending{}, false, err
	}
	return out, true, nil
}

// ListPending 从热层里挑出待批的那几条，按「进队时间」从早到晚。
// **不按 status 过滤**：前缀就是队列的边界（理由见 FromState）。
func ListPending(q Queue) ([]Pending, error) {
	if q == nil {
		return nil, errors.New("没有队列 —— 热层没接上")
	}
	list, err := q.List()
	if err != nil {
		return nil, err
	}
	out := make([]Pending, 0, len(list))
	for _, s := range list {
		p, err := FromState(s)
		if err != nil {
			continue // 不是这个队列的（进行中的任务躺在一起），不算错
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return earlier(out[i].Enqueued, out[j].Enqueued) })
	return out, nil
}

// earlier 比两个 RFC3339 时刻。**读不懂的排后面**（宁可疑，不静默当成最早或最晚）。
func earlier(a, b string) bool {
	ta, oka := parseStamp(a)
	tb, okb := parseStamp(b)
	switch {
	case oka && okb:
		return ta.Before(tb)
	case oka:
		return true
	case okb:
		return false
	}
	return a < b
}

func parseStamp(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	return t, err == nil
}

// Decision 是一次点头。
type Decision struct {
	ProposalID string
	By         string // 谁点的：open_id / 人名 / bot 名 —— **原样**（先留痕，不校）
	Signal     Signal
	Why        string
	// Via 是**这一次点头是怎么来的**：哪个前端、落在哪台 bot、原始的平台标识
	// （看板来的通常是空的；飞书卡片那条回程由 CLI 填）。它进审计那一行的 Detail ——
	// 「谁在哪儿点的头」是行使记录的一部分，事后要能一路回到那一张卡片。
	Via string
	At  time.Time
}

// New 建一次点头，并把该拦的拦在这里。
//
// **必带 who**：匿名点头等于没点头 —— 留痕的第一性问题就是谁点的。
func New(proposalID, by string, sig Signal, why string, at time.Time) (Decision, error) {
	id := strings.TrimSpace(proposalID)
	if id == "" {
		return Decision{}, errors.New("缺少提案 id —— 点头要指认是对哪一条点的")
	}
	who := strings.TrimSpace(by)
	if who == "" {
		return Decision{}, errors.New("缺少 -by —— 匿名点头等于没点头")
	}
	if _, ok := ParseSignal(string(sig)); !ok {
		return Decision{}, fmt.Errorf("认不出的信号 %q —— 只认 %s", sig, signalList())
	}
	if at.IsZero() {
		at = time.Now()
	}
	return Decision{
		ProposalID: id,
		By:         who,
		Signal:     sig,
		Why:        strings.TrimSpace(why),
		At:         at,
	}, nil
}

// signalList 是三个信号连起来的那一句话（提示文案只此一处）。
func signalList() string {
	parts := make([]string, 0, len(Signals))
	for _, s := range Signals {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, " / ")
}

// Verdict 是这次点名的人话（带上 -why）。
func (d Decision) Verdict() string {
	if w := strings.TrimSpace(d.Why); w != "" {
		return d.Signal.Verdict() + "：" + w
	}
	return d.Signal.Verdict()
}

// AuditRecord 是这次点头要落的审计行。
//
// 类别记 `invoke`（read / write / invoke 三个已知类别里，这是**一次行使**）；
// 三个信号都算 `ok` —— `denied` 是「我这次行使被拒了」，而驳回是**决定的内容**，
// 不是这次行使的结果，所以它写在 Why / Detail 里，不写在 Result 里。
func (d Decision) AuditRecord(p Pending) audit.Record {
	return audit.Record{
		ID:     "approval-" + p.ID + "-" + string(d.Signal),
		At:     d.At.Format(time.RFC3339),
		Actor:  d.By,
		Action: audit.ActionInvoke,
		Object: firstNonEmpty(p.Domain, p.Project),
		Result: audit.ResultOK,
		Why:    d.Verdict(),
		Tool:   "anc approvals",
		Source: "manual",
		Detail: "提案 " + p.ID + "：" + p.Title + viaLine(d.Via),
	}
}

// viaLine 把「怎么来的」缀在审计那一行后面。空 = 不加任何东西（**不写「（无）」**：
// 一个没有来源说明的行，比一行写着「来源：无」的更容易看出是没记，而不是记了个「无」）。
func viaLine(via string) string {
	if s := strings.TrimSpace(via); s != "" {
		return " · 经 " + s
	}
	return ""
}

// TimelineEntry 是这次点头要落的那条库行。
//
// case 用**提案 id**：一份 id 贯穿热层、审计、时间线 —— 事后要查「这条权怎么来的」，
// 一个 id 就能把三段串起来。
func (d Decision) TimelineEntry(p Pending) timeline.Entry {
	return timeline.Entry{
		ID:      "approval-" + p.ID + "-" + string(d.Signal),
		Case:    p.ID,
		At:      d.At.Format(time.RFC3339),
		Kind:    KindDecision,
		Status:  StatusDone,
		By:      d.By,
		To:      p.To,
		Domain:  p.Domain,
		Project: p.Project,
		Title:   p.Title + " —— " + d.Verdict(),
		Detail:  p.Body,
	}
}

// Receipt 是结账的结果（给人看、也给日志留）。
type Receipt struct {
	Pending  Pending
	Decision Decision
	Timeline string // 落库那一行写到了哪个文件
	Audit    string // 审计那一行写到了哪个文件
	At       string
}

// Settle 是「结账」：**先落库、再清热层**（反序就会丢东西 —— hot 的口径）。
//
// 不在队列里就报 ErrNotQueued：那意味着要么没提过，要么已经结过账。这一条**不能静默成功** ——
// 否则一次点头会变成两条痕，或者干脆没有痕。
func Settle(q Queue, vault string, d Decision) (Receipt, error) {
	if q == nil {
		return Receipt{}, errors.New("没有队列 —— 热层没接上")
	}
	if strings.TrimSpace(vault) == "" {
		return Receipt{}, errors.New("缺少 vault —— 落库与审计都要写进真相源")
	}
	s, ok, err := q.Get(QueueID(d.ProposalID))
	if err != nil {
		return Receipt{}, err
	}
	if !ok {
		return Receipt{}, fmt.Errorf("%w：%s —— 要么没提过，要么已经结过账（看 timeline 里 case=%s 的行）",
			ErrNotQueued, d.ProposalID, d.ProposalID)
	}
	p, err := FromState(s)
	if err != nil {
		return Receipt{}, err
	}
	entry := d.TimelineEntry(p)
	tlPath, err := timeline.Append(vault, entry)
	if err != nil {
		return Receipt{}, fmt.Errorf("落库失败，热层没动（先落库、再清热层）：%w", err)
	}
	rec := d.AuditRecord(p)
	auditPath, err := audit.Append(vault, rec)
	if err != nil {
		return Receipt{}, fmt.Errorf("库落了（%s）但审计没落 —— 热层没动：补上审计后重跑：%w", tlPath, err)
	}
	if err := q.Drop(QueueID(d.ProposalID)); err != nil {
		return Receipt{}, fmt.Errorf("库与审计都落了（%s / %s），热层没清掉 —— 清掉前别再点：%w",
			tlPath, auditPath, err)
	}
	return Receipt{
		Pending:  p,
		Decision: d,
		Timeline: tlPath,
		Audit:    auditPath,
		At:       d.At.Format(time.RFC3339),
	}, nil
}

// firstNonEmpty 取第一个非空。本包只用它，不再往 main 里借。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

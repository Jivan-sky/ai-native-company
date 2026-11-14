// Package tasksettle —— 「达标」那一步：**先落库，再清热层**。
//
// 为什么单独一个包：CLI（`anc task settle`）、MCP（`anc_task action=settle`）、看板
// （`POST /api/tasks/settle`）是同一件事的三个前端。三个前端必须走**同一个**入口，
// 否则「结账」的规则迟早各写一份：一边允许把 running 落库，另一边不允许。
package tasksettle

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"anc/internal/hot"
	"anc/internal/timeline"
)

// ErrOpen：任务还没结束（queued / running）就有人在结账。
// 单独一个哨兵，是为了让三个前端都不用靠错误文案去猜「这是不是「还没做完」那一种」。
var ErrOpen = errors.New("任务还没结束")

// Store 是「结账」要的两件事：读当前值、清走它。
// 没有 pull / ack —— 结账不改状态，改状态是回执那一步的事。
type Store interface {
	Get(id string) (hot.State, bool, error)
	// DropCAS 只在热层版本仍等于 version 时才清。返回 false = 没清（版本变了或本来没有）。
	// 为什么不用「先 Get 再 Drop」：那中间正好是交接落地的窗口。
	DropCAS(id string, version int64) (bool, error)
}

type Opts struct {
	By     string   // 谁交的；空 = 任务的执行者（To）
	Title  string   // 空 = 用任务标题
	Status string   // 空 = 用热层里的当前态；给值 = 这次结账的判定
	Kind   string   // 默认 execution
	Note   string   // 结账说明，接在任务正文后面
	Refs   []string // 文件 / 链接
	At     time.Time
	// ExpectedVersion 是调用者看到的版本（0 = 不校验，兼容旧客户端）。
	// 结账要凭它判「这一刀是不是基于已经过期的现场」。
	ExpectedVersion int64
}

// ErrStale：请求基于的版本已经不是当前版本（中间有人交接 / 改状态）。
// 单列一个哨兵：三个前端要把它和「还没做完」分开报。
var ErrStale = errors.New("现场已经变了")

type Result struct {
	Task    hot.State
	Entry   timeline.Entry
	Path    string // 这次写到了哪个文件；Already = true 时为空
	Already bool   // timeline 里已有同 case 的行，本次没重复写
}

// Settle 是「达标」那一步：**先落库，再清热层**。
//
// 三条口径：
//   - 还在 queued / running 的条目默认不结账：那还是「进行中」，
//     结账等于把没做完的事从看板抹掉。真要终止，显式给 status（cancelled / failed …），
//     让「这是谁的决定」留在记录里。
//   - 幂等：timeline 里已有同 case 的行就不再追加；热层还在就顺手清掉。
//     重复调用不会制造第二条痕。
//   - 落库失败时热层一个字都不动 —— 此时它还是唯一的记录。
func Settle(st Store, vault, id string, o Opts) (Result, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Result{}, errors.New("结账需要任务 id")
	}
	if st == nil {
		return Result{}, errors.New("热层没接上 —— 任务状态读不到")
	}
	if strings.TrimSpace(vault) == "" {
		return Result{}, errors.New("缺少 vault —— 落库要写进真相源")
	}
	if o.At.IsZero() {
		o.At = time.Now()
	}

	t, found, err := st.Get(id)
	if err != nil {
		return Result{}, err
	}

	prev, already, err := settledEntry(vault, id)
	if err != nil {
		return Result{}, err
	}
	if already {
		res := Result{Task: t, Entry: prev, Already: true}
		if found {
			// 只补清「我读到的那一版」：中间若有人交接，别把新的一版删掉。
			if _, err := st.DropCAS(id, t.Version); err != nil {
				return res, fmt.Errorf("这条早已沉淀；热层没清掉 —— 重跑 settle 只补清这一刀：%w", err)
			}
		}
		return res, nil
	}
	if !found {
		return Result{}, fmt.Errorf("%w：%s", hot.ErrNotFound, id)
	}

	// 版本先行：这一刀是不是基于已经过期的现场。
	if o.ExpectedVersion > 0 && found && t.Version != o.ExpectedVersion {
		return Result{}, fmt.Errorf("%w：你看到的是 v%d，现在是 v%d —— 重新读一遍再决定",
			ErrStale, o.ExpectedVersion, t.Version)
	}

	status := firstNonEmpty(o.Status, t.Status)
	if strings.TrimSpace(status) == "" {
		return Result{}, errors.New("这条没有状态 —— 先回执，或显式给 status")
	}
	stage := strings.TrimSpace(t.Stage)
	if stage == "" {
		stage = hot.DeriveStage(t.Status)
	}
	if strings.TrimSpace(o.Status) == "" {
		switch stage {
		case hot.StageQueued:
			return Result{}, fmt.Errorf(
				"%w：任务 %s 还在队列里（queued）—— 还没人拉走。要终止就显式给 status=cancelled（或其他终态）", ErrOpen, id)
		case hot.StageRunning, hot.StageReview, hot.StageBlocked:
			return Result{}, fmt.Errorf(
				"%w：任务 %s 还在进行中（%s）—— 还没做完的事不落库。先回执，或显式给 status 说明这是谁的决定", ErrOpen, id, stage)
		}
	}

	title := firstNonEmpty(o.Title, t.Title)
	if title == "" {
		return Result{}, errors.New("这条没有标题 —— 用 title 给一句（落库的行要能被人看懂）")
	}
	by := firstNonEmpty(o.By, t.To, t.By)
	if by == "" {
		return Result{}, errors.New("不知道是谁交的 —— 用 by 给一个身份")
	}
	kind := firstNonEmpty(o.Kind, "execution")

	ent := timeline.Entry{
		ID:      "task-" + id,
		Case:    id,
		At:      o.At.Format(time.RFC3339),
		Kind:    kind,
		Status:  status,
		By:      by,
		To:      t.To,
		Domain:  t.Domain,
		Project: t.Project,
		Title:   title,
		Detail:  detail(t, o.Note),
		Refs:    cleanRefs(o.Refs),
	}
	path, err := timeline.Append(vault, ent)
	if err != nil {
		return Result{}, fmt.Errorf("落库失败，热层没动（先落库、再清热层）：%w", err)
	}
	res := Result{Task: t, Entry: ent, Path: path}
	dropped, derr := st.DropCAS(id, t.Version)
	if derr != nil {
		return res, fmt.Errorf("库已落（%s），热层没清掉 —— 重跑 settle 只会补清这一刀：%w", path, derr)
	}
	if !dropped {
		// 落库之后才发现版本变了：终态行已经写了（事实不能撤），但热层那一条是别人的现场。
		// 如实说清，不假装清干净了。
		return res, fmt.Errorf("库已落（%s），但热层那一条已被别人改动（不是 v%d）—— 没清，去核一下谁在办：%w",
			path, t.Version, ErrStale)
	}
	return res, nil
}

// Receipt 找这条任务的**结账回执**（timeline 里 `task-<id>` 那一行）。
//
// 导出它是为了让别的写入口也能问一句「这条结过账没有」——
// 交接就是这样一个入口：账上已经落了取消，热层里再换个人接手，就是「抹掉一个已经做出的决定」。
func Receipt(vault, id string) (timeline.Entry, bool, error) {
	return settledEntry(vault, id)
}

// settledEntry 找 timeline 里已经沉淀过的同 case 行。
// 读不出来就报错：宁可这一刀不下，也不要在「不知道有没有」的时候追加重复记录。
func settledEntry(vault, id string) (timeline.Entry, bool, error) {
	doc, err := timeline.Load(vault)
	if err != nil {
		return timeline.Entry{}, false, err
	}
	// **只认结账行自己的身份**（`task-<id>`），不认「同一个 case 下的任何一行」。
	// 之前按 case 匹配，结果一条同案进度备注就能让 settle 判成「早已沉淀」，
	// 然后把还在跑的任务从热层删掉 —— 备注把活干掉了。
	want := "task-" + id
	for _, e := range doc.Entries {
		if strings.TrimSpace(e.ID) == want {
			return e, true, nil
		}
	}
	return timeline.Entry{}, false, nil
}

// detail 把「任务正文 + 会话归属 + 版本」拼成一段可读的展开。
// 空项不写，不留「会话：」这种空壳。
func detail(t hot.State, extra string) string {
	var parts []string
	if s := strings.TrimSpace(t.Note); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(extra); s != "" {
		parts = append(parts, s)
	}
	var meta []string
	if s := strings.TrimSpace(t.Session); s != "" {
		meta = append(meta, "会话 "+s)
	}
	if s := strings.TrimSpace(t.Intent); s != "" {
		meta = append(meta, "意图 "+s)
	}
	if s := strings.TrimSpace(t.From); s != "" {
		meta = append(meta, "派单 "+s)
	}
	if t.Version > 0 {
		meta = append(meta, fmt.Sprintf("v%d", t.Version))
	}
	if s := strings.TrimSpace(t.AsOf); s != "" {
		meta = append(meta, "最后状态 "+s)
	}
	if len(meta) > 0 {
		parts = append(parts, strings.Join(meta, " · "))
	}
	return strings.Join(parts, "\n")
}

func cleanRefs(in []string) []string {
	var out []string
	for _, r := range in {
		if s := strings.TrimSpace(r); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

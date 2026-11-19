// Package casework —— 卡点（Blocker）与突破（Breakthrough）两类对象。
//
// 为什么它们必须是对象，而不是"时间线里的一个 stage 和一句话"：
//
//	飞轮缺它们 → 蒸馏只能从"结果"猜过程；
//	调派缺它们 → 「这个坑踩了几次」「谁解开的」「代价多大」都答不出来。
//
// 一条纪律：**卡点与突破都只追加，不改写**。
// 卡点被解开是「状态变了 + 记一条突破」，不是把卡点删掉 —— 删掉就没有"踩过几次"。
package casework

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	BlockerDir      = "blockers"
	BreakthroughDir = "breakthroughs"

	BlockerOpen     = "open"
	BlockerResolved = "resolved"
	BlockerDropped  = "dropped" // 不解决也能结束：明确放弃也是结论
)

// Blocker 是一次阻塞。
type Blocker struct {
	ID         string // 稳定 id（文件名）
	Case       string // 归属的 case / 任务 id
	Title      string // 一句话：卡在哪
	Kind       string // 类别（口径 / 权限 / 工具 / 人 / 外部），**归组用**，答"踩了几次"靠它
	Domain     string // 业务域
	Stage      string // 卡在流程哪一步（与 hot 的 stage 同一套词）
	WaitingOn  string // 在等谁 / 等什么
	Since      string // 从什么时候开始卡（RFC3339）
	Status     string // open | resolved | dropped
	ResolvedBy string // 谁解的（人名 / agent slug）
	ResolvedAt string
	Why        string   // 展开：现象与上下文
	From       []string // 出处：事件 id / 文件:行号
}

// Breakthrough 是一次突破：它**必须指向一个卡点**，否则就是一句无法验证的心得。
type Breakthrough struct {
	ID        string
	BlockerID string
	Method    string // 怎么解的（要能被人照着再做一遍）
	Cost      string // 代价：工时 / 返工 / 钱
	Reusable  bool   // 能不能复用（能进飞轮的蒸馏候选）
	By        string
	At        string
	Scope     string // 适用于哪类任务；空 = 未声明
	Evidence  []string
}

func dirOf(vault, name string) string { return filepath.Join(vault, name) }

// AddBlocker 建一条卡点。
func AddBlocker(vault string, b Blocker) (Blocker, error) {
	b.ID = strings.TrimSpace(b.ID)
	if b.ID == "" {
		b.ID = "blk-" + time.Now().UTC().Format("20060102-150405")
	}
	if strings.ContainsAny(b.ID, "/\\ ") {
		return Blocker{}, fmt.Errorf("id %q 不能带路径分隔符或空格", b.ID)
	}
	if strings.TrimSpace(b.Title) == "" {
		return Blocker{}, errors.New("title 不能为空 —— 一条说不清卡在哪的卡点，事后没人看得懂")
	}
	if strings.TrimSpace(b.Kind) == "" {
		return Blocker{}, errors.New("kind 不能为空 —— 没有类别的卡点无法归组，「这个坑踩了几次」就答不出来")
	}
	if b.Since == "" {
		b.Since = time.Now().Format(time.RFC3339)
	}
	b.Status = BlockerOpen
	if err := writeMD(vault, BlockerDir, b.ID, renderBlocker(b)); err != nil {
		return Blocker{}, err
	}
	return b, nil
}

// Resolve 把卡点标成已解决。**不删记录**：删掉就没有"踩过几次"。
func Resolve(vault, id, by string) (Blocker, error) {
	b, err := GetBlocker(vault, id)
	if err != nil {
		return Blocker{}, err
	}
	if b.Status != BlockerOpen {
		return Blocker{}, fmt.Errorf("这条卡点已经是 %s —— 解不开的是新卡点，另开一条", b.Status)
	}
	if strings.TrimSpace(by) == "" {
		return Blocker{}, errors.New("resolve 必须写 --by（谁解的）")
	}
	b.Status, b.ResolvedBy, b.ResolvedAt = BlockerResolved, by, time.Now().Format(time.RFC3339)
	if err := writeMD(vault, BlockerDir, b.ID, renderBlocker(b)); err != nil {
		return Blocker{}, err
	}
	return b, nil
}

// AddBreakthrough 记一次突破。必须指向一个真实存在的卡点。
func AddBreakthrough(vault string, bt Breakthrough) (Breakthrough, error) {
	if strings.TrimSpace(bt.BlockerID) == "" {
		return Breakthrough{}, errors.New("breakthrough 必须写 --blocker —— 不指向卡点的「突破」无法验证")
	}
	if _, err := GetBlocker(vault, bt.BlockerID); err != nil {
		return Breakthrough{}, fmt.Errorf("找不到卡点 %q —— 突破要挂在真实卡点上", bt.BlockerID)
	}
	if strings.TrimSpace(bt.Method) == "" {
		return Breakthrough{}, errors.New("method 不能为空 —— 要能被人照着再做一遍")
	}
	bt.ID = strings.TrimSpace(bt.ID)
	if bt.ID == "" {
		bt.ID = "btw-" + time.Now().UTC().Format("20060102-150405")
	}
	bt.At = time.Now().Format(time.RFC3339)
	if err := writeMD(vault, BreakthroughDir, bt.ID, renderBreakthrough(bt)); err != nil {
		return Breakthrough{}, err
	}
	return bt, nil
}

// GetBlocker 读一条卡点。
func GetBlocker(vault, id string) (Blocker, error) {
	b, err := os.ReadFile(filepath.Join(dirOf(vault, BlockerDir), id+".md"))
	if err != nil {
		return Blocker{}, fmt.Errorf("没有这条卡点：%s", id)
	}
	return parseBlocker(string(b))
}

// ListBlockers 列出全部，按 id 排序（可重放）。
func ListBlockers(vault string) ([]Blocker, error) {
	return listBlockers(vault, func(Blocker) bool { return true })
}

// ListBreakthroughs 列出全部突破。
func ListBreakthroughs(vault string) ([]Breakthrough, error) {
	entries, err := os.ReadDir(dirOf(vault, BreakthroughDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Breakthrough
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dirOf(vault, BreakthroughDir), e.Name()))
		if err != nil {
			continue
		}
		if bt, err := parseBreakthrough(string(b)); err == nil {
			out = append(out, bt)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Recurring 回答「这个坑踩了几次」：按 (域, 类别) 归组，只算真实的卡点条目。
//
// 为什么用 (域, 类别) 而不是标题相似度：相似度阈值需要调参，调不准就变成"看着像就算"。
// 域 + 类别是人能自己填、也能自己核的 —— 宁可粗一点，也不要一个解释不清的聚类。
type RecurGroup struct {
	Domain     string
	Kind       string
	Count      int
	BlockerIDs []string
	Solved     int
}

func Recurring(vault string) ([]RecurGroup, error) {
	all, err := ListBlockers(vault)
	if err != nil {
		return nil, err
	}
	idx := map[string]*RecurGroup{}
	for _, b := range all {
		k := b.Domain + "|" + b.Kind
		g, ok := idx[k]
		if !ok {
			g = &RecurGroup{Domain: b.Domain, Kind: b.Kind}
			idx[k] = g
		}
		g.Count++
		g.BlockerIDs = append(g.BlockerIDs, b.ID)
		if b.Status == BlockerResolved {
			g.Solved++
		}
	}
	out := make([]RecurGroup, 0, len(idx))
	for _, g := range idx {
		out = append(out, *g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

// ── 存储 ────────────────────────────────────────────────────────────────

func writeMD(vault, dir, id, body string) error {
	d := dirOf(vault, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(d, id+".md")
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func renderBlocker(b Blocker) string {
	var s strings.Builder
	s.WriteString("---\n")
	fmt.Fprintf(&s, "id: %s\nstatus: %s\nkind: %s\n", b.ID, b.Status, b.Kind)
	if b.Case != "" {
		fmt.Fprintf(&s, "case: %s\n", b.Case)
	}
	if b.Domain != "" {
		fmt.Fprintf(&s, "domain: %s\n", b.Domain)
	}
	if b.Stage != "" {
		fmt.Fprintf(&s, "stage: %s\n", b.Stage)
	}
	if b.WaitingOn != "" {
		fmt.Fprintf(&s, "waiting_on: %s\n", b.WaitingOn)
	}
	fmt.Fprintf(&s, "since: %s\n", b.Since)
	if b.ResolvedBy != "" {
		fmt.Fprintf(&s, "resolved_by: %s\nresolved_at: %s\n", b.ResolvedBy, b.ResolvedAt)
	}
	if len(b.From) > 0 {
		fmt.Fprintf(&s, "from: %s\n", strings.Join(b.From, ", "))
	}
	s.WriteString("---\n\n")
	fmt.Fprintf(&s, "# %s\n", b.Title)
	if b.Why != "" {
		fmt.Fprintf(&s, "\n%s\n", b.Why)
	}
	return s.String()
}

func renderBreakthrough(bt Breakthrough) string {
	var s strings.Builder
	s.WriteString("---\n")
	fmt.Fprintf(&s, "id: %s\nblocker: %s\nat: %s\n", bt.ID, bt.BlockerID, bt.At)
	if bt.By != "" {
		fmt.Fprintf(&s, "by: %s\n", bt.By)
	}
	if bt.Cost != "" {
		fmt.Fprintf(&s, "cost: %s\n", bt.Cost)
	}
	if bt.Scope != "" {
		fmt.Fprintf(&s, "scope: %s\n", bt.Scope)
	}
	fmt.Fprintf(&s, "reusable: %t\n", bt.Reusable)
	if len(bt.Evidence) > 0 {
		fmt.Fprintf(&s, "evidence: %s\n", strings.Join(bt.Evidence, ", "))
	}
	s.WriteString("---\n\n")
	fmt.Fprintf(&s, "## 怎么解的\n\n%s\n", bt.Method)
	return s.String()
}

func listBlockers(vault string, keep func(Blocker) bool) ([]Blocker, error) {
	entries, err := os.ReadDir(dirOf(vault, BlockerDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Blocker
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dirOf(vault, BlockerDir), e.Name()))
		if err != nil {
			continue
		}
		if blk, err := parseBlocker(string(b)); err == nil && keep(blk) {
			out = append(out, blk)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// 极简 frontmatter：这一层不引 YAML（零依赖约束，与 exp 同一决定）。
func splitFront(s string) (map[string]string, string, error) {
	if !strings.HasPrefix(s, "---\n") {
		return nil, "", errors.New("缺 frontmatter")
	}
	body := s[4:]
	end := strings.Index(body, "\n---\n")
	if end < 0 {
		return nil, "", errors.New("frontmatter 没有结束标记")
	}
	m := map[string]string{}
	for _, line := range strings.Split(body[:end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return m, body[end+5:], nil
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parseBlocker(s string) (Blocker, error) {
	m, body, err := splitFront(s)
	if err != nil {
		return Blocker{}, err
	}
	b := Blocker{ID: m["id"], Status: m["status"], Kind: m["kind"], Case: m["case"],
		Domain: m["domain"], Stage: m["stage"], WaitingOn: m["waiting_on"], Since: m["since"],
		ResolvedBy: m["resolved_by"], ResolvedAt: m["resolved_at"], From: splitList(m["from"])}
	if b.ID == "" {
		return b, errors.New("frontmatter 缺 id")
	}
	body = strings.TrimSpace(body)
	if i := strings.Index(body, "\n"); i > 0 {
		b.Title = strings.TrimSpace(strings.TrimPrefix(body[:i], "# "))
		b.Why = strings.TrimSpace(body[i:])
	} else {
		b.Title = strings.TrimSpace(strings.TrimPrefix(body, "# "))
	}
	return b, nil
}

func parseBreakthrough(s string) (Breakthrough, error) {
	m, body, err := splitFront(s)
	if err != nil {
		return Breakthrough{}, err
	}
	bt := Breakthrough{ID: m["id"], BlockerID: m["blocker"], At: m["at"], By: m["by"],
		Cost: m["cost"], Scope: m["scope"], Reusable: m["reusable"] == "true", Evidence: splitList(m["evidence"])}
	if bt.ID == "" {
		return bt, errors.New("frontmatter 缺 id")
	}
	bt.Method = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(body), "## 怎么解的"))
	return bt, nil
}

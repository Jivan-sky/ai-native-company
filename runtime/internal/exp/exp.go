// Package exp —— 经验对象：从「一次决策/卡点/突破」里提炼出来、**要改 Agent 行为**的条目。
//
// 为什么单独成对象（治理判据 7/8/12）：
//   - 判据 7：经验未经人工确认不得生效。第一版根本没有对象，所谓"部署"是把草稿 cp 成技能文件，
//     等于生效通道不受管 —— 不是被拦住了，是没人管。
//   - 判据 8：部署可回滚。要回滚就得有版本、有部署台账、有 digest。
//   - 判据 12：飞轮要有出口。没有否决与废止，三个月后规则库变成没人敢删的沼泽。
//
// 状态机（只能往前走，回退一律留痕）：
//
//	candidate ──approve──▶ approved ──deploy──▶ deployed ──retire──▶ retired
//	    └──────────reject──▶ rejected（可 revive 回 candidate）
//
// 落点：<vault>/experiences/<id>.md（frontmatter 是事实），
//
//	<vault>/experiences/ledger.jsonl（追加式台账：谁在什么时候做了什么，带 digest）。
package exp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	Dir             = "experiences"
	Ledger          = "ledger.jsonl"
	StatusCandidate = "candidate"
	StatusApproved  = "approved"
	StatusDeployed  = "deployed"
	StatusRetired   = "retired"
	StatusRejected  = "rejected"
)

// Experience 是一条经验。
type Experience struct {
	ID         string // 稳定 id（文件名），不可复用
	Title      string
	Claim      string   // 一句话结论（要能直接被 Agent 读）
	Scope      string   // 适用范围：哪类任务/哪个域；空 = 未声明（部署时要求填）
	From       []string // 出处：事件 id / 文件:行号（判据 1 的血缘）
	Evidence   []string // 证据：支撑这条结论的具体事实
	Status     string
	By         string // 谁提的
	ApprovedBy string
	At         string // 最近一次状态变更
	Version    int    // 部署版本：每次 deploy +1
	Note       string
}

// 台账的一行。
type Entry struct {
	At         string `json:"at"`
	Action     string `json:"action"` // add|approve|deploy|reject|revive|retire
	ID         string `json:"id"`
	By         string `json:"by"`
	Why        string `json:"why,omitempty"`
	FromStatus string `json:"from_status,omitempty"`
	ToStatus   string `json:"to_status"`
	Version    int    `json:"version,omitempty"`
	Digest     string `json:"digest,omitempty"` // 部署时的内容指纹：回滚要能对得上
}

var (
	ErrNotFound    = errors.New("没有这条经验")
	ErrBadState    = errors.New("状态不允许这一步")
	ErrNotApproved = errors.New("经验未经人工确认，不得部署")
)

func dirOf(vault string) string { return filepath.Join(vault, Dir) }

// Add 建一条候选。候选**不生效**，只是进池子等人工确认。
func Add(vault string, e Experience) (Experience, error) {
	id := strings.TrimSpace(e.ID)
	if id == "" {
		id = "exp-" + time.Now().UTC().Format("20060102-150405")
	}
	if strings.ContainsAny(id, "/\\ ") {
		return Experience{}, fmt.Errorf("id %q 不能带路径分隔符或空格 —— 它要当文件名", id)
	}
	if strings.TrimSpace(e.Claim) == "" {
		return Experience{}, errors.New("claim 不能为空 —— 一条说不清结论的'经验'不是经验")
	}
	if len(e.From) == 0 {
		return Experience{}, errors.New("from 不能为空 —— 没有出处的经验无法回溯（判据 1）")
	}
	if _, err := os.Stat(filepath.Join(dirOf(vault), id+".md")); err == nil {
		return Experience{}, fmt.Errorf("id %q 已经存在 —— 经验不许覆盖，要改就新增一条并更正旧的", id)
	}
	e.ID, e.Status = id, StatusCandidate
	e.At = time.Now().Format(time.RFC3339)
	if err := save(vault, e); err != nil {
		return Experience{}, err
	}
	if err := appendLedger(vault, Entry{At: e.At, Action: "add", ID: id, By: e.By, ToStatus: e.Status}); err != nil {
		return Experience{}, err
	}
	return e, nil
}

// Approve 是**唯一**能让经验有资格生效的动作，必须留人。
func Approve(vault, id, by, why string) (Experience, error) {
	e, err := Get(vault, id)
	if err != nil {
		return Experience{}, err
	}
	if by = strings.TrimSpace(by); by == "" {
		return Experience{}, errors.New("approve 必须写 --by（谁确认的）")
	}
	if e.Status != StatusCandidate && e.Status != StatusRejected {
		return Experience{}, fmt.Errorf("%w：当前 %s", ErrBadState, e.Status)
	}
	from := e.Status
	e.Status, e.ApprovedBy, e.At = StatusApproved, by, time.Now().Format(time.RFC3339)
	if err := save(vault, e); err != nil {
		return Experience{}, err
	}
	return e, appendLedger(vault, Entry{At: e.At, Action: "approve", ID: id, By: by, Why: why, FromStatus: from, ToStatus: e.Status})
}

// Deploy 让经验真正生效。**未确认一律拒**——这是执行者唯一要守的那条。
func Deploy(vault, id, by string) (Experience, error) {
	e, err := Get(vault, id)
	if err != nil {
		return Experience{}, err
	}
	if e.Status != StatusApproved {
		return Experience{}, fmt.Errorf("%w（当前 %s）—— 先 approve", ErrNotApproved, e.Status)
	}
	if strings.TrimSpace(e.Scope) == "" {
		return Experience{}, errors.New("scope 不能为空 —— 部署前要写清这条经验用在哪，否则会被套到不该用的地方")
	}
	d, err := digest(vault, id)
	if err != nil {
		return Experience{}, err
	}
	from := e.Status
	e.Status, e.Version, e.At = StatusDeployed, e.Version+1, time.Now().Format(time.RFC3339)
	if err := save(vault, e); err != nil {
		return Experience{}, err
	}
	return e, appendLedger(vault, Entry{At: e.At, Action: "deploy", ID: id, By: by, FromStatus: from,
		ToStatus: e.Status, Version: e.Version, Digest: d})
}

// Reject 是否决：进"垃圾池"，但状态可查、也可 revive —— 判据 12 要的出口。
func Reject(vault, id, by, why string) (Experience, error) {
	e, err := Get(vault, id)
	if err != nil {
		return Experience{}, err
	}
	if strings.TrimSpace(why) == "" {
		return Experience{}, errors.New("reject 必须写 --why —— 否决的理由比同意更重要")
	}
	from := e.Status
	e.Status, e.At = StatusRejected, time.Now().Format(time.RFC3339)
	if err := save(vault, e); err != nil {
		return Experience{}, err
	}
	return e, appendLedger(vault, Entry{At: e.At, Action: "reject", ID: id, By: by, Why: why, FromStatus: from, ToStatus: e.Status})
}

// Retire 废止：已生效的东西要能下线，且留理由。
func Retire(vault, id, by, why string) (Experience, error) {
	e, err := Get(vault, id)
	if err != nil {
		return Experience{}, err
	}
	if strings.TrimSpace(why) == "" {
		return Experience{}, errors.New("retire 必须写 --why —— 下线一条已生效的经验，理由必须留在账上")
	}
	from := e.Status
	e.Status, e.At = StatusRetired, time.Now().Format(time.RFC3339)
	if err := save(vault, e); err != nil {
		return Experience{}, err
	}
	return e, appendLedger(vault, Entry{At: e.At, Action: "retire", ID: id, By: by, Why: why, FromStatus: from, ToStatus: e.Status})
}

// Get 读一条。
func Get(vault, id string) (Experience, error) {
	b, err := os.ReadFile(filepath.Join(dirOf(vault), id+".md"))
	if err != nil {
		return Experience{}, ErrNotFound
	}
	return parse(string(b))
}

// List 列出全部，按 id 排序（可重放：同一份目录永远同一个顺序）。
func List(vault string) ([]Experience, error) {
	entries, err := os.ReadDir(dirOf(vault))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Experience
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dirOf(vault), en.Name()))
		if err != nil {
			continue
		}
		if e, err := parse(string(b)); err == nil {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// LedgerOf 读台账（审计与回滚都靠它）。
func LedgerOf(vault string) ([]Entry, error) {
	b, err := os.ReadFile(filepath.Join(dirOf(vault), Ledger))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e Entry
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out, nil
}

func digest(vault, id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dirOf(vault), id+".md"))
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func appendLedger(vault string, e Entry) error {
	if err := os.MkdirAll(dirOf(vault), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dirOf(vault), Ledger), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	_, err = f.Write(append(b, '\n'))
	return err
}

// save 写一条（覆盖同 id）。用临时文件 + rename，避免写一半留下坏文件。
func save(vault string, e Experience) error {
	if err := os.MkdirAll(dirOf(vault), 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dirOf(vault), e.ID+".md")
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, []byte(render(e)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func render(e Experience) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\ntitle: %s\nstatus: %s\n", e.ID, e.Title, e.Status)
	fmt.Fprintf(&b, "scope: %s\nby: %s\n", e.Scope, e.By)
	if e.ApprovedBy != "" {
		fmt.Fprintf(&b, "approved_by: %s\n", e.ApprovedBy)
	}
	fmt.Fprintf(&b, "at: %s\nversion: %d\n", e.At, e.Version)
	fmt.Fprintf(&b, "from: %s\n", strings.Join(e.From, ", "))
	if len(e.Evidence) > 0 {
		fmt.Fprintf(&b, "evidence: %s\n", strings.Join(e.Evidence, ", "))
	}
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n%s\n", e.Title, e.Claim)
	if e.Note != "" {
		fmt.Fprintf(&b, "\n## 备注\n\n%s\n", e.Note)
	}
	return b.String()
}

// parse 读 frontmatter。写法刻意朴素：这一层不引 YAML 依赖（零依赖约束）。
func parse(s string) (Experience, error) {
	var e Experience
	if !strings.HasPrefix(s, "---\n") {
		return e, errors.New("缺 frontmatter")
	}
	body := s[4:]
	end := strings.Index(body, "\n---\n")
	if end < 0 {
		return e, errors.New("frontmatter 没有结束标记")
	}
	for _, line := range strings.Split(body[:end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "id":
			e.ID = v
		case "title":
			e.Title = v
		case "status":
			e.Status = v
		case "scope":
			e.Scope = v
		case "by":
			e.By = v
		case "approved_by":
			e.ApprovedBy = v
		case "at":
			e.At = v
		case "from":
			if v != "" {
				e.From = strings.Split(v, ",")
				for i := range e.From {
					e.From[i] = strings.TrimSpace(e.From[i])
				}
			}
		case "evidence":
			if v != "" {
				e.Evidence = strings.Split(v, ",")
				for i := range e.Evidence {
					e.Evidence[i] = strings.TrimSpace(e.Evidence[i])
				}
			}
		}
	}
	if e.ID == "" {
		return e, errors.New("frontmatter 缺 id")
	}
	return e, nil
}

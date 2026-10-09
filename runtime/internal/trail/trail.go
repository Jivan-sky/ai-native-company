// Package trail 把「谁问了什么、agent 干了什么、花了多少」从两份**现成**记录里聚合出来。
//
// 它是**只读**的：不写、不改、不建任何东西。留痕的写入端是 harness 自己（记录就是账本），
// trail 只把账翻成人看得懂的样子。这条边界和 probe 一样硬 —— 只读命令会被随手跑，
// 一旦有了副作用，「随手跑」就成了风险。
//
// 真相源两份，缺一份都不完整：
//
//  1. cc-connect 的会话落盘 JSON（`<data>/sessions/<project>_<hash>.json`）——
//     哪个 project 的哪个会话槽、对应哪个 harness 会话 id。**这是两份记录之间唯一的桥。**
//  2. harness 原生记录（`<claude-home>/projects/<slug>/<id>.jsonl` 与 `<id>/subagents/*.jsonl`）——
//     逐轮的 token / 工具 / 被拒 / 耗时 / 成本。
//
// 三条口径是**实测**出来的（2026-10-07，把聚合结果与 claude 自己的 cost-state 对拍到逐字节相等，
// 见 runtime/DESIGN.md §7.1.6）。它们都不是「看着像」，是拿数字验过的：
//
//  1. **不按行累加。** 同一条 assistant 消息会写成多行（一行一个 content block，且流式中间态
//     `output_tokens=0`）。按行相加会把账**翻倍**。要按 `message.id` 合并：用量取各次出现的最大值，
//     工具名取并集。
//  2. **子 agent 的账算在这个 bot 头上。** `<id>/subagents/*.jsonl` 是 Agent 工具拉起的旁路会话，
//     token 同样记在本会话账上 —— 不并进来会少报一大截。
//  3. **成本用 harness 自己算的。** cost-state 的 `totalCostUSD` 连「未知模型没有价格」都标了
//     （`hasUnknownModelCost`）—— 我们自己维护价格表只会漂移。
package trail

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"anc/internal/harness"
)

// Schema 是 JSON 出口的版本。
const Schema = "anc.trail/v1"

// maxLine 是单行上限。原生记录的一行可能很大（整段工具输出、附件都塞在行里），
// bufio.Scanner 默认 64KB 会直接报错 —— 那是**读不到**，不是「没有」。
const maxLine = 16 << 20

// promptHead 是提示语在输出里的截断长度（原话在记录里，这里只给人一眼认出是哪一轮）。
const promptHead = 80

// Slug 是 harness 给项目目录起名的规则。实测（2026-10-07）：
//
//	D:\work\anc-demo\homes/alice
//	→ D--work-anc-demo-homes-alice
//
// 即**每个非字母数字字符 → '-'**（`:` `\` `_` `/` `.` 一律如此）。
// 这个映射是有损的、反推不回来，所以只做正向：从配置里的 work_dir 算出该去哪个目录找记录。
// 规则本身搬进了接入口径表（internal/harness 的 dir_rule）—— 上游反推和下游找记录
// 必须是同一条规则，而「是不是同一条」靠共用一份数据保证，不靠两处抄得一样。
func Slug(workDir string) string {
	s, err := SlugFor(harness.RuleNonalnumDash, workDir)
	if err != nil {
		// 这条规则是编译期常量，走不到这里；真走到了宁可炸，也不要静默编一个目录名。
		panic("Slug 的规则不认识了: " + err.Error())
	}
	return s
}

// SlugFor 按口径表里的规则名起名（表里换了规则，这里跟着换）。
func SlugFor(rule, workDir string) (string, error) { return harness.Slug(rule, workDir) }

// Usage 是 token 账。四个字段跟着 harness 的口径走，不做换算、不做猜测。
type Usage struct {
	In         int `json:"in"`
	Out        int `json:"out"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
}

// Add 相加（合并子 agent 的账时用）。
func (u Usage) Add(v Usage) Usage {
	return Usage{u.In + v.In, u.Out + v.Out, u.CacheRead + v.CacheRead, u.CacheWrite + v.CacheWrite}
}

// Total 是「这次一共动了多少 token」。缓存读单列但不并进这里 —— 它和 in 不是一个量级，
// 混在一起会让人以为「输入」暴涨。
func (u Usage) Total() int { return u.In + u.Out }

// maxOf 按字段取大。同一条消息的多次落盘里，中间态是 0，最后一次才是终值。
func maxOf(a, b Usage) Usage {
	return Usage{
		In:         maxInt(a.In, b.In),
		Out:        maxInt(a.Out, b.Out),
		CacheRead:  maxInt(a.CacheRead, b.CacheRead),
		CacheWrite: maxInt(a.CacheWrite, b.CacheWrite),
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ToolCount 是某个工具用了几次。
type ToolCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Failure 是一次**没成功的动作**。它不是「故障」的同义词，得看 Kind：
//
//	denied —— 被权限规则挡下（人没给这个动作的权，不是坏了）；
//	failed —— 真执行失败（文件不在、路径不存……）。
//
// 两者要分开讲：把「权限没给」说成「故障」，就会把人引去查一个不存在的问题；
// 反过来把真失败说成「权限不够」，人就会去加权限，然后问题还在。
type Failure struct {
	Kind string    `json:"kind"` // denied | failed
	Tool string    `json:"tool,omitempty"`
	Why  string    `json:"why,omitempty"` // 记录里的原话（截断），不转述
	At   time.Time `json:"at,omitempty"`
}

// Turn 是一轮：谁在什么时候问的、这一轮 agent 用了什么、花了多少。
//
// 这是「留痕」的最小单位。**不问「决定是什么」** —— 那是判断层的事，得挂在事实层之上，
// 不能让读记录的人替 agent 编一个理由出来。
type Turn struct {
	At       time.Time     `json:"at"`
	Prompt   string        `json:"prompt"`
	Usage    Usage         `json:"usage"`
	Tools    []ToolCount   `json:"tools,omitempty"`
	Denied   int           `json:"denied,omitempty"`
	Duration time.Duration `json:"duration_ns,omitempty"`

	// Failures 是这一轮里**没成功**的动作，带记录里的原话。
	// 判据层（internal/judge）就长在这些字段上 —— 事实层只负责把话说准，不解释。
	Failures []Failure `json:"failures,omitempty"`
}

// Subagent 是一次子任务：主 agent 用 Agent 工具拉起的旁路会话。
// 它的 token 记在**本会话**账上，所以要单列出来让账能对上。
type Subagent struct {
	Agent string `json:"agent,omitempty"`
	Desc  string `json:"desc,omitempty"`
	Turn  int    `json:"turn,omitempty"` // 是第几轮把它拉起来的（0 = 归属不到）
	Turns int    `json:"turns"`
	Usage Usage  `json:"usage"`
}

// Session 是一个 bot 的一段运行记录（= harness 会话）。
type Session struct {
	Schema    string `json:"schema"`
	Project   string `json:"project"`
	Slot      string `json:"slot,omitempty"` // cc-connect 的会话槽（s1/s2/…）——对账用
	ID        string `json:"id"`             // harness 会话 id
	AgentType string `json:"agent_type,omitempty"`
	Historic  bool   `json:"historic,omitempty"` // true = 这个槽历史上用过、现在不是当前会话

	Dir   string `json:"dir"`   // 记录在哪个目录（给人定位）
	Found bool   `json:"found"` // 原生记录找到了没有 —— 没找到就是**读不到**，不是 0 消耗

	Started time.Time `json:"started,omitempty"`
	Ended   time.Time `json:"ended,omitempty"`

	Turns []Turn `json:"turns"`
	Usage Usage  `json:"usage"`

	CostUSD     float64 `json:"cost_usd,omitempty"`
	UnknownCost bool    `json:"unknown_cost,omitempty"` // harness 自己标的「有模型没价格」

	TotalDuration time.Duration `json:"total_duration_ns,omitempty"`
	APIDuration   time.Duration `json:"api_duration_ns,omitempty"`
	ToolDuration  time.Duration `json:"tool_duration_ns,omitempty"`

	LinesAdded   int    `json:"lines_added,omitempty"`
	LinesRemoved int    `json:"lines_removed,omitempty"`
	GitBranch    string `json:"git_branch,omitempty"`

	Tools     []ToolCount `json:"tools,omitempty"`
	Denials   []ToolCount `json:"denials,omitempty"`  // 被权限规则挡下的动作，按 kind 计
	Failures  []Failure   `json:"failures,omitempty"` // 全段会话里没成功的动作（含子任务的）
	Subagents []Subagent  `json:"subagents,omitempty"`

	// Problems 是「这份账没读全」的地方。**必须说出来** —— 静默少报等于把
	// 「读不到」伪装成「没有」，那和假绿是同一类错误。
	Problems []string `json:"problems,omitempty"`
}

// Bridges 是 cc-connect 侧的一条映射：会话槽 → harness 会话 id。
type Bridge struct {
	Project   string
	Slot      string
	SessionID string
	PastIDs   []string
	AgentType string
}

// ccFile 只取我们要的字段；上游会加字段，不跟着它跑。
type ccFile struct {
	Sessions map[string]struct {
		AgentSessionID      string   `json:"agent_session_id"`
		PastAgentSessionIDs []string `json:"past_agent_session_ids"`
		AgentType           string   `json:"agent_type"`
		History             []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			Timestamp string `json:"timestamp"`
		} `json:"history"`
	} `json:"sessions"`
}

// Bridges 从一份会话落盘 JSON 里取出「哪些 harness 会话 id 属于这个 project」。
//
// 连**历史 id**（`past_agent_session_ids`）一起取：一个槽换过会话之后，旧记录还在盘上，
// 只报当前的会把之前的工作整段丢掉。
func Bridges(project, path string) ([]Bridge, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f ccFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("会话文件 %s 解析不了：%w", filepath.Base(path), err)
	}
	slots := make([]string, 0, len(f.Sessions))
	for k := range f.Sessions {
		slots = append(slots, k)
	}
	sort.Strings(slots)

	var out []Bridge
	for _, slot := range slots {
		s := f.Sessions[slot]
		b := Bridge{Project: project, Slot: slot, SessionID: s.AgentSessionID, AgentType: s.AgentType}
		for _, p := range s.PastAgentSessionIDs {
			if p != "" && p != s.AgentSessionID {
				b.PastIDs = append(b.PastIDs, p)
			}
		}
		if b.SessionID == "" && len(b.PastIDs) == 0 {
			continue // 这个槽从没起来过 agent，没有记录可归集
		}
		out = append(out, b)
	}
	return out, nil
}

// TranscriptDir 是某段会话的原生记录落在哪，按**默认那一家**的口径算。
// 布局不写在这里：它来自接入口径表（internal/harness 的 layout）。
func TranscriptDir(root, workDir string) string {
	dir, err := TranscriptDirIn(harness.DefaultFamily(), root, workDir)
	if err != nil {
		// 默认那家的规则是编译期常量，走不到这里。
		panic("TranscriptDir: " + err.Error())
	}
	return dir
}

// TranscriptDirIn 按指定那一家的布局算记录目录。
func TranscriptDirIn(fam harness.Family, root, workDir string) (string, error) {
	return fam.TranscriptDir(root, workDir)
}

// ReadSession 读一段会话的原生记录（按默认那一家）。读不到**不报错**，而是把 Session.Found
// 置 false 并在 Problems 里写明原因 —— 调用方要能把「读不到」和「没有」分开讲。
func ReadSession(project, slot string, historic bool, root, workDir, id, agentType string) Session {
	return ReadSessionIn(harness.DefaultFamily(), root, project, slot, historic, workDir, id, agentType)
}

// ReadSessionIn 按指定那一家的口径读。三件事都在这里定，且都来自表：
// 记录目录怎么算（layout）、主记录叫什么、子任务记录在哪。
//
// **读不了的家在这里就被挡下**：表里 reader 为空 = 只登记了口径、还没有读取器 ——
// 明说读不到，不装作 0 消耗（静默少报和假绿是同一类错误）。
func ReadSessionIn(fam harness.Family, root, project, slot string, historic bool, workDir, id, agentType string) Session {
	s := Session{
		Schema:    Schema,
		Project:   project,
		Slot:      slot,
		ID:        id,
		AgentType: agentType,
		Historic:  historic,
		Turns:     []Turn{},
	}
	if err := fam.CanRead(); err != nil {
		s.Problems = append(s.Problems, err.Error())
		return s
	}
	dir, main, merr := fam.ResolveMain(root, workDir, id)
	if merr != nil {
		s.Problems = append(s.Problems, merr.Error())
		return s
	}
	s.Dir = dir
	if main == "" {
		s.Problems = append(s.Problems, fmt.Sprintf(
			"原生记录找不到：%s 底下没有句柄为 %s 的主记录（这是**读不到**，不是 0 消耗）", dir, id))
		return s
	}
	if _, err := os.Stat(main); err != nil {
		s.Problems = append(s.Problems, fmt.Sprintf(
			"原生记录找不到：%s（这是**读不到**，不是 0 消耗）", main))
		return s
	}
	s.Found = true
	uses, err := scanWith(fam.Reader, main, &s)
	if err != nil {
		s.Problems = append(s.Problems, "主记录读不动："+err.Error())
		return s
	}
	// 子 agent：同一段会话的旁路记录，token 也算在这个 bot 头上。
	// 这一家有没有子任务记录、放在哪，也来自口径表 —— 表里没声明就当作没有，
	// 不编一个目录出来（编出来的话，读不到会被说成「没有」）。
	subGlob, metaSuffix := fam.SubagentPaths(dir, id)
	var subs []string
	if subGlob != "" {
		listed, gerr := filepath.Glob(subGlob)
		if gerr != nil {
			s.Problems = append(s.Problems, "子 agent 记录列不出来："+gerr.Error())
		}
		subs = listed
	}
	sort.Strings(subs)
	for _, f := range subs {
		var sub Session
		if _, err := scanWith(fam.Reader, f, &sub); err != nil {
			s.Problems = append(s.Problems, "子 agent 记录读不动（"+filepath.Base(f)+"）："+err.Error())
			continue
		}
		sa := Subagent{Turns: len(sub.Turns), Usage: sub.Usage}
		// 子任务花的钱是**这个 bot 花的**，一律并进会话总账；能对上 toolUseId 就再归到
		// 拉起它的那一轮（这样「哪一轮花了多少」才解释得了总账）。
		s.Usage = s.Usage.Add(sub.Usage)
		parent := -1
		// metaSuffix 也来自表；它为空时上面的 subs 必为空，这个循环不会进来。
		if m, ok := readMeta(strings.TrimSuffix(f, ".jsonl") + metaSuffix); ok {
			sa.Agent, sa.Desc = m.AgentType, m.Description
			if i, ok := uses[m.ToolUseID]; ok && i >= 0 && i < len(s.Turns) {
				s.Turns[i].Usage = s.Turns[i].Usage.Add(sub.Usage)
				parent = i
				sa.Turn = i + 1
			}
		}
		// 子任务里没成功的动作也算这一轮的（同一个 id 归位）。
		for _, fa := range sub.Failures {
			s.Failures = append(s.Failures, fa)
			if parent >= 0 {
				s.Turns[parent].Failures = append(s.Turns[parent].Failures, fa)
			}
		}
		if sa.Turn == 0 {
			s.Problems = append(s.Problems, fmt.Sprintf(
				"有 1 个子任务归属不到哪一轮（meta 里的 toolUseId 在主记录里找不到）：它的 token 只在会话总账上",
			))
		}
		// 工具与被拒也一起并进来：子任务里的动作同样是这个 bot 做的动作，
		// 单列出来是为了「能拆开看」，不是为了「不算它」。
		for _, c := range sub.Tools {
			s.Tools = bumpBy(s.Tools, c.Name, c.Count)
		}
		for _, c := range sub.Denials {
			s.Denials = bumpBy(s.Denials, c.Name, c.Count)
		}
		s.Subagents = append(s.Subagents, sa)
	}
	sortCounts(s.Tools)
	sortCounts(s.Denials)
	return s
}

type subMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
}

func readMeta(path string) (subMeta, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return subMeta{}, false
	}
	var m subMeta
	if json.Unmarshal(b, &m) != nil {
		return subMeta{}, false
	}
	return m, true
}

// claudeLine 是一行原生记录里 trail 关心的字段。上游字段很多，只挑**账和动作**相关的。
type claudeLine struct {
	Type       string          `json:"type"`
	Timestamp  string          `json:"timestamp"`
	PromptID   string          `json:"promptId"`
	Denial     string          `json:"toolDenialKind"`
	GitBranch  string          `json:"gitBranch"`
	ToolResult json.RawMessage `json:"toolUseResult"`

	Message struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		IsError bool            `json:"is_error"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			In         int `json:"input_tokens"`
			Out        int `json:"output_tokens"`
			CacheRead  int `json:"cache_read_input_tokens"`
			CacheWrite int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`

	// cost-state：harness 自己的账本（累计快照，不是流水）。
	TotalDuration       float64 `json:"totalDuration"`
	TotalAPIDuration    float64 `json:"totalAPIDuration"`
	TotalToolDuration   float64 `json:"totalToolDuration"`
	TotalCostUSD        float64 `json:"totalCostUSD"`
	HasUnknownModelCost bool    `json:"hasUnknownModelCost"`
	TotalLinesAdded     int     `json:"totalLinesAdded"`
	TotalLinesRemoved   int     `json:"totalLinesRemoved"`
}

// usageOf 从一条 assistant 行里取用量。
func (l claudeLine) usageOf() Usage {
	u := l.Message.Usage
	return Usage{In: u.In, Out: u.Out, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
}

// readerFunc 是一家的记录读取器：把一份记录折进 s，返回工具调用 id → 轮次下标。
type readerFunc func(path string, s *Session) (map[string]int, error)

// readers 是读取器注册表：口径表里的 reader 名字 → 实现。
//
// 加一家 harness，只有「读的语义」真的不一样时才加一行；布局不一样（目录名、文件名、
// 子任务在哪）只改数据，不改这里 —— 那正是口径搬进表的意义。
var readers = map[string]readerFunc{
	ReaderCodex:     scanCodex,
	ReaderOpenClaw:  scanOpenClaw,
	"claude-jsonl":  scanClaude,
	ReaderCodeBuddy: scanCodeBuddy,
}

// scanWith 按口径表里的 reader 名字分派。
//
// 名字不认识**必须报错**：静默退回默认那一家的读法，就会把别家的记录按错的口径读出来 ——
// 那比读不到更糟（读不到会说读不到，读错会说成一堆数字）。
func scanWith(reader string, path string, s *Session) (map[string]int, error) {
	fn, ok := readers[reader]
	if !ok {
		return nil, fmt.Errorf("读取器 %q 不认识（这个名字来自接入口径表）", reader)
	}
	return fn(path, s)
}

// scan 读一份 claude 记录。保留这个名字，是因为现有的对拍用例直接叫它。
func scan(path string, s *Session) (map[string]int, error) { return scanClaude(path, s) }

// scanClaude 读一份 jsonl，按 message.id 合并后折进 s。
//
// 合并是因为**同一条消息会写多行**：一行一个 content block，流式中间态还会把 output 写成 0。
// 按行累加会把账翻倍（实测：172549 vs 85726 —— 正好一倍）。
// scanClaude 返回 tool_use id → 第几轮的下标：子任务的 meta 靠这个 id 归到它所属的那一轮。
func scanClaude(path string, s *Session) (map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLine)

	var (
		turns    []Turn
		index    = map[string]int{}    // promptId → 在 turns 里的下标
		byMsg    = map[string]*Usage{} // message.id → 合并后的用量
		msgTurn  = map[string]int{}    // message.id → 归属哪一轮（取最后一次出现）
		msgTools = map[string]map[string]bool{}
		toolTurn = map[string]int{}
		subTools = map[string]int{}
		denials  = map[string]int{}
		uses     = map[string]int{}    // tool_use id → 轮次
		useName  = map[string]string{} // tool_use id → 工具名
		failures []Failure
		ends     []time.Time // 每一轮最后一条记录的时刻（算这一轮实际干了多久）
		badLines int
		cur      = -1
	)

	// mark：把这一轮的实际结束时间往后推。**不用下一轮的开始时间当结束** ——
	// 中间的空闲（人去开会了）会被算成 agent 干了 20 分钟，那是假的。
	mark := func(ts time.Time) {
		if cur >= 0 && cur < len(ends) && ts.After(ends[cur]) {
			ends[cur] = ts
		}
	}

	for sc.Scan() {
		raw := sc.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var l claudeLine
		if err := json.Unmarshal(raw, &l); err != nil {
			badLines++
			continue
		}
		ts, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
		if !ts.IsZero() {
			if s.Started.IsZero() || ts.Before(s.Started) {
				s.Started = ts
			}
			if ts.After(s.Ended) {
				s.Ended = ts
			}
		}
		if l.GitBranch != "" && s.GitBranch == "" {
			s.GitBranch = l.GitBranch
		}

		switch l.Type {
		case "user":
			if len(l.ToolResult) > 0 && string(l.ToolResult) != "null" {
				// 工具结果：算这一轮的记录，也记它是不是被权限挡下来的。
				mark(ts)
				if l.Denial != "" {
					denials[l.Denial]++
					if cur >= 0 {
						turns[cur].Denied++
					}
				}
				// 没成功的动作：记录里带 is_error 的那些。**原话照抄**（截断），不转述 ——
				// 「File does not exist」和「Permission denied」是两件完全不同的事。
				for _, f := range failuresIn(l.Message.Content, l.Denial, useName) {
					f.At = ts
					failures = append(failures, f)
					if cur >= 0 {
						turns[cur].Failures = append(turns[cur].Failures, f)
					}
				}
				continue
			}
			// 用户的一轮提问：新开一轮。
			key := l.PromptID
			if key == "" {
				key = "ts:" + l.Timestamp
			}
			if i, ok := index[key]; ok {
				cur = i
				continue
			}
			turns = append(turns, Turn{At: ts, Prompt: head(contentText(l.Message.Content), promptHead), Duration: 0})
			ends = append(ends, ts)
			index[key] = len(turns) - 1
			cur = len(turns) - 1

		case "assistant":
			id := l.Message.ID
			if id == "" {
				id = "ts:" + l.Timestamp
			}
			if u, ok := byMsg[id]; ok {
				*u = maxOf(*u, l.usageOf())
			} else {
				u := l.usageOf()
				byMsg[id] = &u
			}
			msgTurn[id] = cur
			// 工具名：一行只带一个 block，所以要**并集**，不能只看最后一行。
			for _, tr := range toolUses(l.Message.Content) {
				if msgTools[id] == nil {
					msgTools[id] = map[string]bool{}
				}
				if !msgTools[id][tr.Name] {
					msgTools[id][tr.Name] = true
					toolTurn[id] = cur
					subTools[tr.Name]++
				}
				if tr.ID != "" && cur >= 0 {
					uses[tr.ID] = cur
				}
				if tr.ID != "" && tr.Name != "" {
					useName[tr.ID] = tr.Name
				}
			}
			mark(ts)

		case "cost-state":
			// 累计快照：最后一次写的才是全量。前面的是中途快照，不能相加。
			s.CostUSD = l.TotalCostUSD
			s.UnknownCost = l.HasUnknownModelCost
			s.TotalDuration = ms(l.TotalDuration)
			s.APIDuration = ms(l.TotalAPIDuration)
			s.ToolDuration = ms(l.TotalToolDuration)
			s.LinesAdded = l.TotalLinesAdded
			s.LinesRemoved = l.TotalLinesRemoved
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if badLines > 0 {
		s.Problems = append(s.Problems, fmt.Sprintf(
			"%s 里有 %d 行解析不了 —— 记录格式可能变了，这份账不全", filepath.Base(path), badLines))
	}

	// 把按消息合并出来的账，折回它所属的那一轮。
	for id, u := range byMsg {
		if i := msgTurn[id]; i >= 0 && i < len(turns) {
			turns[i].Usage = turns[i].Usage.Add(*u)
		}
	}
	for id, names := range msgTools {
		i := toolTurn[id]
		if i < 0 || i >= len(turns) {
			continue
		}
		for name := range names {
			turns[i].Tools = bumpBy(turns[i].Tools, name, 1)
		}
	}
	for i := range turns {
		turns[i].Usage = clampUsage(turns[i].Usage)
		s.Usage = s.Usage.Add(turns[i].Usage)
		sortCounts(turns[i].Tools)
		if i < len(ends) && ends[i].After(turns[i].At) {
			turns[i].Duration = ends[i].Sub(turns[i].At)
		}
	}
	s.Turns = append(s.Turns, turns...)
	s.Failures = append(s.Failures, failures...)
	for name, n := range subTools {
		s.Tools = bumpBy(s.Tools, name, n)
	}
	for kind, n := range denials {
		s.Denials = bumpBy(s.Denials, kind, n)
	}
	sortCounts(s.Tools)
	sortCounts(s.Denials)
	return uses, nil
}

func ms(v float64) time.Duration { return time.Duration(v) * time.Millisecond }

// clampUsage 是最后的护栏：负数不该出现，出现了就当 0（宁可少报也不报负数）。
func clampUsage(u Usage) Usage {
	if u.In < 0 {
		u.In = 0
	}
	if u.Out < 0 {
		u.Out = 0
	}
	if u.CacheRead < 0 {
		u.CacheRead = 0
	}
	if u.CacheWrite < 0 {
		u.CacheWrite = 0
	}
	return u
}

func bumpBy(list []ToolCount, name string, n int) []ToolCount {
	for i := range list {
		if list[i].Name == name {
			list[i].Count += n
			return list
		}
	}
	return append(list, ToolCount{Name: name, Count: n})
}

func sortCounts(list []ToolCount) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return list[i].Name < list[j].Name
	})
}

// contentText 从 message.content 里取人话。它可能是字符串（用户提问），
// 也可能是 block 数组（assistant 的 text / thinking / tool_use）。
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, " ")
}

// toolRef 是一次工具调用的身份：名字 + 调用 id（子任务靠 id 归到轮次）。
type toolRef struct{ Name, ID string }

// toolUses 取这一行里的工具调用（一行只带一个 block，所以调用方要取并集）。
// failuresIn 从一条工具结果里挑出**没成功**的那些，并说清是哪一种没成功。
//
// 判据全在记录里现成的字段上，我们不自己推断：
//   - `is_error` 为真 = 这次工具调用没成；
//   - 同一条记录带 `toolDenialKind` = 被**权限规则**挡下（不是坏了），
//     这时 is_error 的原话是「Permission to use X has been denied…」；
//   - 没有 toolDenialKind 的 = 真执行失败（文件不在、路径不存…），原因取记录原话。
//
// 工具名优先用 tool_use_id 回查主记录里的 tool_use（最准）；查不到就留空，
// **不猜**（宁可少一个名字，也不要给一个错的名字）。
func failuresIn(content json.RawMessage, denial string, useName map[string]string) []Failure {
	if len(content) == 0 {
		return nil
	}
	var blocks []struct {
		Type      string          `json:"type"`
		IsError   bool            `json:"is_error"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []Failure
	for _, b := range blocks {
		if b.Type != "tool_result" || !b.IsError {
			continue
		}
		kind := "failed"
		if denial != "" {
			kind = "denied"
		}
		out = append(out, Failure{
			Kind: kind,
			Tool: useName[b.ToolUseID],
			Why:  reasonHead(blockText(b.Content)),
		})
	}
	return out
}

// blockText 把 block 的 content 取成人话：可能是字符串，也可能是 [{type,text}]。
func blockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// reasonHead 取原因的第一句。上游在拒绝理由后面接了一长段给模型看的 IMPORTANT 提示，
// 那是给 agent 的指令、不是给人看的原因 —— 切掉。
func reasonHead(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "IMPORTANT:"); i > 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return head(s, promptHead*2)
}

// ambientTags 是 harness 塞在真人提问**前面**的环境上下文块的名字。
//
// 实测（2026-10-09 本机 Codex rollout）：一轮的提问整段是
// `<in-app-browser-context …>…</in-app-browser-context>` + 空行 + `## My request:` + 真人那句。
// 直接截前 80 字，看到的一行全是这套样板，人到底问了什么反而看不见 —— 所以先剥再截。
// 表里没有的记号**不猜**（猜错比不猜更坏），原样留着。
var ambientTags = []string{"environment_context", "in-app-browser-context", "app-context"}

// promptOf 从一条用户消息里挑出「人问的那句话」，再截到 promptHead。
// 两步：① 认上游自己的分隔记号 `## My request:`（有就取它**后面**的）；② 剥行首已知的
// `<tag …>…</tag>` 块（可能连着好几个）。剥完是空的就退回原文 —— 宁可看到的是一行样板，
// 也不能让这一轮的提问变成空白。
func promptOf(s string) string {
	full := strings.TrimSpace(s)
	s = full
	if i := strings.LastIndex(s, "## My request:"); i >= 0 {
		s = strings.TrimSpace(s[i+len("## My request:"):])
	} else {
		for {
			rest, ok := cutLeadWrapper(s, ambientTags)
			if !ok {
				break
			}
			s = strings.TrimSpace(rest)
		}
	}
	if s == "" {
		s = full
	}
	return head(s, promptHead)
}

// cutLeadWrapper 剥掉 s 行首的一个 `<tag …>…</tag>` 块（只认 tags 里有的名字），
// 返回余下的部分；行首没有可剥的块就返回 ok=false。属性（`<tag source="…">`）照收。
func cutLeadWrapper(s string, tags []string) (string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "<") {
		return s, false
	}
	gt := strings.IndexByte(s, '>')
	if gt < 0 {
		return s, false
	}
	name := strings.TrimPrefix(s[:gt], "<")
	if i := strings.IndexAny(name, " \t\r\n"); i >= 0 {
		name = name[:i]
	}
	known := false
	for _, tag := range tags {
		if tag == name {
			known = true
			break
		}
	}
	if name == "" || !known {
		return s, false
	}
	end := strings.Index(s, "</"+name+">")
	if end < 0 {
		return s, false
	}
	return s[end+len("</"+name+">"):], true
}

func toolUses(raw json.RawMessage) []toolRef {
	if len(raw) == 0 {
		return nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []toolRef
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name != "" {
			out = append(out, toolRef{Name: b.Name, ID: b.ID})
		}
	}
	return out
}

func head(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ") // 折成一行的：这行要进终端表格，不是正文
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

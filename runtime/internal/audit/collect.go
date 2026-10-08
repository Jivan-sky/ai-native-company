package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ToolRule 是「一个工具怎么读成一次行使」。
//
// **这是数据**（可以整份替换，同 judge 的判据表）：换 harness、加工具、改归类，
// 换的是这份表，不是代码。认不出的工具**不静默丢掉** —— 计入 Problems 报出来（见 Collect）。
type ToolRule struct {
	Tool   string   `json:"tool"`   // harness 里的工具名
	Action string   `json:"action"` // read | write | invoke
	Target []string `json:"target"` // input 里哪几个键是「对谁」；按顺序取第一个非空的
}

// BuiltinTools 是出厂工具表。覆盖 Claude Code 原生工具；
// 别的 harness（Codex / Hermes / OpenClaw…）加自己的条目即可 —— 加的是数据。
//
// 目标键留空的（Bash / Agent）：它们的 input 是自由文本，抽不出「对谁」。
// 这类行使照样落流水（Object 为空），但归集器会把次数报进 Problems ——
// 「有一条行使，但目标抽不出来」是必须让人看见的事实。
func BuiltinTools() []ToolRule {
	return []ToolRule{
		{Tool: "Read", Action: ActionRead, Target: []string{"file_path"}},
		{Tool: "Glob", Action: ActionRead, Target: []string{"path"}},
		{Tool: "Grep", Action: ActionRead, Target: []string{"path"}},
		{Tool: "NotebookRead", Action: ActionRead, Target: []string{"notebook_path"}},
		{Tool: "Write", Action: ActionWrite, Target: []string{"file_path"}},
		{Tool: "Edit", Action: ActionWrite, Target: []string{"file_path"}},
		{Tool: "MultiEdit", Action: ActionWrite, Target: []string{"file_path"}},
		{Tool: "NotebookEdit", Action: ActionWrite, Target: []string{"notebook_path"}},
		{Tool: "Bash", Action: ActionInvoke},
		{Tool: "BashOutput", Action: ActionInvoke},
		{Tool: "Agent", Action: ActionInvoke},
		{Tool: "Task", Action: ActionInvoke},
	}
}

// CollectOptions 是一次归集的输入。
type CollectOptions struct {
	ClaudeHome string                      // harness 的项目记录根（`<ClaudeHome>/projects/<slug>/`）
	Projects   map[string]string           // project 名 → work_dir（`render.WorkDirs` 给的）
	ActorOf    func(project string) string // project → 行使者名；nil = 用 project 名
	Tools      []ToolRule                  // 空 = BuiltinTools()
	Known      map[string]bool             // 已经记过的 id（Load 出来的 IDs）；命中的跳过
	Since      time.Time                   // 只要这个时刻之后的（零值 = 不限）
}

// CollectResult 是一次归集的结果。
type CollectResult struct {
	Records  []Record
	Problems []string // **记不到的**（目标抽不出 / 工具没登记 / 结果配不上）—— 调用方必须回显
	Shards   int      // 扫了几个分片（会话文件）
	Calls    int      // 认出的行使数（含没落进 Records 的）
}

// Collect 从 harness 原生记录里把「已经发生的行使」抽出来。
//
// 三条口径：
//   - **只读**：不改上游、不碰会话文件。
//   - **幂等**：Record.ID 由（会话 id, tool_use id）拼成；重跑不会写第二条。
//     调用方传 Known（既有 id 集合）来跳过已记的。
//   - **记不到的显式报**：目标抽不出、工具没登记、结果配不上，一律进 Problems。
//
// 「本可以行使但没行使」**这里永远不会出现** —— 它没发生，就没有记录可归集。
// 那一类只能靠显式补记（`anc audit add`）。这是这份流水的边界，页面要如实写。
func Collect(opt CollectOptions) CollectResult {
	var res CollectResult
	rules := opt.Tools
	if len(rules) == 0 {
		rules = BuiltinTools()
	}
	byTool := make(map[string]ToolRule, len(rules))
	for _, r := range rules {
		byTool[r.Tool] = r
	}
	unregistered := map[string]int{}
	noTarget := map[string]int{}      // 工具表里没登记目标键（Bash 这类）
	missingTarget := map[string]int{} // 登记了目标键、但记录里那个字段是空的

	for _, project := range sortedKeys(opt.Projects) {
		actor := project
		if opt.ActorOf != nil {
			if a := strings.TrimSpace(opt.ActorOf(project)); a != "" {
				actor = a
			}
		}
		dir := filepath.Join(opt.ClaudeHome, "projects", slugDir(opt.Projects[project]))
		names, err := os.ReadDir(dir)
		if err != nil {
			// 记录目录不在 = 这个 bot 还没有过会话（或没跑在这台机器上）。
			// 不报 Problem：那是「还没发生」，不是「读不到」（区别见 ReadSession 的 Found）。
			continue
		}
		for _, n := range names {
			if n.IsDir() || !strings.HasSuffix(n.Name(), ".jsonl") {
				continue
			}
			res.Shards++
			collectShard(filepath.Join(dir, n.Name()), strings.TrimSuffix(n.Name(), ".jsonl"), actor,
				byTool, unregistered, noTarget, missingTarget, opt, &res)
		}
	}
	for _, tool := range sortedKeys(unregistered) {
		res.Problems = append(res.Problems, fmt.Sprintf(
			"工具 %s 调用 %d 次，不在工具表里，没有落进审计 —— 加一条 ToolRule 就能收（工具表是数据）",
			tool, unregistered[tool]))
	}
	for _, tool := range sortedKeys(noTarget) {
		res.Problems = append(res.Problems, fmt.Sprintf(
			"工具 %s 调用 %d 次，工具表里没有它的目标键（object 为空）—— 这类行使「对谁」这一栏永远是空的，看板上按「未归属」显示",
			tool, noTarget[tool]))
	}
	for _, tool := range sortedKeys(missingTarget) {
		res.Problems = append(res.Problems, fmt.Sprintf(
			"工具 %s 调用 %d 次，工具表登记了目标键但记录里是空的（object 为空）—— 这是记录不全，不是「没有目标」",
			tool, missingTarget[tool]))
	}
	return res
}

// outcome 是一次工具调用的结果。
type outcome struct {
	isError bool
	denied  bool
	why     string
}

// collectShard 扫一个会话文件。
//
// 同一条 tool_use 会被写多行（流式中间态里 input 还没流完），所以**后到的覆盖先到的** ——
// 保留最后那一份完整的 input。tool_result 只会出现一次，但同样按 id 存，不依赖顺序。
func collectShard(path, sessionID, actor string, byTool map[string]ToolRule,
	unregistered, noTarget, missingTarget map[string]int, opt CollectOptions, res *CollectResult) {

	f, err := os.Open(path)
	if err != nil {
		res.Problems = append(res.Problems, "读不动会话记录 "+filepath.Base(path)+"："+firstLine(err.Error()))
		return
	}
	defer f.Close()

	uses := map[string]Record{}
	var order []string
	outcomes := map[string]outcome{}
	// 没登记的工具：记 id → 名字（去重），扫完统一报一次。
	// 不在扫的过程中计数 —— 流式中间态会让同一条调用出现多行，边扫边数会重复计。
	unregIDs := map[string]string{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		var l struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Denial    string `json:"toolDenialKind"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil || len(l.Message.Content) == 0 {
			continue
		}
		var blocks []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(l.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				if b.ID == "" {
					continue
				}
				rule, ok := byTool[b.Name]
				if !ok {
					// 没登记的工具**不静默丢掉**，但也不落流水：记下 id（去重），扫完统一报一次。
					unregIDs[b.ID] = b.Name
					delete(uses, b.ID)
					continue
				}
				if _, seen := uses[b.ID]; !seen {
					order = append(order, b.ID)
				}
				// 后到的覆盖先到的（流式中间态里 input 还没流完，最后那份才是完整的）。
				uses[b.ID] = Record{
					ID:     sessionID + ":" + b.ID,
					At:     l.Timestamp,
					Actor:  actor,
					Action: rule.Action,
					Object: targetOf(b.Input, rule.Target),
					Tool:   b.Name,
					Source: "collect:claude",
					Ref:    sessionID,
					Detail: head(inputDigest(b.Name, b.Input), 160),
				}
			case "tool_result":
				if b.ToolUseID == "" {
					continue
				}
				outcomes[b.ToolUseID] = outcome{
					isError: b.IsError,
					denied:  l.Denial != "",
					why:     reasonHead(blockText(b.Content)),
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		res.Problems = append(res.Problems, "会话记录 "+
			filepath.Base(path)+" 读到一半断了（"+firstLine(err.Error())+"）—— 这一份可能少记，别当成没发生")
	}

	for _, name := range unregIDs {
		unregistered[name]++
	}
	for _, id := range order {
		r, ok := uses[id]
		if !ok {
			continue
		}
		res.Calls++
		// 目标抽不出来分两种：工具表没登记目标键（Bash 这类）vs 登记了但记录里是空的。
		// 两个都得报，但**原因不同** —— 混成一句话会把人送去改错的地方。
		// 在**最终形态**上数：流式中间态的 `{}` 不算「记录不全」。
		if r.Object == "" {
			if len(byTool[r.Tool].Target) == 0 {
				noTarget[r.Tool]++
			} else {
				missingTarget[r.Tool]++
			}
		}
		if !opt.Since.IsZero() {
			t, perr := time.Parse(time.RFC3339, r.At)
			if perr != nil || t.Before(opt.Since) {
				continue
			}
		}
		if opt.Known[r.ID] {
			continue
		}
		if o, ok := outcomes[id]; ok {
			switch {
			case !o.isError:
				r.Result = ResultOK
			case o.denied:
				r.Result = ResultDenied
			default:
				r.Result = ResultFailed
			}
			r.Why = o.why
		} else {
			// 没有结果：这一轮被中断了（会话断了 / 进程被杀了）。
			// **不许当成「没发生」**，也不许猜一个结果出来 —— 落 unknown，并在 Problems 里说清。
			r.Result = ResultUnknown
			r.Detail = strings.TrimSpace(r.Detail + "（没有配到结果，是这一轮被中断了）")
			res.Problems = append(res.Problems, fmt.Sprintf(
				"%s 的 %s 调用没有配到结果（结果落 unknown）—— 多半是那一轮被中断，不是它没发生",
				sessionID, r.Tool))
		}
		res.Records = append(res.Records, r)
	}
}

// targetOf 从 input 里取「对谁」。键按顺序试，取第一个非空字符串。
func targetOf(input json.RawMessage, keys []string) string {
	if len(keys) == 0 || len(input) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// inputDigest 给 Detail 一句可读的原话：目标键没登记的（Bash）拿 command / prompt，
// 别的工具拿第一个字符串值 —— 只在 object 抽不出来时才用得上。
func inputDigest(tool string, input json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, k := range []string{"command", "description", "prompt", "pattern", "query"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// reasonHead 取原因的第一句。上游在拒绝理由后面接了一长段给模型看的 IMPORTANT 提示，
// 那是给 agent 的指令、不是给人看的原因 —— 切掉。
//
// 与 trail.reasonHead 同源（同一份记录格式），刻意各留一份：
// 两个包对记录的依赖面不同，谁也不用为对方的重构买单。
func reasonHead(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "IMPORTANT:"); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.ReplaceAll(s, "\n", " ")
	return head(s, 160)
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

// slugDir 是 harness 给项目目录起名的规则（同 trail.Slug）：每个非字母数字字符 → '-'。
//
// 这个映射是有损的、反推不回来，所以只做正向：从 work_dir 算出该去哪个目录找记录。
func slugDir(workDir string) string {
	var b strings.Builder
	b.Grow(len(workDir))
	for _, r := range workDir {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// head 按 **rune** 截断：按字节切会把一个中文字切成两半，拼出乱码。
func head(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

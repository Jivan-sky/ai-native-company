package trail

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Slug 是实测出来的目录名规则（见 package 注释）。这三个样本都是盘上真存在的目录。
func TestSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{`D:\Agetn_Context\codex_work\anc-demo\homes/alice`, "D--Agetn-Context-codex-work-anc-demo-homes-alice"},
		{`C:\Users\sjw`, "C--Users-sjw"},
		{"/Users/sjw/anc/homes/alice", "-Users-sjw-anc-homes-alice"},
	}
	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q，想要 %q", c.in, got, c.want)
		}
	}
}

// 造一份原生记录。每行是原始 JSON 文本。
func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func promptAt(id, ts, text string) string {
	return `{"type":"user","timestamp":"` + ts + `","promptId":"` + id + `","message":{"content":` + jsonString(text) + `}}`
}

func toolResult(ts, denial string) string {
	d := "null"
	if denial != "" {
		d = `"` + denial + `"`
	}
	return `{"type":"user","timestamp":"` + ts + `","toolUseResult":{"ok":true},"toolDenialKind":` + d + `,"message":{"content":[]}}`
}

func assistant(ts, id string, in, out int, blocks string) string {
	return `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"` + id + `","usage":{"input_tokens":` +
		itoa(in) + `,"output_tokens":` + itoa(out) + `},"content":` + blocks + `}}`
}

func costState(ts string, cost float64, in int) string {
	return `{"type":"cost-state","timestamp":"` + ts + `","totalCostUSD":` + ftoa(cost) +
		`,"totalDuration":1000,"totalAPIDuration":200,"totalToolDuration":100,"hasUnknownModelCost":false,"modelUsage":{"m":{"inputTokens":` + itoa(in) + `}}}`
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func ftoa(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func sumCounts(list []ToolCount) int {
	n := 0
	for _, c := range list {
		n += c.Count
	}
	return n
}

// 核心口径：同一条 assistant 消息写多行，**不能按行累加**（会翻倍）；
// 工具名散在不同行里，要取并集。
func TestScanDedupesByMessageIDAndUnionsTools(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		promptAt("p1", "2026-10-07T12:00:00Z", "你好"),
		assistant("2026-10-07T12:00:01Z", "m1", 100, 0, `[{"type":"thinking","thinking":"…"}]`),
		assistant("2026-10-07T12:00:02Z", "m1", 100, 40, `[{"type":"tool_use","name":"Bash","id":"c1"}]`),
		toolResult("2026-10-07T12:00:03Z", "permission-rule"),
		assistant("2026-10-07T12:00:04Z", "m2", 200, 10, `[{"type":"tool_use","name":"Read","id":"c2"}]`),
		assistant("2026-10-07T12:00:05Z", "m2", 200, 10, `[{"type":"tool_use","name":"Glob","id":"c3"}]`),
		costState("2026-10-07T12:00:06Z", 0.5, 300),
	)
	var s Session
	if _, err := scan(p, &s); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if s.Usage.In != 300 || s.Usage.Out != 50 {
		t.Errorf("用量按 message.id 合并后应为 in 300 / out 50（按行加会得到 600），拿到 in %d / out %d",
			s.Usage.In, s.Usage.Out)
	}
	if got := sumCounts(s.Tools); got != 3 {
		t.Errorf("工具该是 3 次（Bash/Read/Glob 各一），拿到 %d：%+v", got, s.Tools)
	}
	if len(s.Denials) != 1 || s.Denials[0].Name != "permission-rule" || s.Denials[0].Count != 1 {
		t.Errorf("被拒该按 kind 记 1 次，拿到 %+v", s.Denials)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("该只有一轮，拿到 %d", len(s.Turns))
	}
	if s.Turns[0].Denied != 1 {
		t.Errorf("这一轮该记 1 次被拒，拿到 %d", s.Turns[0].Denied)
	}
}

// cost-state 是**累计快照**不是流水：取最后一次，不能相加。
func TestScanCostStateLastWins(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		promptAt("p1", "2026-10-07T12:00:00Z", "一"),
		costState("2026-10-07T12:00:10Z", 0.25, 100),
		promptAt("p2", "2026-10-07T12:01:00Z", "二"),
		costState("2026-10-07T12:01:10Z", 0.75, 400),
	)
	var s Session
	if _, err := scan(p, &s); err != nil {
		t.Fatal(err)
	}
	if s.CostUSD != 0.75 {
		t.Errorf("成本该取最后一次快照 0.75，拿到 %v", s.CostUSD)
	}
}

// 同一个 promptId 写多行仍然是一轮；工具结果不算新的一轮。
func TestScanTurnsKeyedByPromptID(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		promptAt("p1", "2026-10-07T12:00:00Z", "一"),
		promptAt("p1", "2026-10-07T12:00:01Z", "一（重复落盘）"),
		toolResult("2026-10-07T12:00:02Z", ""),
		promptAt("p2", "2026-10-07T12:02:00Z", "二"),
	)
	var s Session
	if _, err := scan(p, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Turns) != 2 {
		t.Fatalf("该是 2 轮，拿到 %d：%+v", len(s.Turns), s.Turns)
	}
	if s.Turns[0].Prompt != "一" {
		t.Errorf("第一轮的原话该是第一次那行，拿到 %q", s.Turns[0].Prompt)
	}
}

// 一轮的耗时 = 这一轮最后一条记录 − 起点。**不能拿下一轮的开始时间当结束** ——
// 中间的空闲（人去开会了）会被算成 agent 干了半小时。
func TestScanTurnDurationIgnoresIdleGap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		promptAt("p1", "2026-10-07T12:00:00Z", "一"),
		assistant("2026-10-07T12:00:05Z", "m1", 10, 5, `[{"type":"text","text":"好了"}]`),
		promptAt("p2", "2026-10-07T12:30:00Z", "半小时后我又来了"),
		assistant("2026-10-07T12:30:02Z", "m2", 10, 5, `[{"type":"text","text":"在"}]`),
	)
	var s Session
	if _, err := scan(p, &s); err != nil {
		t.Fatal(err)
	}
	if got := s.Turns[0].Duration; got != 5*time.Second {
		t.Errorf("第一轮该是 5s（不含空闲），拿到 %v", got)
	}
	if got := s.Turns[1].Duration; got != 2*time.Second {
		t.Errorf("第二轮该是 2s，拿到 %v", got)
	}
}

// 一行的体量可能很大（整段工具输出都塞在行里）：超过 bufio 默认 64KB 也必须读得动 ——
// 读不动是「读不到」，不是「没有」。
func TestScanHandlesHugeLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	big := strings.Repeat("啊", 200000) // ~600KB，远超 64KB
	writeLines(t, p,
		promptAt("p1", "2026-10-07T12:00:00Z", big),
		assistant("2026-10-07T12:00:01Z", "m1", 1, 1, `[{"type":"text","text":"收到"}]`),
	)
	var s Session
	if _, err := scan(p, &s); err != nil {
		t.Fatalf("长行读不动：%v", err)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("该是 1 轮，拿到 %d", len(s.Turns))
	}
	if r := []rune(s.Turns[0].Prompt); len(r) > promptHead+1 {
		t.Errorf("提示语该被截断到 %d 字，拿到 %d", promptHead, len(r))
	}
}

// 子任务的 token 算在这个 bot 头上，并且要能归到拉起它的那一轮。
func TestReadSessionMergesSubagent(t *testing.T) {
	dir := t.TempDir()
	id := "sess-1"
	workDir := `D:\work`
	// 记录不在 claudeHome 根上，而在 projects/<slug(work_dir)>/ 下 —— 这正是被测的解析规则。
	base := filepath.Join(dir, "projects", Slug(workDir))
	main := filepath.Join(base, id+".jsonl")
	writeLines(t, main,
		promptAt("p1", "2026-10-07T12:00:00Z", "帮我查点东西"),
		assistant("2026-10-07T12:00:01Z", "m1", 100, 10, `[{"type":"tool_use","name":"Agent","id":"call-A"}]`),
		costState("2026-10-07T12:00:20Z", 0.3, 400),
	)
	sub := filepath.Join(base, id, "subagents", "agent-x.jsonl")
	writeLines(t, sub,
		promptAt("sp1", "2026-10-07T12:00:02Z", "去找"),
		assistant("2026-10-07T12:00:05Z", "sm1", 300, 50, `[{"type":"tool_use","name":"Grep","id":"c9"}]`),
	)
	meta, _ := json.Marshal(map[string]string{
		"agentType": "Explore", "description": "去找", "toolUseId": "call-A",
	})
	writeLines(t, strings.TrimSuffix(sub, ".jsonl")+".meta.json", string(meta))

	s := ReadSession("demo-alice", "s1", false, dir, workDir, id, "claudecode")
	if !s.Found {
		t.Fatal("该找得到记录")
	}
	if s.Usage.In != 400 || s.Usage.Out != 60 {
		t.Errorf("会话总账该含子任务：想要 in 400 / out 60，拿到 in %d / out %d", s.Usage.In, s.Usage.Out)
	}
	if len(s.Subagents) != 1 {
		t.Fatalf("该有 1 个子任务，拿到 %d", len(s.Subagents))
	}
	sa := s.Subagents[0]
	if sa.Agent != "Explore" || sa.Turn != 1 || sa.Usage.In != 300 {
		t.Errorf("子任务该归到第 1 轮且带上自己的账，拿到 %+v", sa)
	}
	if s.Turns[0].Usage.In != 400 {
		t.Errorf("那一轮的账该含子任务：想要 in 400，拿到 %d", s.Turns[0].Usage.In)
	}
	if got := sumCounts(s.Tools); got != 2 {
		t.Errorf("工具该是 2 次（Agent + 子任务的 Grep），拿到 %d：%+v", got, s.Tools)
	}
	if len(s.Problems) != 0 {
		t.Errorf("这个现场不该有问题：%+v", s.Problems)
	}
}

// 原生记录找不到时：不报错、不当 0，而是明说「读不到」。
func TestReadSessionReportsMissingTranscript(t *testing.T) {
	s := ReadSession("demo-alice", "s1", false, t.TempDir(), `D:\nowhere`, "nope", "claudecode")
	if s.Found {
		t.Fatal("找不到记录就不该标 Found")
	}
	if len(s.Problems) != 1 || !strings.Contains(s.Problems[0], "读不到") {
		t.Errorf("该明说读不到，拿到 %+v", s.Problems)
	}
	if s.Usage.In != 0 {
		t.Errorf("读不到时用量该是 0 而不是编出来：%+v", s.Usage)
	}
}

// 解析不了的行要数出来 —— 格式变了就得说，不能让账默默变少。
func TestScanReportsBadLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		promptAt("p1", "2026-10-07T12:00:00Z", "一"),
		"{这不是 JSON",
	)
	var s Session
	if _, err := scan(p, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Problems) != 1 || !strings.Contains(s.Problems[0], "解析不了") {
		t.Errorf("该报出坏行，拿到 %+v", s.Problems)
	}
}

// 桥：一个槽换过会话时，历史 id 也要算进来（只报当前的会把之前的工作整段丢掉）。
func TestBridges(t *testing.T) {
	p := filepath.Join(t.TempDir(), "demo-alice_x.json")
	doc := `{"sessions":{
	  "s1":{"agent_session_id":"cur-1","past_agent_session_ids":["old-1","cur-1"],"agent_type":"claudecode"},
	  "s2":{"agent_session_id":"","past_agent_session_ids":[],"history":null},
	  "s3":{"agent_session_id":"","past_agent_session_ids":["old-2"]}
	}}`
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	bs, err := Bridges("demo-alice", p)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 2 {
		t.Fatalf("s2 从没起来过该被跳过：拿到 %d 条 %+v", len(bs), bs)
	}
	if bs[0].SessionID != "cur-1" || len(bs[0].PastIDs) != 1 || bs[0].PastIDs[0] != "old-1" {
		t.Errorf("s1 该有当前 + 1 个历史（当前 id 重复出现要去掉）：%+v", bs[0])
	}
	if bs[1].SessionID != "" || bs[1].PastIDs[0] != "old-2" {
		t.Errorf("s3 只有历史 id 也要收：%+v", bs[1])
	}
}

func TestBridgesBadJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.json")
	if err := os.WriteFile(p, []byte("{坏"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Bridges("p", p); err == nil {
		t.Fatal("坏 JSON 必须报错，不许静默当空")
	}
}

// Usage 的加法与「缓存读不进总数」的口径。
func TestUsageAddAndTotal(t *testing.T) {
	a := Usage{In: 1, Out: 2, CacheRead: 3, CacheWrite: 4}
	b := Usage{In: 10, Out: 20, CacheRead: 30, CacheWrite: 40}
	got := a.Add(b)
	if got.In != 11 || got.Out != 22 || got.CacheRead != 33 || got.CacheWrite != 44 {
		t.Errorf("相加不对：%+v", got)
	}
	if got.Total() != 33 {
		t.Errorf("总数该只算 in+out（缓存读单列），拿到 %d", got.Total())
	}
}

package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeShard 在 harness 记录目录里放一份会话分片（格式照实测的原生记录）。
func writeShard(t *testing.T, home, workDir, session string, lines ...string) {
	t.Helper()
	dir := filepath.Join(home, "projects", slugDir(workDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, session+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func use(id, tool, input string) string {
	return `{"type":"assistant","timestamp":"2026-10-08T10:00:0` + `0.000Z","message":{"content":[` +
		`{"type":"tool_use","id":"` + id + `","name":"` + tool + `","input":` + input + `}]}}`
}

func result(id, content string, isErr bool, denial string) string {
	d := ""
	if denial != "" {
		d = `"toolDenialKind":"` + denial + `",`
	}
	e := "false"
	if isErr {
		e = "true"
	}
	return `{"type":"user","timestamp":"2026-10-08T10:00:01.000Z",` + d +
		`"message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","is_error":` + e +
		`,"content":"` + content + `"}]}}`
}

func collectIn(t *testing.T, home, workDir string, opt CollectOptions) CollectResult {
	t.Helper()
	if opt.ClaudeHome == "" {
		opt.ClaudeHome = home
	}
	if opt.Projects == nil {
		opt.Projects = map[string]string{"demo-alice": workDir}
	}
	return Collect(opt)
}

func TestCollectReadsOkDeniedAndFailed(t *testing.T) {
	home := t.TempDir()
	wd := filepath.Join(t.TempDir(), "homes", "alice")
	writeShard(t, home, wd, "s1",
		use("t1", "Read", `{"file_path":"/vault/10-knowledge/a.md"}`),
		result("t1", "file body", false, ""),
		use("t2", "Write", `{"file_path":"/vault/20-ops/b.md"}`),
		result("t2", "Permission to use Write has been denied because dont ask mode. IMPORTANT: 这是给模型看的长段提示", true, "permission"),
		use("t3", "Edit", `{"file_path":"/vault/20-ops/c.md"}`),
		result("t3", "File does not exist.", true, ""),
	)
	res := collectIn(t, home, wd, CollectOptions{ActorOf: func(string) string { return "alice" }})
	if len(res.Problems) != 0 {
		t.Fatalf("不该有记不到的：%v", res.Problems)
	}
	byID := map[string]Record{}
	for _, r := range res.Records {
		byID[strings.SplitN(r.ID, ":", 2)[1]] = r
	}
	if len(byID) != 3 {
		t.Fatalf("想要 3 条，得到 %d", len(byID))
	}
	if r := byID["t1"]; r.Result != ResultOK || r.Object != "/vault/10-knowledge/a.md" ||
		r.Action != ActionRead || r.Actor != "alice" || r.At == "" {
		t.Errorf("t1 五样没齐：%+v", r)
	}
	if r := byID["t2"]; r.Result != ResultDenied {
		t.Errorf("被权限规则挡下应当是 denied，得到 %q", r.Result)
	} else if strings.Contains(r.Why, "IMPORTANT") {
		t.Errorf("拒绝理由要切掉给模型看的那段，得到 %q", r.Why)
	}
	if r := byID["t3"]; r.Result != ResultFailed {
		t.Errorf("真执行失败应当是 failed（不是 denied），得到 %q", r.Result)
	}
	if res.Calls != 3 || res.Shards != 1 {
		t.Errorf("calls/shards = %d/%d，想要 3/1", res.Calls, res.Shards)
	}
}

// 结果配不上 = 那一轮被中断。**不许当成「没发生」**，也不许猜一个结果出来。
func TestCollectUnpairedResultIsUnknownAndReported(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	writeShard(t, home, wd, "s1", use("t1", "Read", `{"file_path":"/v/a.md"}`))
	res := collectIn(t, home, wd, CollectOptions{})
	if len(res.Records) != 1 || res.Records[0].Result != ResultUnknown {
		t.Fatalf("想要一条 unknown，得到 %+v", res.Records)
	}
	if len(res.Problems) == 0 || !strings.Contains(res.Problems[0], "没有配到结果") {
		t.Errorf("必须报出来：%v", res.Problems)
	}
}

func TestCollectReportsUnregisteredToolAndMissingTarget(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	writeShard(t, home, wd, "s1",
		use("t1", "TodoWrite", `{"todos":[]}`),
		result("t1", "ok", false, ""),
		use("t2", "Bash", `{"command":"ls -la /vault"}`),
		result("t2", "ok", false, ""),
		use("t3", "Read", `{"offset":10}`),
		result("t3", "ok", false, ""),
	)
	res := collectIn(t, home, wd, CollectOptions{})
	// ① 没登记的工具：不落流水，但必须报（不许静默丢）。
	for _, r := range res.Records {
		if r.Tool == "TodoWrite" {
			t.Error("没登记的工具不该落进流水")
		}
	}
	// ② 没登记目标键 / ③ 登记了但记录里没有 —— 都要报，且措辞要分得开。
	joined := strings.Join(res.Problems, "\n")
	if !strings.Contains(joined, "TodoWrite") || !strings.Contains(joined, "不在工具表里") {
		t.Errorf("没登记的工具要报出来：%v", res.Problems)
	}
	if !strings.Contains(joined, "没有它的目标键") {
		t.Errorf("Bash 那类要报「没有目标键」：%v", res.Problems)
	}
	if !strings.Contains(joined, "登记了目标键但记录里是空的") {
		t.Errorf("Read 缺 file_path 要报「记录里是空的」—— 与上一条不是一回事：%v", res.Problems)
	}
}

// 同一条 tool_use 会被写多行（流式中间态 input 还没流完）—— 后到的覆盖先到的。
func TestCollectLastWriteWinsForStreamedToolUse(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	writeShard(t, home, wd, "s1",
		use("t1", "Read", `{}`),
		use("t1", "Read", `{"file_path":"/vault/10-knowledge/final.md"}`),
		result("t1", "body", false, ""),
	)
	res := collectIn(t, home, wd, CollectOptions{})
	if len(res.Records) != 1 {
		t.Fatalf("同一条调用只该落一条，得到 %d", len(res.Records))
	}
	if res.Records[0].Object != "/vault/10-knowledge/final.md" {
		t.Errorf("该取最后那份完整的 input，得到 %q", res.Records[0].Object)
	}
	if len(res.Problems) != 0 {
		t.Errorf("中间态不该被当成「记录里是空的」：%v", res.Problems)
	}
}

func TestCollectSkipsKnownAndRespectsSince(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	writeShard(t, home, wd, "s1",
		use("t1", "Read", `{"file_path":"/v/a.md"}`),
		result("t1", "ok", false, ""),
	)
	// 幂等：id 记过就不再写第二条。
	res := collectIn(t, home, wd, CollectOptions{Known: map[string]bool{"s1:t1": true}})
	if len(res.Records) != 0 {
		t.Errorf("已记过的应当跳过，得到 %d 条", len(res.Records))
	}
	// Since：只收这个时刻之后的。
	since, _ := time.Parse(time.RFC3339, "2026-10-09T00:00:00Z")
	res = collectIn(t, home, wd, CollectOptions{Since: since})
	if len(res.Records) != 0 {
		t.Errorf("记录在 since 之前，应当过滤掉，得到 %d 条", len(res.Records))
	}
}

// 记录目录不在 = 这个 bot 还没跑过会话（或没跑在这台机器上）：
// 那是「还没发生」，不是「读不到」—— 不报 Problem。
func TestCollectMissingDirIsNotAProblem(t *testing.T) {
	home := t.TempDir()
	res := collectIn(t, home, filepath.Join(t.TempDir(), "nope"), CollectOptions{})
	if len(res.Problems) != 0 || len(res.Records) != 0 {
		t.Errorf("想要什么都报不出来，得到 %+v / %v", res.Records, res.Problems)
	}
}

func TestBuiltinToolsCoverClaudeNativeSet(t *testing.T) {
	byTool := map[string]ToolRule{}
	for _, r := range BuiltinTools() {
		byTool[r.Tool] = r
	}
	// 实测过的四种工具（2026-10-08 的 demo 会话里就这几个）。
	for _, tool := range []string{"Read", "Glob", "Bash", "Agent"} {
		if _, ok := byTool[tool]; !ok {
			t.Errorf("%s 不在出厂工具表里", tool)
		}
	}
	if byTool["Read"].Target[0] != "file_path" {
		t.Error("Read 的「对谁」是 file_path")
	}
	if byTool["Write"].Action != ActionWrite || byTool["Read"].Action != ActionRead {
		t.Error("读写归类不对")
	}
	for _, tool := range []string{"Bash", "Agent", "Task"} {
		if len(byTool[tool].Target) != 0 {
			t.Errorf("%s 的 input 是自由文本，不该登记目标键", tool)
		}
	}
}

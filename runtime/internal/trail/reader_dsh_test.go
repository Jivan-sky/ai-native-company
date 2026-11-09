package trail

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dshFixture 是照 2026-11-09 本机真记录的形状缩出来的一段：一轮、两次工具调用（一成一败）。
// 真记录里 assistant 的 usage 每个 step 都带一条，所以这里有两条 —— 累加才是这一轮的数。
const dshFixture = `{"type":"session","version":4,"id":"s-1","createdAt":"2026-11-09T03:00:00Z","cwd":"/Users/x/acme"}
{"type":"turn/start","seq":1,"time":1794129600000,"data":{"turn":1}}
{"type":"user/message","seq":2,"time":1794129600100,"data":{"role":"user","content":[{"type":"text","text":"把结算口径找出来"}],"id":"u1"}}
{"type":"assistant/message","seq":3,"time":1794129601000,"data":{"turn":1,"step":1,"usage":{"inputTokens":100,"outputTokens":20,"cacheReadTokens":50,"cacheWriteTokens":0},"message":{"role":"assistant","content":[{"type":"reasoning","text":"先读文件"}]}}}
{"type":"tool/call","seq":4,"time":1794129601100,"data":{"turn":1,"step":1,"callId":"c1","name":"read","arguments":"{\"path\":\"10-knowledge/settle.md\"}"}}
{"type":"tool/result","seq":5,"time":1794129601200,"data":{"turn":1,"step":1,"message":{"toolCallId":"c1","content":[{"type":"text","text":"结算周期按自然月"}],"isError":false},"meta":{"cwd":"/Users/x/acme"}}}
{"type":"assistant/message","seq":6,"time":1794129602000,"data":{"turn":1,"step":2,"usage":{"inputTokens":40,"outputTokens":10,"cacheReadTokens":0,"cacheWriteTokens":0},"message":{"role":"assistant","content":[{"type":"text","text":"再看一下"}]}}}
{"type":"tool/call","seq":7,"time":1794129602100,"data":{"turn":1,"step":2,"callId":"c2","name":"bash","arguments":"{\"command\":\"cat nope.md\"}"}}
{"type":"tool/result","seq":8,"time":1794129602200,"data":{"turn":1,"step":2,"message":{"toolCallId":"c2","content":[{"type":"text","text":"No such file or directory"}],"isError":true},"meta":{}}}
{"type":"turn/end","seq":9,"time":1794129603000,"data":{"turn":1,"reason":{"kind":"completed"}}}
`

func writeDSHFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "session.v4.jsonl")
	if err := os.WriteFile(p, []byte(dshFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// 判据：一轮里两次调用（一成一败）、用量按 step 累加、失败带记录原话、成本标成「不知道」。
func TestScanDSH(t *testing.T) {
	s := &Session{Turns: []Turn{}}
	uses, err := scanDSH(writeDSHFixture(t), s)
	if err != nil {
		t.Fatal(err)
	}
	if s.Found {
		t.Fatal("Found 是 ReadSessionIn 的事：读取器只填内容，不替调用方下「找到了」的结论")
	}
	if s.ID != "s-1" || s.Cwd != "/Users/x/acme" {
		t.Fatalf("头部没读对：id=%q cwd=%q", s.ID, s.Cwd)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("应该 1 轮，实际 %d", len(s.Turns))
	}
	turn := s.Turns[0]
	if turn.Prompt != "把结算口径找出来" {
		t.Fatalf("这一轮的提问没读到：%q", turn.Prompt)
	}
	want := Usage{In: 140, Out: 30, CacheRead: 50, CacheWrite: 0}
	if turn.Usage != want || s.Usage != want {
		t.Fatalf("用量要按 step 累加：turn=%+v session=%+v 期望 %+v", turn.Usage, s.Usage, want)
	}
	if turn.Duration != 3*1e9 {
		t.Fatalf("轮时长应按事件时间算：%v", turn.Duration)
	}
	if len(s.Tools) != 2 {
		t.Fatalf("两次工具调用应该算两个工具：%+v", s.Tools)
	}
	if len(s.Acts) != 2 || s.Acts[0].Result != ResultOK || s.Acts[1].Result != ResultFailed {
		t.Fatalf("两次调用的结果没归一对：%+v", s.Acts)
	}
	// arguments 在记录里是「字符串套 JSON」，要解开成真 JSON 才好原样带走。
	if string(s.Acts[0].Input) != `{"path":"10-knowledge/settle.md"}` {
		t.Fatalf("入参没解开：%s", s.Acts[0].Input)
	}
	if len(s.Failures) != 1 || !strings.Contains(s.Failures[0].Why, "No such file") {
		t.Fatalf("失败要带记录里的原话：%+v", s.Failures)
	}
	if !s.UnknownCost {
		t.Fatal("DSH 记录里没有金额 —— 必须标成不知道，不能写 0")
	}
	if len(uses) != 2 {
		t.Fatalf("callId → 下标应该两个：%v", uses)
	}
	found := false
	for _, p := range s.Problems {
		if strings.Contains(p, "金额") {
			found = true
		}
	}
	if !found {
		t.Fatalf("「只出用量不算钱」要写进 Problems，不能只靠调用方猜：%v", s.Problems)
	}
}

// 真形态是 zstd 压缩的。这条用例走完整条解压链（借宿主 zstd），没有 zstd 就跳过 ——
// 跳过要显式说，不能默默算过。
func TestScanDSHZstd(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("本机没有 zstd，跳过解压链用例")
	}
	src := writeDSHFixture(t)
	out := filepath.Join(t.TempDir(), "session.v4.jsonl.zstd")
	cmd := exec.Command("zstd", "-q", "-f", "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("压缩样本失败：%v %s", err, b)
	}

	s := &Session{Turns: []Turn{}}
	if _, err := scanDSH(out, s); err != nil {
		t.Fatalf("zstd 记录应当读得动：%v", err)
	}
	if len(s.Turns) != 1 || s.Usage.In != 140 {
		t.Fatalf("解压后的账不对：turns=%d usage=%+v", len(s.Turns), s.Usage)
	}
}

// 没有 zstd 时必须**报错**（说清这条没读），不许静默返回 0 —— 「读不到」不能装成「没有」。
func TestDSHWithoutZstdSaysSo(t *testing.T) {
	src := writeDSHFixture(t)
	out := filepath.Join(t.TempDir(), "x.jsonl.zstd")
	if err := os.WriteFile(out, []byte("not really zstd"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty"))
	_, err := scanDSH(out, &Session{})
	if err == nil {
		t.Fatal("没有 zstd 时应当报错")
	}
	if !strings.Contains(err.Error(), "zstd") || !strings.Contains(err.Error(), "没读") {
		t.Fatalf("错误要说清「这台机器没有 zstd，这条没读」：%v", err)
	}
	_ = src
}

// TestScanDSHRealRecord 吃**一份真记录**（路径由 ANC_DSH_REAL 给，没给就跳过）。
//
// 为什么留这个口子：合成样本只能证明「按我理解的形状读得对」，证明不了
// 「DSH 真写出来的形状就是这个」。真记录里字段只会更多、不会更少。
// 判据只要求「读得动、不崩、账比 0 大」——不锁具体数字（那是别人机器上的会话）。
func TestScanDSHRealRecord(t *testing.T) {
	path := os.Getenv("ANC_DSH_REAL")
	if path == "" {
		t.Skip("没给 ANC_DSH_REAL，跳过真记录用例")
	}
	s := &Session{Turns: []Turn{}}
	if _, err := scanDSH(path, s); err != nil {
		t.Fatalf("真记录读不动：%v", err)
	}
	if len(s.Turns) == 0 {
		t.Fatalf("真记录应当至少一轮：%+v", s)
	}
	if s.Usage.Total() == 0 {
		t.Fatalf("真记录应当有用量：%+v", s.Usage)
	}
	if s.Cwd == "" {
		t.Fatal("真记录应当自带 cwd（认领靠它）")
	}
	t.Logf("真记录：%d 轮 / 用量 %+v / 工具 %d 种 / 动作 %d 次 / 失败 %d 条 / 问题 %d 条",
		len(s.Turns), s.Usage, len(s.Tools), len(s.Acts), len(s.Failures), len(s.Problems))
}

package trail

// 故障注入：先写「预测会怎么坏」，再看实际。读取器回归时这里必须红 ——
// 这是这条链路唯一的裁判（DESIGN §7.1.84 的「没做的」第一项）。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func scanBroken(t *testing.T, name string, body string) (*Session, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Session{Turns: []Turn{}}
	_, err := scanDSH(p, s)
	return s, err
}

// 预测：最后一行被截断 → 前面的事件照算，末尾缺的那截进 Problems（不许静默当完整）。
func TestFaultHalfLine(t *testing.T) {
	lines := strings.Split(strings.TrimRight(dshFixture, "\n"), "\n")
	body := strings.Join(lines[:len(lines)-1], "\n") + "\n" + `{"type":"tool/call","seq":99,"time":1794129`
	s, err := scanBroken(t, "half.jsonl", body)
	if err != nil {
		t.Fatalf("半行不该让整条读失败（读到的要照算）：%v", err)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("前面那一轮应当读到：%+v", s.Turns)
	}
	found := false
	for _, p := range s.Problems {
		if strings.Contains(p, "断了") {
			found = true
		}
	}
	if !found {
		t.Fatalf("末尾被截断必须报出来，实际 Problems=%v", s.Problems)
	}
}

// 预测：同一条 tool/call 出现两次（同 callId）→ 读取器按事件记两次（记录里真有两条），
// 台账那侧靠 callId 去重 —— 这一层的数字会翻，**这是事实层的选择**，得钉住别被悄悄改成 1。
func TestFaultDuplicateCall(t *testing.T) {
	var call string
	for _, ln := range strings.Split(dshFixture, "\n") {
		if strings.HasPrefix(ln, `{"type":"tool/call"`) {
			call = ln
			break
		}
	}
	if call == "" {
		t.Fatal("夹具里没有 tool/call 行，这条用例的前提没了")
	}
	body := strings.Replace(dshFixture, call, call+"\n"+call, 1)
	s, err := scanBroken(t, "dup.jsonl", body)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Acts) != 3 {
		t.Fatalf("重复的 callId 在事实层算两次（原 2 次 + 重复 1 次 = 3），实际 %d：%+v", len(s.Acts), s.Acts)
	}
}

// 预测：记录谎报 usage=0 → 账就是 0，但**不能**因此变成「不花钱」的假账：
// UnknownCost 仍要为真（DSH 本来就没有金额）。
func TestFaultZeroUsageIsStillUnknownCost(t *testing.T) {
	body := strings.Replace(dshFixture,
		`"usage":{"inputTokens":100,"outputTokens":20,"cacheReadTokens":50,"cacheWriteTokens":0}`,
		`"usage":{"inputTokens":0,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0}`, 1)
	s, err := scanBroken(t, "zero.jsonl", body)
	if err != nil {
		t.Fatal(err)
	}
	if s.Usage.In != 40 || s.Usage.Out != 10 {
		t.Fatalf("只该剩第二条 assistant 的用量：%+v", s.Usage)
	}
	if !s.UnknownCost {
		t.Fatal("用量是 0 也不等于「知道花了多少钱」")
	}
}

// 预测：时钟倒流（turn/end 早于 turn/start）→ 时长不许是负数（负数会被下游当成「跑了很久以前」）。
func TestFaultClockBackwards(t *testing.T) {
	body := strings.Replace(dshFixture,
		`{"type":"turn/end","seq":9,"time":1794129603000,"data":{"turn":1,"reason":{"kind":"completed"}}}`,
		`{"type":"turn/end","seq":9,"time":1794129500000,"data":{"turn":1,"reason":{"kind":"completed"}}}`, 1)
	s, err := scanBroken(t, "back.jsonl", body)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("轮数不对：%+v", s.Turns)
	}
	if s.Turns[0].Duration < 0 {
		t.Fatalf("时长为负：%v（时钟倒流应当报缺，不该记一个负数）", s.Turns[0].Duration)
	}
}

// 预测：文件里有非 UTF-8 字节 → 解析到那行停住，已经读到的照算 + Problem，进程不崩。
func TestFaultNonUTF8(t *testing.T) {
	body := dshFixture + "\xff\xfe garbage\n"
	s, err := scanBroken(t, "bin.jsonl", body)
	if err != nil {
		t.Fatalf("非 UTF-8 不该让整条读失败：%v", err)
	}
	if len(s.Turns) != 1 {
		t.Fatalf("前面的事件应当照算：%+v", s.Turns)
	}
}

// 预测：截断的 zstd（压缩到一半砍掉）→ 要么报错、要么读到一部分 + Problem；
// **不许**安静地返回「一条事件都没有、也没有问题」。
func TestFaultTruncatedZstd(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("本机没有 zstd，跳过")
	}
	dir := t.TempDir()
	plain := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(plain, []byte(dshFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, "s.zstd")
	if out, err := exec.Command("zstd", "-q", "-f", "-o", full, plain).CombinedOutput(); err != nil {
		t.Fatalf("压缩失败：%v %s", err, out)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	cut := filepath.Join(dir, "cut.jsonl.zstd")
	if err := os.WriteFile(cut, b[:len(b)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Session{Turns: []Turn{}}
	_, serr := scanDSH(cut, s)
	if serr == nil && len(s.Turns) == 0 && len(s.Problems) == 0 {
		t.Fatal("截断的记录被安静吞掉了：既没报错、也没读到东西、也没有 Problem —— 那是假绿")
	}
}

// 预测：一条没有 name 的 tool/call → 不算进行使，但必须报出来（静默丢 = 替记录说「没发生」）。
func TestFaultNamelessCallIsReported(t *testing.T) {
	body := dshFixture + `{"type":"tool/call","seq":99,"time":1794129609000,"data":{"turn":1,"step":9,"callId":"cx","name":"","arguments":"{}"}}` + "\n"
	s, err := scanBroken(t, "noname.jsonl", body)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Acts) != 2 {
		t.Fatalf("无名调用不该变成一次行使：%+v", s.Acts)
	}
	found := false
	for _, p := range s.Problems {
		if strings.Contains(p, "没有 name") {
			found = true
		}
	}
	if !found {
		t.Fatalf("无名调用必须报出来，实际 Problems=%v", s.Problems)
	}
}

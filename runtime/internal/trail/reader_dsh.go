package trail

// DSH 读取器：把 `~/.dsh/sessions/<mangled-cwd>/<id>/session.vN.jsonl.zstd` 读成一份会话账。
//
// **为什么以前读不了**：这份记录是 **zstd 压缩**的 jsonl，而本仓库钉死「零第三方依赖」
// （单 exe、`CGO_ENABLED=0`、交叉编译零障碍）。标准库没有 zstd 解压，引一个实现就是
// 往交付物里塞几 MB —— 所以 2026-10-09 拍板「先登记不读」。
//
// **现在怎么办的**：不引 Go 依赖，改成**借宿主上的 `zstd` 命令行**（`zstd -dc`）。
// 三条口径：
//
//  1. 找得到 `zstd` 就读；找不到就**如实说读不了**（带一句怎么装），不静默返回 0 消耗 ——
//     「读不到」装成「没有」是这个项目最怕的假绿。
//  2. 解压在管道里流式做，不落临时文件（记录里是客户会话，别在盘上多留一份）。
//  3. 解压出来的形状按事件类型归一（`session` / `turn/start` / `user/message` /
//     `assistant/message` / `tool/call` / `tool/result` / `turn/end`）——
//     这份形状是 2026-11-09 在本机真实记录上验的，不是照文档猜的。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ReaderDSH 是口径表里这一家的 reader 名字。
const ReaderDSH = "dsh-jsonl"

// dshEvent 是一条事件。头部那行（`type=session`）的字段直接在顶层，事件在 data 里 ——
// 两种都收，按 type 分派。
type dshEvent struct {
	Type string          `json:"type"`
	Seq  int64           `json:"seq"`
	Time int64           `json:"time"` // epoch 毫秒
	Data json.RawMessage `json:"data"`

	// 头部那一行（type=session）
	ID string `json:"id"`
	// CreatedAt 在真记录里是 **epoch 毫秒（数字）**，不是 RFC3339 串 —— 2026-11-09 真记录用例
	// 第一次跑就报 "cannot unmarshal number into ... of type string"。两种都收，别猜。
	CreatedAt json.RawMessage `json:"createdAt"`
	Cwd       string          `json:"cwd"`
	Version   int             `json:"version"`
}

type dshUsage struct {
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	CacheReadTokens  int `json:"cacheReadTokens"`
	CacheWriteTokens int `json:"cacheWriteTokens"`
}

type dshMsg struct {
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Source struct {
		Kind     string `json:"kind"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	} `json:"source"`
	ToolCallID string `json:"toolCallId"`
	IsError    bool   `json:"isError"`
}

// scanDSH 读一份 DSH 会话记录（.jsonl 或 .jsonl.zstd 都收）。
func scanDSH(path string, s *Session) (map[string]int, error) {
	rc, err := openDSHRecord(path)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(rc)
	var (
		turnIdx   = -1
		actsAt    = map[string]int{} // callId → Acts 下标（tool/result 回来时补结果）
		sawEvent  bool
		decodeErr error
	)
	for {
		var e dshEvent
		if err := dec.Decode(&e); err != nil {
			if err == io.EOF {
				break
			}
			// 压缩流截断（进程被杀）时这里会报错：读到的部分照算，缺的那截必须说出来。
			decodeErr = err
			s.Problems = append(s.Problems, "DSH 记录读到一半断了（多半是会话被中断）："+err.Error())
			break
		}
		sawEvent = true
		at := timeFromMS(e.Time)
		if at.After(s.Ended) {
			s.Ended = at
		}
		switch e.Type {
		case "session":
			if e.ID != "" {
				s.ID = e.ID
			}
			if e.Cwd != "" {
				s.Cwd = e.Cwd
			}
			if t, ok := parseStartedAt(e.CreatedAt); ok {
				s.Started = t
			}

		case "turn/start":
			s.Turns = append(s.Turns, Turn{At: at})
			turnIdx = len(s.Turns) - 1

		case "user/message":
			var m dshMsg
			_ = json.Unmarshal(e.Data, &m)
			if turnIdx >= 0 && s.Turns[turnIdx].Prompt == "" {
				s.Turns[turnIdx].Prompt = firstText(m.Content)
			}

		case "assistant/message":
			var d struct {
				Usage dshUsage `json:"usage"`
				Msg   dshMsg   `json:"message"`
			}
			_ = json.Unmarshal(e.Data, &d)
			if s.Model == "" {
				s.Model = strings.TrimSpace(d.Msg.Source.Model)
			}
			u := Usage{In: d.Usage.InputTokens, Out: d.Usage.OutputTokens,
				CacheRead: d.Usage.CacheReadTokens, CacheWrite: d.Usage.CacheWriteTokens}
			s.Usage = s.Usage.Add(u)
			if turnIdx >= 0 {
				s.Turns[turnIdx].Usage = s.Turns[turnIdx].Usage.Add(u)
			}

		case "tool/call":
			var d struct {
				Turn      int             `json:"turn"`
				CallID    string          `json:"callId"`
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(e.Data, &d)
			if d.Name == "" {
				// 无名的调用**不能静默丢**：它是一次发生过、但读不出是什么的行使。
				// 丢了就等于替记录说「没发生」——标定脚本正是靠这条差异发现两边的口径不同。
				s.Problems = append(s.Problems, fmt.Sprintf(
					"有一条 tool/call 没有 name（callId=%q）：算不出它是什么工具，没算进行使", d.CallID))
				continue
			}
			// arguments 在记录里是**字符串**（JSON 再套一层）；解开成真 JSON 才好原样带走。
			arg := d.Arguments
			if un := unquoteJSON(arg); un != nil {
				arg = un
			}
			actsAt[d.CallID] = len(s.Acts)
			s.Acts = append(s.Acts, Act{CallID: d.CallID, At: at, Tool: d.Name, Input: arg, Result: ResultUnknown})
			if turnIdx >= 0 {
				s.Turns[turnIdx].Tools = bumpBy(s.Turns[turnIdx].Tools, d.Name, 1)
			}
			s.Tools = bumpBy(s.Tools, d.Name, 1)

		case "tool/result":
			var d struct {
				Msg  dshMsg `json:"message"`
				Meta struct {
					Cwd string `json:"cwd"`
				} `json:"meta"`
				Turn int `json:"turn"`
			}
			_ = json.Unmarshal(e.Data, &d)
			idx, ok := actsAt[d.Msg.ToolCallID]
			if !ok {
				continue
			}
			if d.Meta.Cwd != "" && s.Cwd == "" {
				s.Cwd = d.Meta.Cwd
			}
			why := firstText(d.Msg.Content)
			if d.Msg.IsError {
				s.Acts[idx].Result = ResultFailed
				s.Acts[idx].Why = truncate(why, 200)
				f := Failure{Kind: ResultFailed, Tool: s.Acts[idx].Tool, Why: truncate(why, 200), At: at}
				s.Failures = append(s.Failures, f)
				if turnIdx >= 0 {
					s.Turns[turnIdx].Failures = append(s.Turns[turnIdx].Failures, f)
				}
			} else {
				s.Acts[idx].Result = ResultOK
			}

		case "turn/end":
			var d struct {
				Reason struct {
					Kind string `json:"kind"`
				} `json:"reason"`
			}
			_ = json.Unmarshal(e.Data, &d)
			if turnIdx >= 0 && !s.Turns[turnIdx].At.IsZero() && !at.IsZero() {
				// 时钟倒流（结束早于开始）时**不记负数**：负时长会被下游当成「很久以前跑的」，
				// 比没有更坏。记 0 并报缺，让人知道这个数不可信（2026-11-10 故障注入抓到的）。
				if at.Before(s.Turns[turnIdx].At) {
					s.Problems = append(s.Problems, fmt.Sprintf(
						"第 %d 轮的结束时间早于开始时间（时钟倒流）—— 时长记 0，这个数不可信", turnIdx+1))
				} else {
					s.Turns[turnIdx].Duration = at.Sub(s.Turns[turnIdx].At)
				}
			}
			if k := strings.TrimSpace(d.Reason.Kind); k != "" && k != "completed" {
				s.Problems = append(s.Problems, fmt.Sprintf("第 %d 轮没跑完（reason=%s）—— 那一轮的账只记到中断处", turnIdx+1, k))
			}
		}
	}
	// 解压是个子进程：必须等它收尾，才知道「读到 EOF」是真读完了还是解压半路死了。
	// 不等的话，一条坏记录会被报成「空文件」—— 那是在替解压失败编理由。
	closeErr := rc.Close()
	if !sawEvent {
		if closeErr != nil {
			return nil, closeErr
		}
		if decodeErr != nil {
			// 一条都没读成的时候，真正的原因是这个 decode 错误，不是「空文件」——
			// 不把它带出来，读的人只会看到一句误导性的「空文件」。
			return nil, fmt.Errorf("记录读不动：%v", decodeErr)
		}
		return nil, fmt.Errorf("记录里一条事件都没有（空文件或解压失败）")
	}
	if s.Started.IsZero() {
		s.Started = s.Ended
	}
	// DSH 记录里没有金额，本仓库也没有它的价格表 —— 只出用量，不算钱。
	// 不写成 0：0 会被读成「不花钱」，那是假账。
	s.UnknownCost = true
	s.Problems = append(s.Problems, "DSH 记录里没有金额、ANC 也没有它的价格表：只出用量，不算钱")
	sortCounts(s.Tools)
	for i := range s.Turns {
		sortCounts(s.Turns[i].Tools)
	}
	return actsAt, nil
}

// openDSHRecord 打开一份记录：`.zstd` 结尾就走宿主上的 zstd，其余当普通文件读。
//
// 为什么借命令行而不是引 Go 依赖：交付物是单 exe、零第三方依赖（SPEC 与 runtimes/README 都钉死）。
// 借命令行的代价是「这台机器上可能没有 zstd」—— 那种情况下**必须报错**，不能静默返回 0。
func openDSHRecord(path string) (io.ReadCloser, error) {
	if !strings.HasSuffix(strings.ToLower(path), ".zstd") {
		return os.Open(path)
	}
	bin, err := exec.LookPath("zstd")
	if err != nil {
		return nil, fmt.Errorf("记录是 zstd 压缩的，但这台机器上没有 zstd 命令（本仓库不内嵌解压实现）；" +
			"装一个再来：macOS `brew install zstd` / Debian `apt install zstd`。这条记录这次**没读**")
	}
	cmd := exec.Command(bin, "-dc", "--", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("起不来 zstd：%v", err)
	}
	return &zstdReader{pipe: out, cmd: cmd, stderr: &stderr}, nil
}

// zstdReader 把「读管道」和「等进程」绑在一起：Close 时等子进程收尾，并把它的 stderr 带出来。
type zstdReader struct {
	pipe   io.ReadCloser
	cmd    *exec.Cmd
	stderr *bytes.Buffer
}

func (z *zstdReader) Read(p []byte) (int, error) { return z.pipe.Read(p) }

func (z *zstdReader) Close() error {
	err := z.pipe.Close()
	werr := z.cmd.Wait()
	if werr != nil && z.stderr.Len() > 0 {
		return fmt.Errorf("zstd 解压失败：%s", strings.TrimSpace(z.stderr.String()))
	}
	return err
}

// unquoteJSON 把「JSON 字符串里套着 JSON」那一层解开；解不开就返回 nil（原样留着）。
func unquoteJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || raw[0] != '"' {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil
	}
	if !json.Valid([]byte(s)) {
		return nil
	}
	return json.RawMessage(s)
}

func firstText(c []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	for _, b := range c {
		if strings.TrimSpace(b.Text) != "" {
			return b.Text
		}
	}
	return ""
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func timeFromMS(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// parseStartedAt 收两种写法：epoch 毫秒（真记录里的形状）与 RFC3339 串（文档里常见）。
func parseStartedAt(raw json.RawMessage) (time.Time, bool) {
	txt := strings.TrimSpace(string(raw))
	if txt == "" || txt == "null" {
		return time.Time{}, false
	}
	if n, err := strconv.ParseInt(txt, 10, 64); err == nil && n > 0 {
		return time.UnixMilli(n).UTC(), true
	}
	var iso string
	if err := json.Unmarshal(raw, &iso); err == nil {
		if t, err := time.Parse(time.RFC3339Nano, iso); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

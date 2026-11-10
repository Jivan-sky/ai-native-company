package main

// anc chat —— ANC 自己的回合：流式模型调用 + 工具调用。
//
// 这是「ANC 是 Agent」那块的地基：在这之前它是**一堆纯代码工具**，每个命令自己做自己退。
// 这一条把「模型说的话 → 调哪个工具 → 结果回灌 → 再说」闭环起来，并留下 transcript。
//
// 三条口径：
//   - **密钥走环境变量**（`--key-env`），不许出现在命令行参数里（会进 shell 历史）；
//   - **流式输出**：文本增量直接写到 stdout（管道里也能用），工具调用写到 stderr；
//   - **未完成要标出来**：被取消 / 到步数上限，退出码非 0，transcript 里标 incomplete。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/agent"
	"anc/internal/ctxtool"
)

const chatUsage = `anc chat —— ANC 自己的回合（流式 + 工具调用）

用法：
  anc chat --once "问题" --base-url <兼容端点> --model <模型> [选项]

选项：
  --once "<话>"      一条就退（没有这个参数时是交互模式，见下）
  --base-url <url>   兼容 OpenAI 的端点（例：https://dashscope.aliyuncs.com/compatible-mode/v1）
  --model <名>       模型名
  --key-env <NAME>   从哪个环境变量读密钥（默认 DASHSCOPE_API_KEY；不给就能调不需要鉴权的端点）
  --vault <目录>     真相源目录（给了才挂 anc_read_context 这个工具）
  --max-steps <n>    回合里最多几步（默认 8）
  --transcript <文件> 把每条消息按 JSONL 追加落盘（可回放）
  --timeout <时长>   整回合超时（默认 2m）

退出码：0 = 正常收口；1 = 出错 / 被取消 / 到步数上限（transcript 标 incomplete）；2 = 用法错。
`

func cmdChat(args []string) int {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	once := fs.String("once", "", "一条就退")
	baseURL := fs.String("base-url", "", "兼容 OpenAI 的端点")
	model := fs.String("model", "", "模型名")
	keyEnv := fs.String("key-env", "DASHSCOPE_API_KEY", "密钥所在的环境变量名")
	vault := fs.String("vault", "", "真相源目录（给了才挂 read_context）")
	maxSteps := fs.Int("max-steps", 8, "最多几步")
	transcript := fs.String("transcript", "", "消息按 JSONL 追加落盘")
	timeout := fs.Duration("timeout", 2*time.Minute, "整回合超时")
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) > 0 || strings.TrimSpace(*baseURL) == "" || strings.TrimSpace(*model) == "" {
		fmt.Fprint(os.Stderr, chatUsage)
		return 2
	}
	if strings.TrimSpace(*once) == "" {
		fmt.Fprintln(os.Stderr, "错误：这一版只支持 --once（交互模式与 SSE 出口是下一刀）")
		return 2
	}

	key := ""
	if *keyEnv != "" {
		key = os.Getenv(*keyEnv)
	}
	client := &agent.OpenAIClient{BaseURL: *baseURL, Model: *model, APIKey: key}

	var tools []agent.Tool
	if strings.TrimSpace(*vault) != "" {
		tools = append(tools, ctxtool.ReadContext(*vault))
	}

	loop := agent.New(client, tools)
	loop.MaxSteps = *maxSteps
	loop.Out = os.Stdout
	var rec *transcriptWriter
	if *transcript != "" {
		w, err := newTranscriptWriter(*transcript)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：transcript 打不开：%v\n", err)
			return 1
		}
		defer w.Close()
		rec = w
		loop.Record = w.Message
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if rec != nil {
		rec.Message(agent.Message{Role: "user", Content: *once})
	}
	res, err := loop.Run(ctx, []agent.Message{{Role: "user", Content: *once}})
	fmt.Println()
	if *transcript != "" {
		fmt.Fprintf(os.Stderr, "— 用量 in %d / out %d / 缓存读 %d · %d 步\n",
			res.Usage.In, res.Usage.Out, res.Usage.CacheRead, res.Steps)
	}
	if rec != nil {
		rec.CloseWith(res)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "未完成：%v\n", err)
		return 1
	}
	return 0
}

// transcriptWriter 把每条消息追加成一行 JSON：可回放、可对账。
type transcriptWriter struct {
	f   *os.File
	enc *json.Encoder
}

func newTranscriptWriter(path string) (*transcriptWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &transcriptWriter{f: f, enc: json.NewEncoder(f)}, nil
}

func (t *transcriptWriter) Message(m agent.Message) { _ = t.enc.Encode(m) }

func (t *transcriptWriter) CloseWith(res agent.Result) {
	_ = t.enc.Encode(map[string]any{
		"role": "_meta", "steps": res.Steps, "usage": res.Usage,
		"incomplete": res.Incomplete, "why": res.Why,
	})
	_ = t.f.Close()
}

func (t *transcriptWriter) Close() { _ = t.f.Close() }

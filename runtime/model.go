package main

// anc model ask —— 沙箱里的应用通过 **unix socket** 调 ANC 的模型出口。
//
// 为什么是 socket 而不是网络：应用跑在 `--network=none` 的容器里，唯一的通道是挂进去的那个
// socket 文件。egress 因此是**构造保证**的，不是靠一条「不许外联」的声明。
//
// 应用手里只有：socket 路径 + 一张短时票（ANC_DELEGATION）+ 自己的 prompt。
// 没有密钥、没有端点、没有模型名 —— 那些都在 ANC 这一侧。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const modelUsage = `anc model ask —— 通过 unix socket 让 ANC 代调模型（给沙箱里的应用用）

用法：
  anc model ask --socket /run/anc/broker.sock --prompt "…" [--json]
                [--token-env ANC_DELEGATION] [--model <注册名>]

口径：
  · 票从环境变量读（默认 ANC_DELEGATION），不进命令行参数（会进历史与进程表）；
  · 没有 socket 路径或票就报错退出，**不偷偷直连网络**。
`

func cmdModel(args []string) int {
	if len(args) == 0 || args[0] != "ask" {
		fmt.Fprint(os.Stderr, modelUsage)
		return 2
	}
	fs := flag.NewFlagSet("model ask", flag.ContinueOnError)
	socket := fs.String("socket", os.Getenv("ANC_SOCKET"), "ANC broker 的 unix socket（Linux 上可用）")
	urlFlag := fs.String("url", os.Getenv("ANC_BROKER_URL"), "ANC broker 的 HTTP 地址（Docker Desktop 上用这个）")
	prompt := fs.String("prompt", "", "要问的话")
	tokenEnv := fs.String("token-env", "ANC_DELEGATION", "票所在的环境变量名")
	modelID := fs.String("model", "", "注册名（不写就用票里的第一个）")
	asJSON := fs.Bool("json", false, "输出完整 JSON")
	flagArgs, posArgs := splitArgs(args[1:], map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) > 0 || (strings.TrimSpace(*socket) == "" && strings.TrimSpace(*urlFlag) == "") || strings.TrimSpace(*prompt) == "" {
		fmt.Fprint(os.Stderr, modelUsage)
		return 2
	}
	token := os.Getenv(*tokenEnv)
	if strings.TrimSpace(token) == "" {
		fmt.Fprintf(os.Stderr, "错误：环境变量 %s 里没有委派令牌\n", *tokenEnv)
		return 1
	}

	endpoint := "http://anc/api/chat"
	client := &http.Client{Timeout: 2 * time.Minute}
	if strings.TrimSpace(*socket) != "" {
		client.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", *socket)
		}}
	} else {
		endpoint = strings.TrimRight(*urlFlag, "/") + "/api/chat"
	}
	_ = endpoint
	body := map[string]any{"prompt": *prompt}
	if *modelID != "" {
		body["model_id"] = *modelID
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(string(b)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：连不上 broker（%s）：%v\n", *socket, err)
		return 1
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "错误：broker 拒绝（HTTP %d）：%s\n", resp.StatusCode, strings.TrimSpace(string(raw)))
		return 1
	}
	// 把 SSE 的 delta 拼成纯文本；--json 时给原始事件。
	text := joinSSE(raw)
	if *asJSON {
		out := map[string]any{"status": resp.StatusCode, "text": text, "raw": string(raw)}
		bb, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(bb))
		return 0
	}
	fmt.Println(text)
	return 0
}

func joinSSE(raw []byte) string {
	var sb strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil && ev.Text != "" {
			sb.WriteString(ev.Text)
		}
	}
	return sb.String()
}

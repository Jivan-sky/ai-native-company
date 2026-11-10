package agent

// SSE 解析 + OpenAI 兼容的流式客户端。
//
// 只做这一件事，是因为它最容易写错：`data:` 行、`[DONE]`、注释心跳、
// tool_calls 的分片累积、以及 usage 只在最后一个 chunk 里出现。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Delta 是一次流式增量：可能只有文本、只有工具分片、或只有用量。
type Delta struct {
	Text      string
	ToolIndex int
	ToolID    string
	ToolName  string
	ToolArgs  string // 分片
	Usage     *Usage
	Done      bool
}

// Client 是模型客户端的最小面：一次流式调用，边收边回调。
type Client interface {
	Stream(ctx context.Context, msgs []Message, tools []ToolSpec, onDelta func(Delta) error) error
}

// OpenAIClient 打一个兼容 OpenAI chat/completions 的端点。
type OpenAIClient struct {
	BaseURL string // 例：https://dashscope.aliyuncs.com/compatible-mode/v1
	Model   string
	APIKey  string
	HTTP    *http.Client
	// 便于测试：不设就用默认
	Timeout time.Duration
}

type chatRequest struct {
	Model    string     `json:"model"`
	Messages []Message  `json:"messages"`
	Tools    []ToolSpec `json:"tools,omitempty"`
	Stream   bool       `json:"stream"`
	// 流式下 usage 默认不返回，要显式要
	StreamOptions map[string]bool `json:"stream_options,omitempty"`
}

// ToolSpec 是给模型看的工具声明（function calling 的 schema）。
type ToolSpec struct {
	Type     string     `json:"type"` // "function"
	Function ToolSchema `json:"function"`
}

type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func (c *OpenAIClient) Stream(ctx context.Context, msgs []Message, tools []ToolSpec, onDelta func(Delta) error) error {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 0}
	}
	body, err := json.Marshal(chatRequest{
		Model: c.Model, Messages: msgs, Tools: tools, Stream: true,
		StreamOptions: map[string]bool{"include_usage": true},
	})
	if err != nil {
		return err
	}
	url := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var sb strings.Builder
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		sb.Write(buf[:n])
		return fmt.Errorf("模型端点 HTTP %d：%s", resp.StatusCode, strings.TrimSpace(sb.String()))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // 空行、注释心跳（": ping"）一律忽略，不算结束
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			return onDelta(Delta{Done: true})
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				PromptCacheHit   int `json:"prompt_cache_hit_tokens"`
				PromptCacheMiss  int `json:"prompt_cache_miss_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("流里出现读不懂的 chunk：%v（原文前 120 字：%.120s）", err, payload)
		}
		if chunk.Usage != nil {
			u := Usage{In: chunk.Usage.PromptTokens, Out: chunk.Usage.CompletionTokens,
				CacheRead: chunk.Usage.PromptCacheHit}
			if err := onDelta(Delta{Usage: &u}); err != nil {
				return err
			}
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				if err := onDelta(Delta{Text: ch.Delta.Content}); err != nil {
					return err
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				if err := onDelta(Delta{
					ToolIndex: tc.Index, ToolID: tc.ID, ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments,
				}); err != nil {
					return err
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("流读到一半断了：%v", err)
	}
	// 没有 [DONE] 就结束：交给上层判断是「正常收尾」还是「被截断」
	return nil
}

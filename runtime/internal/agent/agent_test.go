package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeModel 按「第几次请求」回放脚本化的 SSE。真模型不可控，脚本化的才验得动：
// 特别是 tool_calls 的分片（arguments 分三段到）。
func fakeModel(t *testing.T, scripts [][]string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		// 超过脚本条数就**重复最后一条**：上限类用例需要「模型一直要工具」。
		var script []string
		if len(scripts) > 0 {
			i := int(n) - 1
			if i >= len(scripts) {
				i = len(scripts) - 1
			}
			script = scripts[i]
		}
		for _, payload := range script {
			fmt.Fprintf(w, "data: %s\n\n", payload)
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func chunk(delta string) string { return `{"choices":[{"delta":` + delta + `}]}` }
func usageChunk() string {
	return `{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_cache_hit_tokens":40}}`
}

func TestLoopReassemblesSplitToolArgs(t *testing.T) {
	srv, calls := fakeModel(t, [][]string{
		{ // 第 1 次：先说一句，再分段给 tool_call
			chunk(`{"content":"我先看"} `[:0] + `{"content":"我先看"}`),
			chunk(`{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_ctx","arguments":"{\"who\":\"al"}}]}`),
			chunk(`{"tool_calls":[{"index":0,"function":{"arguments":"ice\"}"}}]}`),
			usageChunk(),
		},
		{ // 第 2 次：拿到工具结果后给最终回答
			chunk(`{"content":"答案是 42"}`),
			usageChunk(),
		},
	})
	var ran string
	tools := []Tool{{
		Spec: Spec("read_ctx", "读上下文", `{"type":"object","properties":{"who":{"type":"string"}},"required":["who"]}`),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			ran = string(args)
			return "上下文片段", nil
		},
	}}
	var out strings.Builder
	l := New(&OpenAIClient{BaseURL: srv.URL, Model: "m"}, tools)
	l.Out = &out
	res, err := l.Run(context.Background(), []Message{{Role: "user", Content: "看看 alice"}})
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("应当调两次模型（一次带工具调用、一次收口），实际 %d", *calls)
	}
	if ran != `{"who":"alice"}` {
		t.Fatalf("分片没拼对：%q（分三片到，必须按 index 累积）", ran)
	}
	if !strings.Contains(out.String(), "我先看") || !strings.Contains(out.String(), "答案是 42") {
		t.Fatalf("流式文本没出来：%q", out.String())
	}
	// 工具结果必须回灌
	found := false
	for _, m := range res.Messages {
		if m.Role == "tool" && m.Content == "上下文片段" && m.ToolCallID == "call_1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("工具结果没回灌给模型：%+v", res.Messages)
	}
	if res.Usage.In != 200 || res.Usage.Out != 40 || res.Usage.CacheRead != 80 {
		t.Fatalf("用量没累加：%+v", res.Usage)
	}
	if res.Incomplete {
		t.Fatal("正常收口不该标未完成")
	}
}

// 工具参数坏掉：不许崩，要变成一条 tool 消息回灌，让模型自己改。
func TestLoopFeedsBackBadArgs(t *testing.T) {
	srv, _ := fakeModel(t, [][]string{
		{chunk(`{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read_ctx","arguments":"{不是json"}}]}`)},
		{chunk(`{"content":"我换个写法"}`)},
	})
	var ran bool
	l := New(&OpenAIClient{BaseURL: srv.URL, Model: "m"}, []Tool{{
		Spec: Spec("read_ctx", "读上下文", `{}`),
		Run:  func(ctx context.Context, args json.RawMessage) (string, error) { ran = true; return "x", nil },
	}})
	res, err := l.Run(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("参数不合法时不该执行工具")
	}
	var toolMsg string
	for _, m := range res.Messages {
		if m.Role == "tool" {
			toolMsg = m.Content
		}
	}
	if !strings.Contains(toolMsg, "错误") {
		t.Fatalf("错误没有回灌：%+v", res.Messages)
	}
}

// 取消：不许假装完成。
func TestLoopCancelIsIncomplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"半句\"}}]}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(2 * time.Second) // 卡住，让 ctx 超时
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	l := New(&OpenAIClient{BaseURL: srv.URL, Model: "m"}, nil)
	res, err := l.Run(ctx, []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("被取消应当返回错误")
	}
	if !res.Incomplete {
		t.Fatal("取消必须标 Incomplete（不许当成正常结束）")
	}
}

// 步数上限：模型一直要工具，必须停下来并说原因。
func TestLoopStopsAtMaxSteps(t *testing.T) {
	srv, calls := fakeModel(t, [][]string{
		{chunk(`{"tool_calls":[{"index":0,"id":"c1","function":{"name":"t","arguments":"{}"}}]}`)},
	})
	l := New(&OpenAIClient{BaseURL: srv.URL, Model: "m"}, []Tool{{
		Spec: Spec("t", "t", `{}`),
		Run:  func(ctx context.Context, args json.RawMessage) (string, error) { return "ok", nil },
	}})
	l.MaxSteps = 3
	res, err := l.Run(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil || !res.Incomplete {
		t.Fatalf("到上限应当停并标 Incomplete：%+v %v", res, err)
	}
	if int(*calls) != 3 {
		t.Fatalf("应当正好跑 3 步，实际 %d", *calls)
	}
	if !strings.Contains(res.Why, "步数上限") {
		t.Fatalf("原因要写清：%q", res.Why)
	}
}

package board

// 两个 HTTP 面：`/api/chat`（SSE 回合）与 `/api/models`（注册表，**只有打码形态**）。
//
// 为什么放在看板服务里：看板已经是有地址、有鉴权边界、有只读纪律的那一个 HTTP 面。
// 再起一个端口，运维与防火墙就要多记一条；而 SEC 的验收脚本（dev/mask-scan.sh）
// 扫的也正是这个地址 —— 出口只有一个，才扫得干净。
//
// 两条硬约束：
//  1. **`/api/models` 从不读密钥值**：它只说「配了 / 没配」。尾巴四位都不给 ——
//     少一个把密钥形状带出去的通道。
//  2. **`/api/chat` 只在配置了端点时才存在**：没配 → 501 + 说清楚，不假装能用。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/agent"
	"anc/internal/ctxtool"
	"anc/internal/models"
)

// ChatConfig 是一次回合要用的端点与模型。KeyEnv 是**环境变量名**，不是密钥。
type ChatConfig struct {
	BaseURL string
	Model   string
	KeyEnv  string
}

func (c *ChatConfig) configured() bool {
	return c != nil && strings.TrimSpace(c.BaseURL) != "" && strings.TrimSpace(c.Model) != ""
}

type chatRequest struct {
	Prompt   string          `json:"prompt,omitempty"`
	Messages []agent.Message `json:"messages,omitempty"`
	MaxSteps int             `json:"max_steps,omitempty"`
	ModelID  string          `json:"model_id,omitempty"`
}

// handleChat 是 `POST /api/chat`：请求体进、SSE 事件出。
//
// 事件三种：
//
//	event: delta   data: {"text":"…"}
//	event: tool    data: {"name":"…","args":"…"}
//	event: done    data: {"usage":{…},"steps":2,"incomplete":false}
//
// 客户端断开 → ctx 取消 → 循环返回 Incomplete → 我们写一条 done(incomplete=true)。
// **不写「假装正常结束」的 done**：那会让下游把半截当成完整。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	// 先读请求体（要拿 model_id 去判定），**再**鉴权：判定需要知道「它想用哪个模型」。
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法 JSON：" + err.Error()})
		return
	}
	modelID := firstNonEmpty(req.ModelID, s.chatModelID())
	if s.Chat.configured() || s.Gate != nil {
		if _, _, ok := s.authorize(w, r, modelID); !ok {
			return
		}
	}
	if !s.Chat.configured() {
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"error": "没有配置模型端点：anc board … --chat-base-url <url> --chat-model <名> [--chat-key-env NAME]",
		})
		return
	}
	msgs := req.Messages
	if strings.TrimSpace(req.Prompt) != "" {
		msgs = append(msgs, agent.Message{Role: "user", Content: req.Prompt})
	}
	if len(msgs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "要么给 prompt，要么给 messages"})
		return
	}

	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "这个连接不支持流式"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	sw := &sseWriter{w: w, fl: fl}
	client := s.ChatClient
	if client == nil {
		key := ""
		if s.Chat.KeyEnv != "" {
			key = os.Getenv(s.Chat.KeyEnv)
		}
		client = &agent.OpenAIClient{BaseURL: s.Chat.BaseURL, Model: s.Chat.Model, APIKey: key}
	}
	tools := s.chatTools()
	loop := agent.New(client, tools)
	if req.MaxSteps > 0 {
		loop.MaxSteps = req.MaxSteps
	}
	loop.Out = sw
	loop.OnTool = func(tc agent.ToolCall) {
		sw.event("tool", map[string]any{"name": tc.Function.Name, "args": tc.Function.Arguments})
	}

	res, err := loop.Run(r.Context(), msgs)
	done := map[string]any{
		"usage": res.Usage, "steps": res.Steps,
		"incomplete": res.Incomplete || err != nil,
	}
	if err != nil {
		done["why"] = err.Error()
	}
	sw.event("done", done)
}

// chatTools 是这个服务能挂的工具。现在只有一个：读上下文（与 MCP 读出口同一装配点）。
func (s *Server) chatTools() []agent.Tool {
	if strings.TrimSpace(s.Vault) == "" {
		return nil
	}
	return []agent.Tool{ctxtool.ReadContext(s.Vault)}
}

// sseWriter 把文本增量封成 SSE 事件。一次 Write 一帧 —— 够用，也简单。
type sseWriter struct {
	w  http.ResponseWriter
	fl http.Flusher
}

func (s *sseWriter) Write(p []byte) (int, error) {
	s.event("delta", map[string]any{"text": string(p)})
	return len(p), nil
}

func (s *sseWriter) event(name string, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, b)
	s.fl.Flush()
}

// handleModels 是 `GET /api/models`：注册表 + 绑定，**只有「配没配」，没有密钥形状**。
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	parent := filepath.Dir(s.Vault)
	reg, err := models.LoadRegistry(filepath.Join(parent, "registry.json"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"schema": "anc.models/v1", "wired": false, "why": err.Error()})
		return
	}
	type row struct {
		ID         string   `json:"id"`
		Provider   string   `json:"provider"`
		Model      string   `json:"model"`
		Secret     string   `json:"secret"`
		Configured bool     `json:"configured"`
		Status     string   `json:"status"`
		Apps       []string `json:"apps"`
	}
	var rows []row
	bindings, _ := loadBindingFiles(filepath.Join(parent, "apps"), reg)
	values := secretKeys(filepath.Join(parent, "secrets.env"))
	for _, e := range reg.Entries {
		status := "enabled"
		if !e.Enabled() {
			status = "disabled"
		}
		var apps []string
		for _, b := range bindings {
			for _, id := range b.Models {
				if id == e.ID {
					apps = append(apps, b.App)
				}
			}
		}
		rows = append(rows, row{e.ID, e.Provider, e.Model, e.Secret, values[e.Secret], status, apps})
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": "anc.models/v1", "wired": true, "entries": rows})
}

// secretKeys 只回答「这个键配了没有」——它**不返回值**，连长度都不给。
func secretKeys(path string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		k := strings.TrimSpace(line[:strings.IndexByte(line, '=')])
		v := strings.TrimSpace(line[strings.IndexByte(line, '=')+1:])
		out[k] = v != ""
	}
	return out
}

func loadBindingFiles(dir string, reg *models.Registry) ([]*models.Binding, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*models.Binding
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := models.LoadBinding(filepath.Join(dir, e.Name()), reg)
		if err != nil {
			continue // 坏掉的绑定不挡注册表：它会在 anc models check 里被点名
		}
		out = append(out, b)
	}
	return out, nil
}

// 让 readOnly 放行这两个口。
func isChatPath(p string) bool { return p == chatPath || p == modelsPath }

const (
	chatPath        = "/api/chat"
	chatSessionPath = "/api/chat/session"
	modelsPath      = "/api/models"
)

var _ = context.Background
var _ = time.Now

// chatModelID 是这一台机器默认使用的注册名。
func (s *Server) chatModelID() string {
	if s.Gate != nil && s.Gate.ModelID != "" {
		return s.Gate.ModelID
	}
	return ""
}

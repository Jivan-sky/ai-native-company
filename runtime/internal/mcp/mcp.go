// Package mcp —— 极小的 MCP 服务端（streamable HTTP）。
//
// 一句话：让 harness（Claude Code / Codex / …）能把一个信封**递进来**，而不必为每个
// harness 写一套私有协议。只做四件事：initialize / notifications/* / tools/list / tools/call。
//
// 刻意**无状态**：不发 `Mcp-Session-Id`、不记会话 —— 状态在真相源与日志里，不在连接里。
// 这正是 #45 那条待验主张（「新的 MCP 协议是无状态的，可能更适合」）在服务端的第一次践行：
// 一个信封自带全部路由所需信息，所以**收到就能办**，不必先握手养出一段会话；反过来，
// 连接断了也不用「重新连一次」—— 下一封信自己会到。
//
// 只绑本机（见 CLI 默认）。这不是「开了一个入站端口」—— 它绑 127.0.0.1，
// 卖点「免公网 IP、免备案」（SPEC §4.1）不受影响。
package mcp

import (
	"encoding/json"
	"io"
	"net/http"
)

// ProtocolVersion 是本服务端默认回话的 MCP 版本。客户端报来的版本我们**原样回**（见 initialize），
// 因为这一层只用最小子集（tools 三个方法），各版本间没有差异；回错版本号反而会让客户端白白拒绝。
const ProtocolVersion = "2025-06-18"

// Tool 是一个可被 harness 调用的工具。
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	// Call 执行一次调用。text 是回给 harness 的文字；isErr=true 表示这次调用失败
	// （按 MCP 口径这仍是一个正常 result，只是 isError=true —— 不把它升级成协议错误，
	// 否则 harness 侧看到的是「工具不存在」而不是「这次没办成」）。
	Call func(args map[string]any) (text string, isErr bool)
}

// Server 是一个 MCP 服务端。Tools 是它对外暴露的全部能力。
type Server struct {
	Name    string
	Version string
	Tools   []Tool
	// Log 是观测出口（可为 nil）。**每次调用打一行** —— 这是「一个入口 = 一份日志」里的
	// 「看得见」那一半；落盘那一半在调用方（envelope 的 Append）。
	Log func(format string, a ...any)
}

func (s *Server) logf(format string, a ...any) {
	if s.Log != nil {
		s.Log(format, a...)
	}
}

// Handler 组装路由。只有一个入口：POST /mcp。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handle)
	return mux
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	// 无状态 = 不提供 GET 的 SSE 流，也不认 DELETE 的会话终止：这两个都建立在「有会话」上。
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "这个 MCP 入口只收 POST（无状态：不提供 SSE 流、也不认会话终止）", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "读请求体失败", http.StatusBadRequest)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &rpcError{Code: -32700, Message: "不是合法的 JSON-RPC"}})
		return
	}

	// 通知（没有 id）：按 MCP 口径回 202、不回 body。`notifications/initialized` 走这条路。
	if isEmptyID(req.ID) {
		s.logf("mcp: 通知 %s", req.Method)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		s.logf("mcp: initialize（客户端协议版本 %s）", clientProtocolVersion(req.Params))
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": echoProtocolVersion(req.Params),
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		}})
	case "ping":
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		s.logf("mcp: tools/list → %d 个工具", len(s.Tools))
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": s.toolList()}})
	case "tools/call":
		s.handleCall(w, req)
	default:
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32601, Message: "不支持的方法：" + req.Method}})
	}
}

func (s *Server) handleCall(w http.ResponseWriter, req rpcRequest) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32602, Message: "params 不是 {name, arguments}"}})
		return
	}
	for _, t := range s.Tools {
		if t.Name != p.Name {
			continue
		}
		text, isErr := t.Call(p.Arguments)
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		s.logf("mcp: tools/call %s（%d 个参数）→ isError=%v", p.Name, len(p.Arguments), isErr)
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}})
		return
	}
	// 工具名不认识 = 协议层错误（-32602）：harness 该换一个名字，而不是「这次没办成」。
	writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
		Error: &rpcError{Code: -32602, Message: "没有这个工具：" + p.Name}})
}

func (s *Server) toolList() []map[string]any {
	out := make([]map[string]any, 0, len(s.Tools))
	for _, t := range s.Tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schema,
		})
	}
	return out
}

func isEmptyID(id json.RawMessage) bool {
	return len(id) == 0 || string(id) == "null"
}

func echoProtocolVersion(params json.RawMessage) string {
	if v := clientProtocolVersion(params); v != "" {
		return v
	}
	return ProtocolVersion
}

func clientProtocolVersion(params json.RawMessage) string {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	return p.ProtocolVersion
}

// writeRPC 出 JSON 而不是 SSE：streamable HTTP 允许服务端对单个请求直接回一个 JSON 对象，
// 只有在需要服务端主动推送时才必须开 SSE。这一层没有主动推送，所以不开。
func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

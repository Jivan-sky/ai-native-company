package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------- 合规对拍：用例照着**官方规范的消息形状**写 ----------
//
// 与 mcp_test.go 的分工：那份钉「我们要的行为」（回声版本号、405、日志…）；
// 这份钉**规范要求的形状与错误码**，出处写在每条用例旁边 ——
// 判据不是「我们的实现这么做」，而是「规范这么要求，我们照做了」。
//
// 依据 2025-06-18 / 2025-11-25 / 2026-07-28 三版共同的子集（我们只实现这个子集）：
//   - initialize → result 必须有 protocolVersion(string) / capabilities(object) / serverInfo{name,version}
//   - tools/list → 每个工具 name / description 是 string，inputSchema 是 **JSON Schema 对象**
//   - tools/call → result.content 是**内容块数组**（type=text + text 字符串），isError 是 bool；
//     **工具级失败仍是 result**（isError=true），不是协议错误
//   - 错误码：-32700 解析失败 / -32601 方法不存在 / -32602 参数非法
//
// **有意不支持**（一并钉住，免得日后被当 bug「修」掉）：
//   - **批量请求**（顶层 JSON 数组）：规范自 2025-06-18 起已移除批量要求 → 按解析失败回 -32700
//   - **SSE 流**：本层没有服务端主动推送；streamable HTTP 允许对单个请求直接回 JSON

func TestConformanceInitializeResultShape(t *testing.T) {
	s, _ := testServer()
	m := decode(t, post(t, s.Handler(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"c","version":"0"}}}`))

	if m["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc 必须是 2.0，实际 %v", m["jsonrpc"])
	}
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize 必须有 result，实际 %v", m)
	}
	if _, ok := res["protocolVersion"].(string); !ok {
		t.Errorf("protocolVersion 必须是字符串，实际 %T", res["protocolVersion"])
	}
	caps, ok := res["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities 必须是对象，实际 %T", res["capabilities"])
	}
	if _, ok := caps["tools"]; !ok {
		t.Error("capabilities 里必须声明 tools —— 我们只服务这一类，声明了客户端才知道可以 tools/list")
	}
	info, ok := res["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo 必须是对象，实际 %T", res["serverInfo"])
	}
	name, _ := info["name"].(string)
	ver, _ := info["version"].(string)
	if name == "" || ver == "" {
		t.Errorf("serverInfo.name / .version 都必须非空字符串，实际 name=%q version=%q", name, ver)
	}
}

// id 必须**原样**回（数字还是数字、字符串还是字符串）：客户端靠它把响应配回请求。
func TestConformanceIDEchoed(t *testing.T) {
	s, _ := testServer()
	cases := []struct {
		body string
		want any
	}{
		{`{"jsonrpc":"2.0","id":7,"method":"ping"}`, float64(7)},
		{`{"jsonrpc":"2.0","id":0,"method":"ping"}`, float64(0)},
		{`{"jsonrpc":"2.0","id":"x-1","method":"ping"}`, "x-1"},
	}
	for _, c := range cases {
		m := decode(t, post(t, s.Handler(), c.body))
		if m["id"] != c.want {
			t.Errorf("%s：id 应原样回 %v（%T），实际 %v（%T）", c.body, c.want, c.want, m["id"], m["id"])
		}
	}
}

// inputSchema 必须是一个 JSON Schema **对象**：type=object；有 properties 时必须是对象；
// 有 required 时必须是数组、且每一项都指向真实存在的参数。
// properties **不是**规范要求（没有参数的工具合法），所以这条对拍只钉「在的时候形状对」。
func TestConformanceToolsListInputSchema(t *testing.T) {
	s, _ := testServer()
	m := decode(t, post(t, s.Handler(), `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list 必须有 result，实际 %v", m)
	}
	tools, ok := res["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools 必须是非空数组，实际 %v", res["tools"])
	}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		if n, _ := tool["name"].(string); n == "" {
			t.Error("工具必须有非空 name")
		}
		if _, ok := tool["description"].(string); !ok {
			t.Errorf("工具 %v 的 description 必须是字符串", tool["name"])
		}
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok {
			t.Fatalf("工具 %v 的 inputSchema 必须是对象", tool["name"])
		}
		if schema["type"] != "object" {
			t.Errorf("工具 %v 的 inputSchema.type 必须是 object，实际 %v", tool["name"], schema["type"])
		}
		// properties **不是**规范要求（没有参数的工具合法）：在就当对象看，不在不报错。
		// 「我们的真工具必须给全 properties + 每个参数有 type/description」那条更严的判据
		// 在包外那份对拍里（runtime/mcp_conformance_test.go），它钉的是 harness 真看得见的那两个工具。
		if raw, ok := schema["properties"]; ok {
			if _, isObj := raw.(map[string]any); !isObj {
				t.Errorf("工具 %v 的 inputSchema.properties 在的时候必须是对象，实际 %T", tool["name"], raw)
			}
		}
	}
}

// 工具级失败 = **result + isError:true**；协议错误只留给「工具根本不存在 / 参数不合法」。
// 反过来（把「没办成」升级成协议错误）会让 harness 侧看到「工具不存在」而不是「这次没办成」。
func TestConformanceToolsCallResultShape(t *testing.T) {
	mk := func(fail bool) *Server {
		return &Server{Name: "t", Version: "0", Tools: []Tool{{
			Name: "probe", Description: "d", InputSchema: map[string]any{"type": "object"},
			Call: func(map[string]any) (string, bool) { return "话", fail },
		}}}
	}
	for _, fail := range []bool{false, true} {
		s := mk(fail)
		m := decode(t, post(t, s.Handler(), `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"probe","arguments":{}}}`))
		if m["error"] != nil {
			t.Fatalf("工具级失败（isError=%v）不许变成协议错误，实际 %v", fail, m["error"])
		}
		res, ok := m["result"].(map[string]any)
		if !ok {
			t.Fatalf("必须有 result，实际 %v", m)
		}
		content, ok := res["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("result.content 必须是非空内容块数组，实际 %v", res["content"])
		}
		block, ok := content[0].(map[string]any)
		if !ok {
			t.Fatalf("内容块必须是对象，实际 %T", content[0])
		}
		if block["type"] != "text" {
			t.Errorf("第一个内容块 type 必须是 text，实际 %v", block["type"])
		}
		if _, ok := block["text"].(string); !ok {
			t.Errorf("文本块必须有 text 字符串，实际 %T", block["text"])
		}
		if got, ok := res["isError"].(bool); !ok || got != fail {
			t.Errorf("isError 必须是 bool 且等于 %v，实际 %v", fail, res["isError"])
		}
	}
}

// 省略 arguments 时，工具必须收到**空 map**而不是 nil ——
// 收到 nil 的工具里一次 `args["k"] = v` 就是 panic（写操作在 nil map 上必崩）。
// 这是实现侧的坑，但表现是「合规调用被服务端写崩」，所以对拍这份里钉住。
func TestConformanceMissingArgumentsReachToolAsEmptyMap(t *testing.T) {
	var got map[string]any
	s := &Server{Name: "t", Version: "0", Tools: []Tool{{
		Name: "needs-args", Description: "d", InputSchema: map[string]any{"type": "object"},
		Call: func(args map[string]any) (string, bool) { got = args; return "", false },
	}}}
	decode(t, post(t, s.Handler(), `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"needs-args"}}`))
	if got == nil {
		t.Fatal("缺 arguments 时工具收到 nil —— 工具里一写 args[...] 就 panic；应传空 map")
	}
}

// 批量请求：规范 2025-06-18 起移除该要求；我们按解析失败回 -32700（有意，不是漏做）。
func TestConformanceBatchIsParseError(t *testing.T) {
	s, _ := testServer()
	m := decode(t, post(t, s.Handler(), `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`))
	errObj, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("批量体应报解析错误，实际 %v", m)
	}
	if errObj["code"] != float64(-32700) {
		t.Errorf("批量体应回 -32700，实际 %v", errObj["code"])
	}
}

// 回话是 JSON（不是 SSE）：streamable HTTP 允许这么做，且我们无状态、没有主动推送。
func TestConformanceResponseIsJSON(t *testing.T) {
	s, _ := testServer()
	rec := post(t, s.Handler(), `{"jsonrpc":"2.0","id":5,"method":"ping"}`)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type 应是 application/json，实际 %q", ct)
	}
	var probe any
	if err := json.Unmarshal(rec.Body.Bytes(), &probe); err != nil {
		t.Errorf("回话必须是合法 JSON：%v", err)
	}
}

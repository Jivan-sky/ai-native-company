package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer() (*Server, *[]string) {
	var calls []string
	s := &Server{
		Name: "t", Version: "0",
		Tools: []Tool{{
			Name:        "echo",
			Description: "回声",
			InputSchema: map[string]any{"type": "object"},
			Call: func(args map[string]any) (string, bool) {
				calls = append(calls, args["x"].(string))
				return "收到 " + args["x"].(string), false
			},
		}},
	}
	return s, &calls
}

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("响应不是 JSON：%v\n%s", err, rec.Body.String())
	}
	return m
}

// 版本号**回客户端报的那个**，不写死自己的：客户端会拿「服务端回的版本」跟自己支持的对，
// 回一个它不认识的版本号，它就走了。
func TestInitializeEchoesClientVersion(t *testing.T) {
	s, _ := testServer()
	for _, v := range []string{"2025-06-18", "2025-11-25"} {
		rec := post(t, s.Handler(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+v+`"}}`)
		res := decode(t, rec)["result"].(map[string]any)
		if got := res["protocolVersion"]; got != v {
			t.Errorf("协议版本应当回 %q，实际 %v", v, got)
		}
		if res["serverInfo"].(map[string]any)["name"] != "t" {
			t.Errorf("serverInfo 不对：%v", res["serverInfo"])
		}
	}
	// 客户端没报版本：回我们的默认，而不是空。
	rec := post(t, s.Handler(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if got := decode(t, rec)["result"].(map[string]any)["protocolVersion"]; got != ProtocolVersion {
		t.Errorf("没报版本时应回默认 %q，实际 %v", ProtocolVersion, got)
	}
}

// 通知（没有 id）回 202 且**没有 body** —— 回 body 会被客户端当成一次应答。
func TestNotificationGets202NoBody(t *testing.T) {
	s, _ := testServer()
	rec := post(t, s.Handler(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("期望 202，实际 %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("通知不该有 body，实际 %q", rec.Body.String())
	}
}

func TestToolsList(t *testing.T) {
	s, _ := testServer()
	tools := decode(t, post(t, s.Handler(), `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("期望 1 个工具，实际 %d", len(tools))
	}
	first := tools[0].(map[string]any)
	if first["name"] != "echo" || first["inputSchema"] == nil {
		t.Fatalf("工具形状不对：%v", first)
	}
}

func TestToolsCall(t *testing.T) {
	s, calls := testServer()
	res := decode(t, post(t, s.Handler(),
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"x":"喂"}}}`))["result"].(map[string]any)
	content := res["content"].([]any)[0].(map[string]any)
	if content["text"] != "收到 喂" {
		t.Errorf("回话不对：%v", content["text"])
	}
	if res["isError"] != false {
		t.Errorf("成功调用不该标 isError：%v", res["isError"])
	}
	if len(*calls) != 1 {
		t.Fatalf("工具应当被调一次，实际 %d", len(*calls))
	}
}

// 工具名不认识 = 协议层错误（客户端该换名字），**不是**「这次没办成」。
func TestUnknownToolIsProtocolError(t *testing.T) {
	s, _ := testServer()
	m := decode(t, post(t, s.Handler(), `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope"}}`))
	if m["error"] == nil {
		t.Fatalf("未知工具应当是协议错误，实际 %v", m)
	}
}

func TestProtocolErrors(t *testing.T) {
	s, _ := testServer()
	cases := []struct {
		name, body string
		code       float64
	}{
		{"坏 JSON", `{`, -32700},
		{"未知方法", `{"jsonrpc":"2.0","id":9,"method":"resources/list"}`, -32601},
		{"params 不是对象", `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":[]}`, -32602},
	}
	for _, c := range cases {
		m := decode(t, post(t, s.Handler(), c.body))
		errObj, ok := m["error"].(map[string]any)
		if !ok {
			t.Errorf("%s：应当报错，实际 %v", c.name, m)
			continue
		}
		if errObj["code"] != c.code {
			t.Errorf("%s：错误码应 %v，实际 %v", c.name, c.code, errObj["code"])
		}
	}
}

// 无状态：不提供 GET 的 SSE 流，也不认 DELETE 的会话终止 —— 两个都建立在「有会话」上。
func TestNonPostIs405(t *testing.T) {
	s, _ := testServer()
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(method, "/mcp", nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s 应当 405，实际 %d", method, rec.Code)
		}
		if rec.Header().Get("Allow") != "POST" {
			t.Errorf("%s 的 Allow 头应当是 POST，实际 %q", method, rec.Header().Get("Allow"))
		}
	}
}

func TestPing(t *testing.T) {
	s, _ := testServer()
	if decode(t, post(t, s.Handler(), `{"jsonrpc":"2.0","id":4,"method":"ping"}`))["result"] == nil {
		t.Fatal("ping 应当有 result")
	}
}

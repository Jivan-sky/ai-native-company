package board

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 注册 → 登录 → 我是谁 → 退出，一条链走通。
func TestAccountsRegisterLoginLogout(t *testing.T) {
	s := &Server{Vault: t.TempDir(), Accounts: true}
	h := s.Handler()

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("/api/auth/register", `{"name":"alice","password":"hunter2hunter2"}`); rec.Code != 200 {
		t.Fatalf("注册该成功，拿到 %d：%s", rec.Code, rec.Body)
	}
	// 同名再注册：拒绝
	if rec := post("/api/auth/register", `{"name":"alice","password":"hunter2hunter2"}`); rec.Code != 400 {
		t.Fatalf("重名该被拒，拿到 %d", rec.Code)
	}
	// 密码错：401
	if rec := post("/api/auth/login", `{"name":"alice","password":"wrongwrong"}`); rec.Code != 401 {
		t.Fatalf("密码错该 401，拿到 %d", rec.Code)
	}
	// 正确登录：拿到 cookie
	rec := post("/api/auth/login", `{"name":"alice","password":"hunter2hunter2"}`)
	if rec.Code != 200 {
		t.Fatalf("登录该成功，拿到 %d：%s", rec.Code, rec.Body)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("登录没种会话 cookie")
	}
	// 带 cookie 问「我是谁」
	req := httptest.NewRequest("GET", "/api/auth/me", nil)
	req.AddCookie(cookie)
	me := httptest.NewRecorder()
	h.ServeHTTP(me, req)
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"alice"`) {
		t.Fatalf("该认出是 alice，拿到 %d：%s", me.Code, me.Body)
	}
	// 退出后再问：401
	req2 := httptest.NewRequest("POST", "/api/auth/logout", nil)
	req2.AddCookie(cookie)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req2)
	req3 := httptest.NewRequest("GET", "/api/auth/me", nil)
	req3.AddCookie(cookie)
	me2 := httptest.NewRecorder()
	h.ServeHTTP(me2, req3)
	if me2.Code != 401 {
		t.Fatalf("退出后该 401，拿到 %d", me2.Code)
	}
}

// 没开账号时：登录/注册一律 501，/me 回 enabled=false，看板其余行为不变。
func TestAccountsDisabledIsHonest(t *testing.T) {
	s := &Server{Vault: t.TempDir()}
	h := s.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{}`)))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("没开账号该 501，拿到 %d", rec.Code)
	}
	me := httptest.NewRecorder()
	h.ServeHTTP(me, httptest.NewRequest("GET", "/api/auth/me", nil))
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"enabled": false`) {
		t.Fatalf("没开账号时 /me 该说 enabled=false，拿到 %d：%s", me.Code, me.Body)
	}
}

// 注册者不能自选角色：一律 member。升权是组织的事，不是注册表单的事。
func TestRegisterForcesMemberRole(t *testing.T) {
	s := &Server{Vault: t.TempDir(), Accounts: true}
	h := s.Handler()
	req := httptest.NewRequest("POST", "/api/auth/register",
		strings.NewReader(`{"name":"bob","password":"hunter2hunter2","role":"super"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"role": "member"`) {
		t.Fatalf("该被强制成 member：%d %s", rec.Code, rec.Body)
	}
}

// 飞书未配置：如实 501 + 给怎么做，不是 500。
func TestFeishuStartUnconfigured(t *testing.T) {
	s := &Server{Vault: t.TempDir()}
	h := s.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/auth/feishu/start", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("未配置该 501，拿到 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "未配置") {
		t.Fatalf("要说清未配置：%s", rec.Body)
	}
}

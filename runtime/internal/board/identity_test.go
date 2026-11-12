package board

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"anc/internal/org"
)

// 路由 → 能力：分类太粗会把不该放的放出去（KindRead 里既有首页投影也有审计）。
func TestRequirement(t *testing.T) {
	cases := []struct {
		path, module, action string
		need                 bool
	}{
		{"/api/audit", "audit", "read", true},
		{"/api/grants", "members", "read", true},
		{WritePath, "approvals", "approve", true},
		{SettlePath, "jobs", "execute", true},
		{"/api/query", "data", "read", true},
		{"/api/board", "board", "read", true},
		{chatPath, "", "", false}, // 模型出口已有票，不叠第二道锁
	}
	for _, c := range cases {
		kind, _ := routeKind(c.path)
		m, a, need := requirement(c.path, kind)
		if need != c.need || m != c.module || a != c.action {
			t.Errorf("%s → (%q,%q,%v)，要 (%q,%q,%v)", c.path, m, a, need, c.module, c.action, c.need)
		}
	}
}

// 关着的时候完全透明：开发机上没有网关，也没人往 header 里放身份。
func TestCapabilityGateOffIsTransparent(t *testing.T) {
	s := &Server{}
	h := s.capabilityGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("关着的时候不该拦，得到 %d", rec.Code)
	}
}

// 开着的时候：没身份 401，认不出 403。两种拒绝都要讲人话。
func TestCapabilityGateOn(t *testing.T) {
	s := &Server{EnforceAuthz: true, Caps: org.CapTable{"manager": {{Module: "audit", Action: "read"}}}, Vault: t.TempDir()}

	rec := httptest.NewRecorder()
	gate(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("没身份要 401，得到 %d", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("401 也要有话说")
	}

	// 头里有人，但真相源读不动 → 403（不是 401：身份是给了的）
	req := httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	req.Header.Set(DefaultIdentityHeader, "bob")
	rec = httptest.NewRecorder()
	gate(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("认不出角色要 403，得到 %d", rec.Code)
	}
}

// 静态资源不属于权威面：不能被能力判定顺手拦掉。
func TestCapabilityGateSkipsStatic(t *testing.T) {
	s := &Server{EnforceAuthz: true}
	rec := httptest.NewRecorder()
	gate(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("静态资源要放行，得到 %d", rec.Code)
	}
}

func gate(s *Server) http.Handler {
	return s.capabilityGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

package board

import (
	"net/http"
	"strings"

	"anc/internal/org"
)

// Identity 是「这次请求是谁」。
//
// v1 从**内网可信 header** 取（网关 / 反代注入）。飞书 SSO 接上之后，由 SSO 回填同一个 header ——
// 判据不动，只换身份来源。这就是「先做不依赖飞书的那一半」的意思。
//
// 取不到 header = 匿名。匿名不等于某个角色：它没有任何能力。
type Identity struct {
	Name string
	Role string
	// Why 是「为什么解不出角色」：人不在名册里 / 名册读不动。给拒绝理由用，别让人猜。
	Why string
}

// DefaultIdentityHeader 是默认的身份头。名字里带 ANC，避免和客户网关照转的其它头撞车。
const DefaultIdentityHeader = "X-ANC-User"

// resolveIdentity 把请求翻成一个身份。第二个返回值表示「有没有人」（不是「有没有权限」）。
func (s *Server) resolveIdentity(r *http.Request) (Identity, bool) {
	h := strings.TrimSpace(s.IdentityHeader)
	if h == "" {
		h = DefaultIdentityHeader
	}
	name := strings.TrimSpace(r.Header.Get(h))
	if name == "" {
		return Identity{}, false
	}
	id := Identity{Name: name}
	o, err := org.Load(s.Vault)
	if err != nil {
		id.Why = "真相源读不动，认不出你是谁"
		return id, true
	}
	m, ok := o.Member(name)
	if !ok {
		id.Why = "名册里没有「" + name + "」"
		return id, true
	}
	if m.Disabled {
		id.Why = "这个成员已停用"
		return id, true
	}
	id.Role = m.Role
	return id, true
}

// requirement 把一条路由翻成「要哪块业务的哪个动作」。
//
// 按路径判而不是按 Kind 判：KindRead 里既有首页投影（人人可看），也有审计（只有管事的能看）。
// 分类太粗，拿它当权限用就是把不该放的放出去。
func requirement(path string, kind RouteKind) (module, action string, need bool) {
	switch path {
	case "/api/audit":
		return "audit", "read", true
	case "/api/grants":
		return "members", "read", true
	case WritePath:
		return "approvals", "approve", true
	case SettlePath:
		return "jobs", "execute", true
	case "/api/query", "/api/files/ls", "/api/files/cat":
		return "data", "read", true
	}
	// 模型出口已经有票（authz.Decide）；这里不叠第二道锁，免得两处口径打架。
	if kind == KindModel {
		return "", "", false
	}
	if kind == KindRead {
		return "board", "read", true
	}
	return "", "", false
}

// capabilityGate 是「能力制」的落点：请求进来先问「他有这段逻辑的使用权吗」。
//
// 默认**不开**（EnforceAuthz=false）：开发机上没有网关、没人往 header 里放身份，
// 开了等于把看板锁死。生产打开它，才会有真正的隔离。
func (s *Server) capabilityGate(next http.Handler) http.Handler {
	if !s.EnforceAuthz {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, known := routeKind(r.URL.Path)
		if !known { // 静态资源：不属于权威面
			next.ServeHTTP(w, r)
			return
		}
		module, action, need := requirement(r.URL.Path, kind)
		if !need {
			next.ServeHTTP(w, r)
			return
		}
		id, ok := s.resolveIdentity(r)
		if !ok {
			denyJSON(w, http.StatusUnauthorized,
				"这一面要身份：请求头里没有 "+identityHeaderName(s))
			return
		}
		if id.Why != "" || id.Role == "" {
			why := id.Why
			if why == "" {
				why = "解不出你的角色"
			}
			denyJSON(w, http.StatusForbidden, "认不出你能做什么："+why)
			return
		}
		if s.Caps == nil || !s.Caps.Allow(id.Role, module, action) {
			denyJSON(w, http.StatusForbidden,
				"「"+id.Name+"」（岗位 "+id.Role+"）没有 "+module+"."+action+" 的权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func identityHeaderName(s *Server) string {
	if h := strings.TrimSpace(s.IdentityHeader); h != "" {
		return h
	}
	return DefaultIdentityHeader
}

// denyJSON 说清三件事：是什么、为什么、下一步。403 也要讲人话 —— 不然排障只能靠猜。
func denyJSON(w http.ResponseWriter, code int, why string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":` + quoteJSON(why) + `,"hint":"能力表在 <vault>/capabilities.md；找不到就往超管要"}`))
}

// quoteJSON 只处理我们自己的短字符串（引号、反斜杠、控制字符），不为了写一个错误体引一整个包。
func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			if r < 0x20 {
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

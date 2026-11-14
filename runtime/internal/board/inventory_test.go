package board

import "testing"

// 清单与路由表必须一致：routeKindSet 是给 readOnly 用的副本，漂了就是两套真相。
func TestInventoryMatchesRoutes(t *testing.T) {
	s := &Server{}
	routes := s.Routes()
	seen := map[string]RouteKind{}
	for _, rt := range routes {
		if _, dup := seen[rt.Path]; dup {
			t.Fatalf("路由重复登记：%s", rt.Path)
		}
		if rt.Path == "" || rt.Path[0] != '/' {
			t.Fatalf("路由必须以 / 开头：%q", rt.Path)
		}
		seen[rt.Path] = rt.Kind
	}
	if len(seen) != len(routeKindSet) {
		t.Fatalf("两张表条目数不一致：Routes=%d routeKindSet=%d", len(seen), len(routeKindSet))
	}
	for p, k := range seen {
		got, ok := routeKindSet[p]
		if !ok {
			t.Fatalf("%s 在 Routes 里，但不在 routeKindSet 里", p)
		}
		if got != k {
			t.Fatalf("%s 分类不一致：Routes=%v routeKindSet=%v", p, k, got)
		}
	}
}

// 模型面只能有一处 —— 多一处就是一个不经过判定的入口。
func TestExactlyOneModelRoute(t *testing.T) {
	var models []string
	for p, k := range routeKindSet {
		if k == KindModel {
			models = append(models, p)
		}
	}
	if len(models) != 2 {
		t.Fatalf("模型面应当恰好两条（%s / %s），实际 %v", chatPath, chatSessionPath, models)
	}
	for _, m := range models {
		if m != chatPath && m != chatSessionPath {
			t.Fatalf("多了一个模型入口：%s —— 每多一个就是一个不经过判定的面", m)
		}
	}
	queries := 0
	for _, k := range routeKindSet {
		if k == KindQuery {
			queries++
		}
	}
	if queries != 3 {
		t.Fatalf("查询面应当 3 条（/api/query 与 files 的 ls/cat），实际 %d 条", queries)
	}
}

// 写口是一张**登记表**，不是「两个」：每多一条都要在这里显式登记一遍。
//
// 接入的三条（2026-11-13 加）：预演只算不写、生效与停用会动 jobs/ 下的文件。
// 它们与审批/归档同策略（只认 POST、原子落盘、共用领域入口），所以并入同一档；
// 但**写口变宽了**，这条测试就是要人每次都在这里签个字。
func TestWriteRoutesAreTheKnownOnes(t *testing.T) {
	want := map[string]bool{
		WritePath: true, SettlePath: true,
		"/api/intake/preview": true, "/api/intake/apply": true, "/api/intake/stop": true,
		// 授权表的两条（2026-11-14 加）：签发与撤回。
		// 它们是**控制面**的写口 —— 比接入那三条更敏感：动的不是"跑不跑"，是"谁能碰什么"。
		"/api/authz/grant": true, "/api/authz/revoke": true,
		// 账号三条（2026-11-15 加）：登录 / 注册 / 退出。
		// 它们写的是 accounts.md（凭据表），不碰真相源；但没有它们就没有"谁在看"。
		"/api/auth/login": true, "/api/auth/register": true, "/api/auth/logout": true,
	}
	for p, k := range routeKindSet {
		if k == KindWrite && !want[p] {
			t.Fatalf("多了一个写口：%s —— 写口每多一个，就越权面多一条", p)
		}
		if want[p] && k != KindWrite {
			t.Fatalf("%s 应当是写口", p)
		}
	}
}

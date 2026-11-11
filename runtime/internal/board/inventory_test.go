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
	if queries != 1 {
		t.Fatalf("查询面应当只有 /api/query，实际 %d 条", queries)
	}
}

// 写口也只能是那两个（与 CLI 共用领域入口的那两个）。
func TestWriteRoutesAreTheTwoKnownOnes(t *testing.T) {
	want := map[string]bool{WritePath: true, SettlePath: true}
	for p, k := range routeKindSet {
		if k == KindWrite && !want[p] {
			t.Fatalf("多了一个写口：%s —— 写口每多一个，就越权面多一条", p)
		}
		if want[p] && k != KindWrite {
			t.Fatalf("%s 应当是写口", p)
		}
	}
}

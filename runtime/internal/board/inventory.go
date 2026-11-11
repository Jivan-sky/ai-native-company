package board

// 权威面清单：**每一个路由都必须被分类**，加新路由就得先回答「它能不能触达模型」。
//
// 为什么是数据而不是注释：清单会漂。漂了的清单比没有清单更坏 —— 它让人以为已经审过了。
// 所以 Handler 从这里建 mux，测试再检查「分类完整、无重复、模型面只有一处」。
//
// 分类三档：
//   - KindRead：只读，GET/HEAD；
//   - KindWrite：写口，POST，与 CLI 共用同一个领域入口（审批 / 归档）；
//   - KindModel：**会触达模型**的出口 —— 必须有票（authz.Decide），没票一律拒。

import "net/http"

type RouteKind int

const (
	KindRead RouteKind = iota
	KindWrite
	KindModel
	KindQuery // 需要票，但不是模型出口（数据查询）
)

type Route struct {
	Path   string
	Kind   RouteKind
	Handle http.HandlerFunc
}

func (r Route) String() string {
	switch r.Kind {
	case KindWrite:
		return r.Path + "（写）"
	case KindModel:
		return r.Path + "（模型）"
	default:
		return r.Path + "（读）"
	}
}

// Routes 返回这个服务对外暴露的全部动态路由。静态资源（"/"）不在其中。
func (s *Server) Routes() []Route {
	return []Route{
		{"/api/board", KindRead, s.handleBoard},
		{"/api/issues", KindRead, s.handleIssues},
		{"/api/runtime", KindRead, s.handleRuntime},
		{"/api/dataflow", KindRead, s.handleDataflow},
		{"/api/assets", KindRead, s.handleAssets},
		{"/api/search", KindRead, s.handleSearch},
		{"/api/distill", KindRead, s.handleDistill},
		{"/api/timeline", KindRead, s.handleTimeline},
		{"/api/audit", KindRead, s.handleAudit},
		{"/api/approvals", KindRead, s.handleApprovals},
		{"/api/hot", KindRead, s.handleHot},
		{"/api/agents", KindRead, s.handleAgents},
		{"/api/context", KindRead, s.handleContext},
		{"/api/grants", KindRead, s.handleGrants},
		{"/api/models", KindRead, s.handleModels},
		{"/api/apps", KindRead, s.handleApps},
		{"/api/jobs", KindRead, s.handleJobs},
		{WritePath, KindWrite, s.handleApprovalsDecide},
		{SettlePath, KindWrite, s.handleTaskSettle},
		{chatPath, KindModel, s.handleChat},
		{chatSessionPath, KindModel, s.handleChatSession},
		{"/api/datasources", KindRead, s.handleDataSources},
		{"/api/catalog", KindRead, s.handleCatalog},
		{"/api/query", KindQuery, s.handleQuery},
	}
}

// routeKindSet 由 Routes 构建一次（测试里也会用到）。这里用一个只读表，
// 因为 readOnly 是包级函数，拿不到 Server 实例。
var routeKindSet = map[string]RouteKind{
	"/api/board": KindRead, "/api/issues": KindRead, "/api/runtime": KindRead,
	"/api/dataflow": KindRead, "/api/assets": KindRead, "/api/search": KindRead,
	"/api/distill": KindRead, "/api/timeline": KindRead, "/api/audit": KindRead,
	"/api/approvals": KindRead, "/api/hot": KindRead, "/api/agents": KindRead,
	"/api/context": KindRead, "/api/grants": KindRead, "/api/models": KindRead,
	"/api/apps": KindRead, "/api/jobs": KindRead,
	WritePath: KindWrite, SettlePath: KindWrite,
	chatPath: KindModel, chatSessionPath: KindModel,
	"/api/datasources": KindRead, "/api/query": KindQuery, "/api/catalog": KindRead,
}

func routeKind(p string) (RouteKind, bool) {
	k, ok := routeKindSet[p]
	return k, ok
}

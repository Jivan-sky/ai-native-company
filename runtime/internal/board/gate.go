package board

// 模型出口的授权闸：**先判定，再干活**。
//
// 顺序是有讲究的：**先闸后配置**。没配端点 → 501 是运维信息；但它不该先于鉴权出现，
// 否则未鉴权的调用方就能探测「这台机器配没配模型」。
//
// fail-closed 三处：
//   - 没有票 / 票坏了 → 401；
//   - 票不能用于这个应用 / 这个注册名 / 已被撤销 → 403；
//   - 预算预留失败（含存储读不出来）→ 429 / 503 —— 都**不放行**。
//
// 每次拒绝都落一条 audit（result=denied, source=gate）：事后只凭台账就能还原
// 「谁在哪一刻被哪一条理由挡住」。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"anc/internal/audit"
	"anc/internal/authz"
)

// GateConfig 是这一台机器上的授权配置。
type GateConfig struct {
	PubKey  []byte            // Ed25519 公钥（签发端持有私钥）
	Revoked authz.Revocations // 撤销表（jti）
	Budgets authz.Budgets     // 预算账（jti → 已花）
	ModelID string            // 没在请求里指定时，用这个注册名
}

func (s *Server) deny(w http.ResponseWriter, r *http.Request, code int, reason, app, jti string) {
	writeJSON(w, code, map[string]any{"error": reason, "reason": reason})
	// 拒绝也进账：只记成功的系统，事后回答不了「为什么那次没跑」。
	if strings.TrimSpace(s.Vault) == "" {
		return
	}
	_, _ = audit.Append(s.Vault, audit.Record{
		ID: fmt.Sprintf("gate:%d:%s", time.Now().UnixNano(), jti), At: s.now().Format(time.RFC3339),
		Actor:  firstNonEmpty(app, "unknown"),
		Action: audit.ActionInvoke, Object: "model:" + firstNonEmpty(jti, "no-token"),
		Result: audit.ResultDenied, Why: reason, Source: "gate", Ref: r.URL.Path,
	})
}

// authorize 是**唯一**的放行判断。返回 claims 与预留金额；不放行时已写好响应。
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, modelID string) (claims authz.Claims, reserved float64, ok bool) {
	if s.Gate == nil {
		// 配了端点却没配闸：拒绝服务，绝不放行。
		s.deny(w, r, http.StatusServiceUnavailable, "没有配置授权闸（gate）—— fail-closed：不放行", "", "")
		return claims, 0, false
	}
	raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if raw == "" {
		s.deny(w, r, http.StatusUnauthorized, "缺少 Authorization: Bearer <委派令牌>", "", "")
		return claims, 0, false
	}
	c, err := authz.Verify(s.Gate.PubKey, raw, s.now())
	if err != nil {
		s.deny(w, r, http.StatusUnauthorized, err.Error(), "", "")
		return claims, 0, false
	}
	if strings.TrimSpace(modelID) == "" {
		s.deny(w, r, http.StatusForbidden, "没有指定要用的注册名（model_id）", c.Sub, c.JTI)
		return c, 0, false
	}
	if _, err := authz.Decide(c, authz.Request{
		App: c.Sub, Aud: "chat", Model: modelID, Revoked: s.Gate.Revoked.Revoked,
	}, s.now()); err != nil {
		s.deny(w, r, http.StatusForbidden, err.Error(), c.Sub, c.JTI)
		return c, 0, false
	}
	okB, _, err := s.Gate.Budgets.TryReserve(c.JTI, c.BudgetUSD)
	if err != nil {
		s.deny(w, r, http.StatusServiceUnavailable, "预算账读不出来（fail-closed）："+err.Error(), c.Sub, c.JTI)
		return c, 0, false
	}
	if !okB {
		s.deny(w, r, http.StatusTooManyRequests,
			fmt.Sprintf("预算不够：这张票的额度是 %.4f，已经预留掉了", c.BudgetUSD), c.Sub, c.JTI)
		return c, 0, false
	}
	return c, s.Gate.Budgets.Reserve, true
}

var _ = json.Marshal

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

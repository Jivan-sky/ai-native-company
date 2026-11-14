package board

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"anc/internal/org"
)

// 授权表的两个写口：签发一条、撤回一条。
//
// 它们走的是 CLI 用过的**同一批领域入口**（org.AppendGrant / org.Revoke），不是另写一套 ——
// 否则"命令行能签、看板签出另一份"只是时间问题。
//
// 三条边界：
//  1 只接受 POST（写口统一由 readOnly 管）
//  2 名字当文件名用：slug 只收小写 ASCII（AppendGrant 里已经拦了，这里提前说清楚）
//  3 撤回不是删除：追加一条撤权记录并把权威版本 +1，历史留着

type grantReq struct {
	Slug      string `json:"slug"`
	To        string `json:"to"`
	Action    string `json:"action"`
	Object    string `json:"object"`
	IssuedBy  string `json:"issued_by"`
	ExpiresAt string `json:"expires_at"`
	Parent    string `json:"parent"`
	Reason    string `json:"reason"`
}

type revokeReq struct {
	Ref string `json:"ref"`
	By  string `json:"by"`
}

func (s *Server) handleAuthzGrant(w http.ResponseWriter, r *http.Request) {
	var req grantReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法 JSON：" + err.Error()})
		return
	}
	auth, wired, err := org.LoadAuthority(s.Vault)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "权威源读不动：" + err.Error()})
		return
	}
	if !wired {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "还没有权威源（authority.md）—— 没有受托签发人的世界不该有根授权",
			"hint":  "先跑 anc authz init <vault> --issuer member:<名>",
		})
		return
	}
	by := firstNonEmpty(req.IssuedBy, auth.Issuer)
	g := org.Grant{
		Slug: strings.TrimSpace(req.Slug), From: by, To: strings.TrimSpace(req.To),
		Action: strings.TrimSpace(req.Action), Object: strings.TrimSpace(req.Object),
		IssuedBy: by, ExpiresAt: strings.TrimSpace(req.ExpiresAt),
		Parent: strings.TrimSpace(req.Parent), Reason: strings.TrimSpace(req.Reason),
	}
	path, err := org.AppendGrant(s.Vault, g)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, org.ErrGrantExists) {
			code = http.StatusConflict
		}
		writeJSON(w, code, map[string]any{"error": s.scrub(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": s.scrub(path), "issued_by": by})
}

func (s *Server) handleAuthzRevoke(w http.ResponseWriter, r *http.Request) {
	var req revokeReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法 JSON：" + err.Error()})
		return
	}
	ref := strings.TrimSpace(req.Ref)
	if ref == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "要撤哪一条？给 slug 或 grants/<slug>.md"})
		return
	}
	by := strings.TrimSpace(req.By)
	if by == "" {
		if a, wired, _ := org.LoadAuthority(s.Vault); wired {
			by = a.Issuer
		}
	}
	a, err := org.Revoke(s.Vault, ref, by)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, org.ErrNoAuthority) {
			code = http.StatusConflict
		}
		writeJSON(w, code, map[string]any{"error": s.scrub(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": a.Version, "revoked": a.Revoked,
		"note": "已撤回。撤权从这一版起生效 —— 命中这条授权的行使会被判为拒绝。",
	})
}

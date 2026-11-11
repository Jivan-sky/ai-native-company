package board

// `GET /api/catalog?ds=<name>` —— 目录详情：表/列/分类/策略摘要。
// 只出元数据与策略（策略里没有密钥），供看板「数据」页渲染。

import (
	"net/http"
	"path/filepath"

	"anc/internal/catalog"
	"anc/internal/policy"
)

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	ds := r.URL.Query().Get("ds")
	if ds == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺 ds 参数"})
		return
	}
	snap, err := catalog.Load(s.Vault, ds)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"schema": "anc.catalogview/v1", "wired": false, "why": err.Error()})
		return
	}
	pol, _ := policy.Load(filepath.Join(filepath.Dir(s.Vault), "policies"), ds)
	type polView struct {
		Rules      []policy.Rule      `json:"rules"`
		RowFilters []policy.RowFilter `json:"row_filters,omitempty"`
		Default    string             `json:"default"`
		Derived    string             `json:"derived"`
	}
	var pv *polView
	if pol != nil {
		pv = &polView{Rules: pol.Rules, RowFilters: pol.RowFilters, Default: pol.Default, Derived: pol.Derived}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema": "anc.catalogview/v1", "wired": true,
		"datasource": ds, "fingerprint": snap.Fingerprint, "scanned_at": snap.ScannedAt,
		"tables": snap.Tables, "classifications": snap.Classifications, "policy": pv,
	})
}

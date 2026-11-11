package board

// 数据面的两个出口：
//   GET  /api/datasources —— 数据源 + 目录摘要 + 分类 + 策略有没有配
//   POST /api/query       —— 以票上的主体查一次（策略、行过滤、脱敏都在服务端做）
//
// 口径与 CLI 完全一致：都走 `queryexec.Run`。否则「命令行拦了、接口没拦」就是必然。

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"anc/internal/authz"
	"anc/internal/catalog"
	"anc/internal/ds"
	"anc/internal/policy"
	"anc/internal/queryexec"
)

type dsRow struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	ReadOnly    bool   `json:"read_only"`
	Tables      int    `json:"tables,omitempty"`
	Columns     int    `json:"columns,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Classified  int    `json:"classified,omitempty"`
	HasPolicy   bool   `json:"has_policy"`
}

func (s *Server) handleDataSources(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(filepath.Dir(s.Vault), "datasources")
	ents, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"schema": "anc.datasources/v1", "wired": false, "why": err.Error()})
		return
	}
	polDir := filepath.Join(filepath.Dir(s.Vault), "policies")
	var rows []dsRow
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		row := dsRow{Name: name}
		if src, err := ds.Load(s.Vault, name); err == nil {
			row.Type, row.ReadOnly = src.Type, src.ReadOnly
		} else if fsrc, ferr := loadFileSource(s.Vault, name); ferr == nil {
			row.Type, row.ReadOnly = "file", true
			_ = fsrc
		} else {
			continue
		}
		if snap, err := catalog.Load(s.Vault, name); err == nil {
			row.Tables = len(snap.Tables)
			for _, t := range snap.Tables {
				row.Columns += len(t.Columns)
			}
			row.Fingerprint = snap.Fingerprint
			row.Classified = len(snap.Classifications)
		}
		if pol, err := policy.Load(polDir, name); err == nil && pol != nil {
			row.HasPolicy = true
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"schema": "anc.datasources/v1", "wired": true, "datasources": rows})
}

type queryReq struct {
	DataSource string `json:"datasource"`
	SQL        string `json:"sql"`
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	var req queryReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法 JSON：" + err.Error()})
		return
	}
	raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if raw == "" || s.Gate == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "缺少 Authorization: Bearer <查询票>"})
		return
	}
	c, err := authz.Verify(s.Gate.PubKey, raw, s.now())
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
		return
	}
	if _, err := authz.Decide(c, authz.Request{
		App: c.Sub, Aud: "query", Model: req.DataSource, Revoked: s.Gate.Revoked.Revoked,
	}, s.now()); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}
	res, err := queryexec.Run(r.Context(), s.Vault, req.DataSource, c.Sub, req.SQL, 0)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "策略拒绝") {
			status = http.StatusForbidden
		}
		writeJSON(w, status, map[string]any{"error": err.Error(), "subject": c.Sub})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

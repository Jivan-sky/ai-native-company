package board

// `GET /api/apps` —— 已安装的脚本应用（状态 / 版本 / hash / 谁审的）。
//
// 与 `/api/models` 同一条口径：**只出事实，不出密钥**。应用包里的任何东西都不在这里回显，
// 只有它的身份（name/version/hash）与状态。

import (
	"net/http"

	"anc/internal/apps"
)

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	list, err := apps.List(s.Vault)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"schema": "anc.apps/v1", "wired": false, "why": err.Error()})
		return
	}
	type row struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		State      string `json:"state"`
		SHA256     string `json:"sha256"`
		ApprovedBy string `json:"approved_by,omitempty"`
		ApprovedAt string `json:"approved_at,omitempty"`
	}
	rows := make([]row, 0, len(list))
	for _, a := range list {
		short := a.SHA256
		if len(short) > 12 {
			short = short[:12]
		}
		rows = append(rows, row{a.Name, a.Version, a.State, short, a.ApprovedBy, a.ApprovedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": "anc.apps/v1", "wired": true, "apps": rows})
}

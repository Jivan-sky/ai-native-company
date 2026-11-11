package board

// `GET /api/jobs` —— 定时作业：下次几点跑、上次成败与原因。
//
// 数据全部来自已有的两份东西：作业 spec（`<vault>/../jobs/*.json`）与运行态
// （`<vault>/../data/jobs/*.json`）。看板不另算一份「该不该跑」——
// 下次时间直接调 `job.Due`，与调度器同一个函数，免得两边算法漂了。

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"anc/internal/job"
)

type jobRow struct {
	Name      string `json:"name"`
	Title     string `json:"title,omitempty"`
	Enabled   bool   `json:"enabled"`
	Every     string `json:"every,omitempty"`
	At        string `json:"at,omitempty"`
	TZ        string `json:"tz,omitempty"`
	Kind      string `json:"kind"` // http | app
	LastAt    string `json:"last_at,omitempty"`
	LastOK    bool   `json:"last_ok"`
	LastError string `json:"last_error,omitempty"`
	NextAt    string `json:"next_at,omitempty"`
	// 下面这几个是「接入」页要用的：接了什么、打到哪、用哪个密钥（只给**键名**）、写到哪。
	// 密钥的值永远不进看板 —— 它只存在于 secrets.env 里，spec 里也只有名字。
	Method    string `json:"method,omitempty"`
	URL       string `json:"url,omitempty"`
	Secret    string `json:"secret,omitempty"`
	App       string `json:"app,omitempty"`
	Timeout   string `json:"timeout,omitempty"`
	WritePath string `json:"write_path,omitempty"`
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	parent := filepath.Dir(s.Vault)
	jobsDir := filepath.Join(parent, "jobs")
	dataDir := filepath.Join(parent, "data")

	ents, err := os.ReadDir(jobsDir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"schema": "anc.jobs/v1", "wired": false, "why": err.Error()})
		return
	}
	rows := make([]jobRow, 0, len(ents))
	now := s.now()
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		spec, err := job.Load(filepath.Join(jobsDir, e.Name()))
		if err != nil {
			continue // 坏掉的 spec 由 `anc job ls` 点名，不挡整页
		}
		st, _ := job.ReadState(dataDir, spec.Name)
		_, next := job.Due(spec, st, now)
		kind := "http"
		if spec.App != nil {
			kind = "app"
		}
		row := jobRow{
			Name: spec.Name, Title: spec.Title, Enabled: spec.EnabledOrDefault(),
			Every: spec.Schedule.Every, At: spec.Schedule.At, TZ: spec.Schedule.TZ,
			Kind: kind, NextAt: next.Format("2006-01-02T15:04:05Z07:00"),
			Method: spec.HTTP.Method, URL: spec.HTTP.URL,
			Timeout: spec.Timeout, WritePath: spec.Write.Path,
		}
		if spec.HTTP.Auth != nil {
			row.Secret = spec.HTTP.Auth.Secret
		}
		if spec.App != nil {
			row.App = spec.App.Name
		}
		if st != nil {
			row.LastAt = st.LastAt.Format("2006-01-02T15:04:05Z07:00")
			row.LastOK = st.LastOK
			row.LastError = st.LastError
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"schema": "anc.jobs/v1", "wired": true, "jobs": rows})
}

package board

// 文件目录的看板出口：
//   GET /api/files?ds=            快照摘要（不读内容）
//   GET /api/files/ls?ds=&path=   实时列目录（按票上主体过滤）
//   GET /api/files/cat?ds=&path=  读文件（按主体判策略，带上限）

import (
	"net/http"
	"path/filepath"
	"strings"

	"anc/internal/authz"
	"anc/internal/files"
	"anc/internal/policy"
)

func (s *Server) handleFileSnapshot(w http.ResponseWriter, r *http.Request) {
	ds := r.URL.Query().Get("ds")
	if ds == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺 ds"})
		return
	}
	snap, err := files.LoadSnapshot(s.Vault, ds)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"schema": "anc.filecatalog/v1", "wired": false, "why": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema": "anc.filecatalog/v1", "wired": true, "datasource": ds,
		"fingerprint": snap.Fingerprint, "scanned_at": snap.ScannedAt,
		"files": snap.Files, "dirs": snap.Dirs,
	})
}

// fileSubject 验票并返回主体；不通过时已写响应。
func (s *Server) fileSubject(w http.ResponseWriter, r *http.Request, ds string) (string, bool) {
	raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if raw == "" || s.Gate == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "缺少 Authorization: Bearer <查询票>"})
		return "", false
	}
	c, err := authz.Verify(s.Gate.PubKey, raw, s.now())
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
		return "", false
	}
	if _, err := authz.Decide(c, authz.Request{App: c.Sub, Aud: "query", Model: ds, Revoked: s.Gate.Revoked.Revoked}, s.now()); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return "", false
	}
	return c.Sub, true
}

func (s *Server) fileAllowed(subject, ds, path string) (bool, string) {
	pol, err := policy.Load(filepath.Join(filepath.Dir(s.Vault), "policies"), ds)
	if err != nil {
		return false, "策略读不动：" + err.Error()
	}
	switch pol.Action(subject, path, "*") {
	case "allow":
		return true, ""
	case "mask":
		return false, "该路径按策略脱敏，文件内容不支持脱敏"
	default:
		return false, "策略拒绝：" + subject + " 不能访问 " + path
	}
}

func (s *Server) handleFileLs(w http.ResponseWriter, r *http.Request) {
	ds := r.URL.Query().Get("ds")
	path := r.URL.Query().Get("path")
	subject, ok := s.fileSubject(w, r, ds)
	if !ok {
		return
	}
	src, err := files.Load(s.Vault, ds)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	ents, truncated, err := files.List(src, path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	type row struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		Dir   bool   `json:"dir"`
		Size  int64  `json:"size"`
		MTime string `json:"mtime"`
	}
	var rows []row
	hidden := 0
	for _, e := range ents {
		p := e.Path
		if e.Dir {
			p += "/"
		}
		if ok, _ := s.fileAllowed(subject, ds, p); ok {
			rows = append(rows, row{e.Name, e.Path, e.Dir, e.Size, e.MTime})
		} else {
			hidden++
		}
	}
	// 被策略挡掉的条目要**说数量**：列表空着不说，会让人以为目录是空的。
	writeJSON(w, http.StatusOK, map[string]any{
		"wired": true, "path": path, "entries": rows, "truncated": truncated, "hidden": hidden,
	})
}

func (s *Server) handleFileCat(w http.ResponseWriter, r *http.Request) {
	ds := r.URL.Query().Get("ds")
	path := r.URL.Query().Get("path")
	subject, ok := s.fileSubject(w, r, ds)
	if !ok {
		return
	}
	if ok, why := s.fileAllowed(subject, ds, path); !ok {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": why})
		return
	}
	src, err := files.Load(s.Vault, ds)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	b, truncated, err := files.Read(src, path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"wired": true, "path": path, "content": string(b), "truncated": truncated})
}

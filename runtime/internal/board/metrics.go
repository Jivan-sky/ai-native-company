package board

// /api/metrics —— 口径由客户定义（`<vault>/metrics.md`），这里只负责算。
//
// 没有定义 = 不上屏；算不出来 = 说清为什么。**不退回代码里的硬编码口径** ——
// 那等于口径还是我们定的，客户只是"看到"而不是"定义"。

import (
	"net/http"

	"anc/internal/metrics"
)

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	rep, err := metrics.Compute(s.Vault, s.now())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"schema": "anc.metrics/v1",
			"error":  s.scrub(firstLine(err.Error())),
			"values": []any{},
		})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

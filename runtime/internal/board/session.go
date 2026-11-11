package board

// `POST /api/chat/session` —— 给**本机看板界面**发一张短时票。
//
// 为什么需要它：面板要调 `/api/chat`，而那条路有闸。让用户在浏览器里手贴 token 不现实；
// 让闸对界面豁免，闸就成了装饰。折中：看板自己签一张**短时、限注册名、限额度**的票，
// 和 CLI 签的那张走同一套 `authz`。
//
// 边界（写死在代码里）：
//   - 只在"配了端点 + 配了闸 + 有私钥"时才发；否则 501；
//   - 票只活 10 分钟、只给闸默认的那个注册名、额度小（界面只用来问问题，不是跑批）；
//   - 看板默认只绑 127.0.0.1；对外暴露必须显式 `--insecure-bind`（那是另一个决定）。

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"anc/internal/authz"
)

func (s *Server) handleChatSession(w http.ResponseWriter, r *http.Request) {
	if s.Gate == nil || len(s.Gate.PrivKey) != ed25519.PrivateKeySize {
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"error": "没有配好授权（缺私钥或默认注册名）—— 先 `anc delegate issue` 生成密钥",
		})
		return
	}
	now := s.now()
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	// 模型票只在配了默认注册名时才签（没配就只给查询票）。
	tok := ""
	if s.Gate.ModelID != "" {
		t, err := authz.Sign(ed25519.PrivateKey(s.Gate.PrivKey), "k1", authz.Claims{
			Sub: "board-ui", Aud: "chat", Scope: []string{s.Gate.ModelID},
			BudgetUSD: 0.5, IAT: now.Unix(), Exp: now.Add(10 * time.Minute).Unix(),
			JTI: "board-" + hex.EncodeToString(rnd[:]),
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		tok = t
	}
	// 界面也要能查数据：另签一张 aud=query 的票，scope 是全部数据源名。
	qTok, qerr := authz.Sign(ed25519.PrivateKey(s.Gate.PrivKey), "k1", authz.Claims{
		Sub: "board-ui", Aud: "query", Scope: s.dataSourceNames(),
		BudgetUSD: 0.5, IAT: now.Unix(), Exp: now.Add(10 * time.Minute).Unix(),
		JTI: "board-q-" + hex.EncodeToString(rnd[:]),
	})
	if qerr != nil {
		qTok = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": tok, "model": s.Gate.ModelID, "query_token": qTok,
		"expires_at": now.Add(10 * time.Minute).Format(time.RFC3339),
	})
}

// dataSourceNames 列出全部数据源名（签查询票的 scope 用）。
func (s *Server) dataSourceNames() []string {
	ents, err := os.ReadDir(filepath.Join(filepath.Dir(s.Vault), "datasources"))
	if err != nil {
		return []string{}
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(out)
	return out
}

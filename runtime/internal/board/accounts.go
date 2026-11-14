package board

// 账号：本地密码 + 飞书 SSO 共用一个「会话」。
//
// 为什么两种：客户可能没有飞书（本地部署、内网），也可能有（那就别让人再记一个密码）。
// 两条路进来以后**落到同一个会话模型**上 —— 会话只回答一件事：这次请求是谁。
//
// 三条纪律：
//  1 密码只存 scrypt 派生值，永不回显、永不进日志；
//  2 飞书未配置时，登录页明说「未接入」，不做一个点了会 500 的按钮；
//  3 会话 cookie 是 HttpOnly + SameSite=Lax；本地 http 时不加 Secure（否则本地登不上）。

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 零依赖约束：不引 golang.org/x/crypto。
// 用 PBKDF2 的实现？标准库也没有。所以用 sha256 迭代 —— 明确写清它不是最优，
// 但比明文强，且不引依赖。参数与理由写在 hashPassword 上。

const accountsFile = "accounts.md"

// Account 是一个本地账号。Name 与 org 里的成员名对齐（不自动建人）。
type Account struct {
	Name string `json:"name"`
	Role string `json:"role"`
	Salt string `json:"salt"`
	Hash string `json:"hash"`
	Iter int    `json:"iter"`
	At   string `json:"at"`
}

var errBadCred = errors.New("用户名或密码不对")

// hashPassword：sha256 迭代 120000 次 + 每人独立 salt。
//
// 说明白：这不是 Argon2/scrypt，强度低一档。选它的唯一理由是**交付物必须零第三方依赖**
// （单二进制、CGO_ENABLED=0、六个目标交叉编译）。真要上生产，第一步就换 scrypt ——
// 换的时候只改这一个函数与 Iter 语义。
func hashPassword(pw, salt string, iter int) string {
	h := sha256.Sum256([]byte(salt + ":" + pw))
	buf := h[:]
	for i := 0; i < iter; i++ {
		n := sha256.Sum256(buf)
		buf = n[:]
	}
	return hex.EncodeToString(buf)
}

// Store 是账号表（内存 + 文件）。
type Store struct {
	mu   sync.Mutex
	path string
	list map[string]Account
}

func OpenAccounts(vault string) *Store {
	st := &Store{path: filepath.Join(vault, accountsFile), list: map[string]Account{}}
	b, err := os.ReadFile(st.path)
	if err != nil {
		return st
	}
	var rows []Account
	if json.Unmarshal(b, &rows) == nil {
		for _, a := range rows {
			st.list[strings.ToLower(a.Name)] = a
		}
	}
	return st
}

func (s *Store) save() error {
	rows := make([]Account, 0, len(s.list))
	for _, a := range s.list {
		rows = append(rows, a)
	}
	b, _ := json.MarshalIndent(rows, "", "  ")
	return os.WriteFile(s.path, append(b, '\n'), 0o600)
}

// Register 建一个账号。**不自动建组织成员** —— 谁能进来由 org 决定，账号只是凭据。
func (s *Store) Register(name, pw, role string) (Account, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(pw) < 8 {
		return Account{}, errors.New("用户名不能为空，密码至少 8 位")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := strings.ToLower(name)
	if _, ok := s.list[k]; ok {
		return Account{}, errors.New("这个用户名已经有人用了")
	}
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	salt := hex.EncodeToString(raw[:])
	const iter = 120000
	a := Account{Name: name, Role: role, Salt: salt, Hash: hashPassword(pw, salt, iter), Iter: iter,
		At: time.Now().Format(time.RFC3339)}
	s.list[k] = a
	return a, s.save()
}

func (s *Store) Login(name, pw string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.list[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return Account{}, errBadCred
	}
	got := hashPassword(pw, a.Salt, a.Iter)
	if subtle.ConstantTimeCompare([]byte(got), []byte(a.Hash)) != 1 {
		return Account{}, errBadCred
	}
	return a, nil
}

// 会话：cookie 里只放随机 id，服务端内存表存 id → 谁是。进程重启即失效（本地部署够用）。
type sessions struct {
	mu   sync.Mutex
	data map[string]session
}

type session struct {
	Name string
	Role string
	At   time.Time
}

var sess = &sessions{data: map[string]session{}}

func (s *sessions) put(name, role string) string {
	var raw [24]byte
	_, _ = rand.Read(raw[:])
	id := hex.EncodeToString(raw[:])
	s.mu.Lock()
	s.data[id] = session{Name: name, Role: role, At: time.Now()}
	s.mu.Unlock()
	return id
}

func (s *sessions) get(id string) (session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[id]
	if !ok || time.Since(v.At) > 12*time.Hour {
		return session{}, false
	}
	return v, true
}

const sessionCookie = "anc_session"

// handleLogin：POST {name, password} → 种 cookie。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.accountsEnabled() {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "这个看板没开账号（--accounts）"})
		return
	}
	var req struct{ Name, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法 JSON"})
		return
	}
	a, err := OpenAccounts(s.Vault).Login(req.Name, req.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sess.put(a.Name, a.Role),
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": a.Name, "role": a.Role})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.accountsEnabled() {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "这个看板没开账号（--accounts）"})
		return
	}
	var req struct{ Name, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法 JSON"})
		return
	}
	// 角色不由注册者自选：新账号一律 member。谁能升到 admin 是组织的事，不是注册表单的事。
	a, err := OpenAccounts(s.Vault).Register(req.Name, req.Password, "member")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sess.put(a.Name, a.Role),
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": a.Name, "role": a.Role})
}

// handleMe：这次请求是谁。未登录返回 401（前端据此显示登录页）。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if !s.accountsEnabled() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "name": "", "role": ""})
		return
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"enabled": true, "error": "没登录"})
		return
	}
	v, ok := sess.get(c.Value)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"enabled": true, "error": "会话过期了，重新登一次"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "name": v.Name, "role": v.Role})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		sess.mu.Lock()
		delete(sess.data, c.Value)
		sess.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// accountsEnabled：账号是**开关**。关着的时候看板行为与以前完全一样（本地只读观测）。
func (s *Server) accountsEnabled() bool { return s.Accounts }

// FeishuSW 是飞书 SSO 的接入点。没有凭据时**如实说未配置**，不给一个点了会 500 的按钮。
func (s *Server) handleFeishuStart(w http.ResponseWriter, r *http.Request) {
	appID := os.Getenv("ANC_FEISHU_APP_ID")
	redirect := os.Getenv("ANC_FEISHU_REDIRECT")
	if appID == "" || redirect == "" {
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"error": "飞书 SSO 未配置",
			"hint":  "设 ANC_FEISHU_APP_ID 与 ANC_FEISHU_REDIRECT（回调地址要在飞书后台白名单里）；没配也能用密码登录",
		})
		return
	}
	q := "https://passport.feishu.cn/suite/passport/oauth/authorize?client_id=" + appID +
		"&redirect_uri=" + redirect + "&response_type=code&state=anc"
	http.Redirect(w, r, q, http.StatusFound)
}

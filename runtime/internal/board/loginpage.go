package board

// 登录页 / 注册页 / 飞书授权回调页。
//
// 为什么是**服务端直出的一个 HTML**，不进 SPA：未登录时 SPA 拿不到任何数据，
// 强行让它先跑一遍"读投影"的流程只会卡在 loading 上。登录是入口，不是应用内的一页。
//
// 三件东西在这一个文件里：页面（内联模板）、飞书回调、以及"未配置"时的如实说明。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const loginHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>登录 · ANC</title>
<style>
  :root { color-scheme: light; }
  body { margin:0; min-height:100vh; display:grid; place-items:center; font: 14px/1.6 -apple-system,"PingFang SC","Microsoft YaHei",sans-serif;
         background: radial-gradient(1100px 620px at 8% -12%, rgba(94,106,210,.13), transparent 68%),
                     radial-gradient(900px 560px at 96% 108%, rgba(45,156,190,.11), transparent 70%), #eef1f6; color:#14161a; }
  .card { width:min(392px,92vw); padding:28px; border-radius:16px; background:rgba(255,255,255,.78);
          backdrop-filter: blur(20px) saturate(180%); border:1px solid rgba(255,255,255,.7);
          box-shadow: inset 0 1px 0 rgba(255,255,255,.6), 0 18px 50px rgba(16,22,40,.10); }
  h1 { margin:0 0 4px; font-size:20px; letter-spacing:-.01em; }
  .sub { margin:0 0 20px; color:#5b6472; font-size:12.5px; }
  .tabs { display:flex; gap:6px; margin-bottom:16px; }
  .tabs button { flex:1; padding:8px; border-radius:9px; border:1px solid #dfe3ea; background:#fff; font:inherit; cursor:pointer; color:#5b6472; }
  .tabs button.on { background:#3b5bdb; border-color:#3b5bdb; color:#fff; font-weight:600; }
  label { display:block; margin:10px 0 4px; font-size:12px; color:#5b6472; }
  input { width:100%; box-sizing:border-box; padding:10px 12px; border-radius:10px; border:1px solid #dfe3ea; font:inherit; background:#fff; }
  input:focus { outline:2px solid #3b5bdb; outline-offset:1px; }
  .go { width:100%; margin-top:16px; padding:11px; border:0; border-radius:10px; background:#3b5bdb; color:#fff; font:inherit; font-weight:600; cursor:pointer; }
  .go:disabled { opacity:.6; }
  .sep { display:flex; align-items:center; gap:10px; margin:18px 0 12px; color:#8b93a1; font-size:12px; }
  .sep::before,.sep::after { content:""; flex:1; height:1px; background:#dfe3ea; }
  .feishu { width:100%; padding:10px; border-radius:10px; border:1px solid #dfe3ea; background:#fff; font:inherit; cursor:pointer; }
  .feishu:disabled { opacity:.5; cursor:not-allowed; }
  .msg { min-height:20px; margin:12px 0 0; font-size:12.5px; color:#c03027; }
  .msg.ok { color:#0f9d6e; }
  .hint { margin-top:14px; font-size:11.5px; color:#8b93a1; line-height:1.7; }
</style></head><body>
<div class="card">
  <h1>ANC</h1>
  <p class="sub">登录后才知道你看到的是哪一部分。</p>
  <div class="tabs">
    <button id="tab-login" class="on" type="button">登录</button>
    <button id="tab-reg" type="button">注册</button>
  </div>
  <div id="form">
    <label>用户名</label><input id="name" autocomplete="username">
    <label>密码</label><input id="pw" type="password" autocomplete="current-password">
    <button id="go" class="go" type="button">登录</button>
  </div>
  <div class="sep">或</div>
  <button id="fs" class="feishu" type="button">用飞书登录</button>
  <p id="msg" class="msg"></p>
  <p class="hint">密码登录是本机账号（存在 vault 的 accounts.md 里）。飞书登录需要部署时配好 App ID 与回调地址。</p>
</div>
<script>
(function(){
  var mode = "login";
  var tabLogin = document.getElementById("tab-login"), tabReg = document.getElementById("tab-reg");
  var go = document.getElementById("go"), msg = document.getElementById("msg");
  function setMode(m){
    mode = m;
    tabLogin.className = m === "login" ? "on" : "";
    tabReg.className = m === "reg" ? "on" : "";
    go.textContent = m === "login" ? "登录" : "创建账号";
    msg.textContent = ""; msg.className = "msg";
  }
  tabLogin.onclick = function(){ setMode("login"); };
  tabReg.onclick = function(){ setMode("reg"); };
  go.onclick = function(){
    var name = document.getElementById("name").value.trim();
    var pw = document.getElementById("pw").value;
    if (!name || !pw) { msg.textContent = "用户名和密码都要填。"; return; }
    go.disabled = true; msg.textContent = "";
    fetch(mode === "login" ? "/api/auth/login" : "/api/auth/register", {
      method: "POST", headers: {"Content-Type": "application/json"},
      body: JSON.stringify({name: name, password: pw})
    }).then(function(r){ return r.json().then(function(b){ return {s:r.status, b:b}; }); })
      .then(function(x){
        go.disabled = false;
        if (x.s === 200 && x.b.ok) { msg.className = "msg ok"; msg.textContent = "好了，正在进入…"; location.href = "/"; return; }
        msg.textContent = (x.b && x.b.error) || ("没成功（HTTP " + x.s + "）");
      }).catch(function(){ go.disabled = false; msg.textContent = "连不上服务——它还在跑吗？"; });
  };
  document.getElementById("fs").onclick = function(){ location.href = "/auth/feishu/start"; };
  fetch("/api/auth/me").then(function(r){ return r.json(); }).then(function(b){
    if (b && b.enabled === false) {
      msg.textContent = "这个看板没开账号（启动时加 ANC_ACCOUNTS=1），不需要登录。";
      document.getElementById("fs").disabled = true;
      go.disabled = true;
    }
  }).catch(function(){});
})();
</script></body></html>`

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, loginHTML)
}

// feishuUser 是我们从飞书那边需要的全部：一个稳定 id + 一个能对得上的人名。
type feishuUser struct {
	OpenID string
	Name   string
}

// handleFeishuCallback 收 code，换 user_access_token，再取用户信息。
//
// 未配置时给一个**能读的页面**，说清缺哪两个环境变量 —— 不是 500，也不是白屏。
func (s *Server) handleFeishuCallback(w http.ResponseWriter, r *http.Request) {
	appID, secret := os.Getenv("ANC_FEISHU_APP_ID"), os.Getenv("ANC_FEISHU_APP_SECRET")
	redirect := os.Getenv("ANC_FEISHU_REDIRECT")
	if appID == "" || secret == "" || redirect == "" {
		s.loginNotice(w, http.StatusNotImplemented, "飞书 SSO 未配置",
			"需要在启动看板前设好三个环境变量：ANC_FEISHU_APP_ID、ANC_FEISHU_APP_SECRET、ANC_FEISHU_REDIRECT。"+
				"回调地址要登记在飞书开放平台的应用里。没配也能用本机密码登录。")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		s.loginNotice(w, http.StatusBadRequest, "没有拿到授权码", "从飞书回来时没有 code —— 可能授权页被关掉了，重新来一次。")
		return
	}
	u, err := exchangeFeishu(appID, secret, redirect, code)
	if err != nil {
		s.loginNotice(w, http.StatusBadGateway, "飞书那边没换到身份", err.Error())
		return
	}
	// 映射到组织：**不自动建人**。飞书里有、org 里没有 → 拒绝并说清找谁。
	acct, lerr := OpenAccounts(s.Vault).EnsureFeishu(u.OpenID, u.Name)
	if lerr != nil {
		s.loginNotice(w, http.StatusForbidden, "飞书认出了你，但这个组织里没有你",
			lerr.Error()+"\n\n找管理员在 org 的 members/ 里加上你，再登一次。")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sess.put(acct.Name, acct.Role),
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) loginNotice(w http.ResponseWriter, code int, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;font:14px/1.7 -apple-system,"PingFang SC",sans-serif;background:#eef1f6;color:#14161a}
.c{width:min(520px,92vw);padding:26px;border-radius:16px;background:#fff;border:1px solid #dfe3ea}
h1{margin:0 0 10px;font-size:17px}p{margin:0;white-space:pre-line;color:#5b6472}a{display:inline-block;margin-top:16px;color:#3b5bdb}</style>
<div class=c><h1>%s</h1><p>%s</p><a href="/login">回到登录</a></div>`, title, title, detail)
}

// exchangeFeishu 走飞书 OIDC：code → user_access_token → 用户信息。
// 两个请求都是标准库；失败时把飞书原话带回来（不吞错误，也不猜）。
func exchangeFeishu(appID, secret, redirect, code string) (feishuUser, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type": "authorization_code", "client_id": appID, "client_secret": secret,
		"code": code, "redirect_uri": redirect,
	})
	req, _ := http.NewRequest("POST", "https://open.feishu.cn/open-apis/authen/v2/oauth/token", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	cl := &http.Client{Timeout: 10 * time.Second}
	res, err := cl.Do(req)
	if err != nil {
		return feishuUser{}, fmt.Errorf("连不上飞书：%w", err)
	}
	defer res.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
		Code        int    `json:"code"`
		Msg         string `json:"error_description"`
	}
	_ = json.NewDecoder(res.Body).Decode(&tok)
	if tok.AccessToken == "" {
		return feishuUser{}, fmt.Errorf("换 token 失败：%s", firstNonEmpty(tok.Msg, "飞书没有返回 access_token"))
	}
	req2, _ := http.NewRequest("GET", "https://open.feishu.cn/open-apis/authen/v1/user_info", nil)
	req2.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	res2, err := cl.Do(req2)
	if err != nil {
		return feishuUser{}, fmt.Errorf("取用户信息失败：%w", err)
	}
	defer res2.Body.Close()
	var info struct {
		Code int `json:"code"`
		Data struct {
			OpenID string `json:"open_id"`
			Name   string `json:"name"`
		} `json:"data"`
	}
	_ = json.NewDecoder(res2.Body).Decode(&info)
	if info.Data.OpenID == "" {
		return feishuUser{}, fmt.Errorf("飞书没给 open_id（code=%d）", info.Code)
	}
	return feishuUser{OpenID: info.Data.OpenID, Name: info.Data.Name}, nil
}

var _ = url.QueryEscape

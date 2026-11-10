// Package authz —— 委派令牌与判定器：**唯一的授权点**，fail-closed。
//
// 这一包只做判定：不做存储、不计数、不碰网络。因为「唯一授权点」必须能被穷举证明 ——
// 给定（令牌，请求，现在）三个输入，输出只有 allow / deny，且**任何错误都是 deny**。
//
// 三条口径：
//
//  1. **不自造格式**：EdDSA 的 JWS 紧凑序列化（header.payload.signature），
//     签名输入就是标准的那一段 —— 任何 JWS 库都能验，不依赖我们这一份实现。
//  2. **fail-closed**：解析失败、签名不对、过期、aud 不对、scope 不含所请求的注册名、
//     被撤销 —— 一律 deny，并说清原因（原因进台账，**不含令牌原文**）。
//  3. **判定是纯函数**：时间由调用方注入。过期、撤销时延这些语义因此能在测试里被钉死，
//     而不是等一天或等一次时钟漂移。
package authz

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 判定原因码。进台账用，不含令牌。
const (
	ReasonMalformed   = "令牌读不懂"
	ReasonBadAlg      = "alg 不是 EdDSA"
	ReasonBadSig      = "签名不对"
	ReasonExpired     = "令牌已过期"
	ReasonNotYetValid = "令牌还没生效"
	ReasonWrongAud    = "aud 不是这个出口"
	ReasonWrongApp    = "令牌不是发给这个应用的"
	ReasonOutOfScope  = "该应用没有这个注册名的权限"
)

// ErrDenied 是判定器唯一会返回的错误类型：**只拒绝，不崩溃**。
type ErrDenied struct{ Reason string }

func (e *ErrDenied) Error() string { return "拒绝：" + e.Reason }

func deny(reason string) error { return &ErrDenied{Reason: reason} }

// Claims 是委派的内容：全是数据，不含密钥。
type Claims struct {
	Sub       string   `json:"sub"`                  // 应用（app id）
	Aud       string   `json:"aud"`                  // 目标出口：chat / job / broker
	Scope     []string `json:"scope"`                // 允许的注册名（"*" = 全部，慎用）
	BudgetUSD float64  `json:"budget_usd,omitempty"` // 这张票的金额上限
	IAT       int64    `json:"iat"`
	Exp       int64    `json:"exp"`
	JTI       string   `json:"jti"` // 唯一 id —— 撤销按它来
}

// MinTokenSeconds 是令牌寿命下限：太短会被时钟误差与撤销表 TTL 吃掉。
const MinTokenSeconds = 60

// Sign 签一个 JWS 紧凑令牌。kid 进 header，便于轮换。
func Sign(priv ed25519.PrivateKey, kid string, c Claims) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("私钥长度不对")
	}
	if strings.TrimSpace(c.Sub) == "" || strings.TrimSpace(c.Aud) == "" || len(c.Scope) == 0 {
		return "", fmt.Errorf("sub / aud / scope 都不能为空（没有范围就没有委派，只有通行证）")
	}
	if c.Exp <= c.IAT || c.Exp-c.IAT < MinTokenSeconds {
		return "", fmt.Errorf("exp 至少要比 iat 晚 %d 秒（太短会被时钟误差吃掉）", MinTokenSeconds)
	}
	if strings.TrimSpace(c.JTI) == "" {
		return "", fmt.Errorf("缺 jti：没有它就无法撤销")
	}
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": kid})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	input := b64(header) + "." + b64(payload)
	sig := ed25519.Sign(priv, []byte(input))
	return input + "." + b64(sig), nil
}

// Verify 验签并返回 claims。**任何异常都返回 ErrDenied**，绝不返回「将就的 claims」。
func Verify(pub ed25519.PublicKey, raw string, now time.Time) (Claims, error) {
	var c Claims
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return c, deny(ReasonMalformed)
	}
	hb, err := unb64(parts[0])
	if err != nil {
		return c, deny(ReasonMalformed)
	}
	var h struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(hb, &h); err != nil {
		return c, deny(ReasonMalformed)
	}
	// 只认 EdDSA：alg=none 之类的经典洞一律不接。
	if h.Alg != "EdDSA" {
		return c, deny(ReasonBadAlg)
	}
	sig, err := unb64(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return c, deny(ReasonBadSig)
	}
	if !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		return c, deny(ReasonBadSig)
	}
	cb, err := unb64(parts[1])
	if err != nil {
		return c, deny(ReasonMalformed)
	}
	if err := json.Unmarshal(cb, &c); err != nil {
		return c, deny(ReasonMalformed)
	}
	if c.IAT > 0 && now.Unix() < c.IAT-60 {
		return c, deny(ReasonNotYetValid)
	}
	if c.Exp > 0 && now.Unix() >= c.Exp {
		return c, deny(ReasonExpired)
	}
	return c, nil
}

// Request 是一次要授权的动作。
type Request struct {
	App     string
	Aud     string
	Model   string
	Revoked func(jti string) bool // 撤销表；nil = 没接撤销（调用方自己保证不为 nil）
}

// Decide 是**唯一的判定函数**。所有能到达模型的出口都必须走它。
func Decide(c Claims, req Request, now time.Time) (Claims, error) {
	if c.Exp > 0 && now.Unix() >= c.Exp {
		return c, deny(ReasonExpired)
	}
	if req.Aud != "" && c.Aud != req.Aud {
		return c, deny(ReasonWrongAud)
	}
	if req.App != "" && c.Sub != req.App {
		return c, deny(ReasonWrongApp)
	}
	if req.Model != "" && !inScope(c.Scope, req.Model) {
		return c, deny(ReasonOutOfScope)
	}
	if req.Revoked == nil {
		return c, deny("撤销表没接上（fail-closed：接不上就不放行）")
	}
	if req.Revoked(c.JTI) {
		return c, deny("令牌已被撤销")
	}
	return c, nil
}

func inScope(scope []string, model string) bool {
	for _, s := range scope {
		if s == "*" || s == model {
			return true
		}
	}
	return false
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func unb64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

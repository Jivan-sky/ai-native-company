package authz

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func base(now time.Time) Claims {
	return Claims{
		Sub: "invoice-sweep", Aud: "chat", Scope: []string{"qwen-max-prod"},
		BudgetUSD: 5, IAT: now.Unix(), Exp: now.Add(10 * time.Minute).Unix(), JTI: "j-1",
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv := keypair(t)
	now := time.Unix(1_800_000_000, 0)
	tok, err := Sign(priv, "k1", base(now))
	if err != nil {
		t.Fatal(err)
	}
	c, err := Verify(pub, tok, now)
	if err != nil {
		t.Fatalf("自己签的自己验不过：%v", err)
	}
	if c.Sub != "invoice-sweep" || c.Scope[0] != "qwen-max-prod" {
		t.Fatalf("claims 读错：%+v", c)
	}
}

// 该验的都在这里：任何一条漏了，都是「看起来有闸」。
func TestVerifyRejects(t *testing.T) {
	pub, priv := keypair(t)
	now := time.Unix(1_800_000_000, 0)
	tok, err := Sign(priv, "k1", base(now))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")

	// 改 payload（把 scope 放宽），签名不变
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c Claims
	_ = json.Unmarshal(raw, &c)
	c.Scope = []string{"*"}
	nb, _ := json.Marshal(c)
	forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString(nb) + "." + parts[2]

	// alg=none 的经典洞
	noneHeader := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))

	cases := map[string]struct {
		tok string
		why string
	}{
		"篡改 payload": {forged, ReasonBadSig},
		"alg=none":   {noneHeader + "." + parts[1] + ".", ReasonBadAlg},
		"少一段":        {parts[0] + "." + parts[1], ReasonMalformed},
		"乱码":         {"这不是令牌", ReasonMalformed},
		"空":          {"", ReasonMalformed},
	}
	for name, tc := range cases {
		_, err := Verify(pub, tc.tok, now)
		if err == nil || !strings.Contains(err.Error(), tc.why) {
			t.Errorf("%s：应当拒绝（%s），实际 %v", name, tc.why, err)
		}
	}

	// 过期
	if _, err := Verify(pub, tok, now.Add(11*time.Minute)); err == nil || !strings.Contains(err.Error(), ReasonExpired) {
		t.Errorf("过期必须拒：%v", err)
	}
}

// 随机串永远不许验过 —— 判定器是攻击面，这条用随机样本兜底。
func TestVerifyNeverAcceptsGarbage(t *testing.T) {
	pub, _ := keypair(t)
	now := time.Now()
	for i := 0; i < 500; i++ {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		if _, err := Verify(pub, base64.RawURLEncoding.EncodeToString(b), now); err == nil {
			t.Fatalf("随机串被验过了：%q", b)
		}
	}
}

func TestDecide(t *testing.T) {
	pub, priv := keypair(t)
	now := time.Unix(1_800_000_000, 0)
	tok, _ := Sign(priv, "k1", base(now))
	c, err := Verify(pub, tok, now)
	if err != nil {
		t.Fatal(err)
	}
	notRevoked := func(string) bool { return false }
	revoked := func(string) bool { return true }

	if _, err := Decide(c, Request{App: "invoice-sweep", Aud: "chat", Model: "qwen-max-prod", Revoked: notRevoked}, now); err != nil {
		t.Fatalf("合法请求应当放行：%v", err)
	}
	cases := map[string]struct {
		req Request
		why string
	}{
		"应用不对":   {Request{App: "other", Aud: "chat", Model: "qwen-max-prod", Revoked: notRevoked}, ReasonWrongApp},
		"出口不对":   {Request{App: "invoice-sweep", Aud: "job", Model: "qwen-max-prod", Revoked: notRevoked}, ReasonWrongAud},
		"注册名超范围": {Request{App: "invoice-sweep", Aud: "chat", Model: "qwen-turbo-dev", Revoked: notRevoked}, ReasonOutOfScope},
		"已撤销":    {Request{App: "invoice-sweep", Aud: "chat", Model: "qwen-max-prod", Revoked: revoked}, "已被撤销"},
		"撤销表没接上": {Request{App: "invoice-sweep", Aud: "chat", Model: "qwen-max-prod", Revoked: nil}, "撤销表没接上"},
	}
	for name, tc := range cases {
		if _, err := Decide(c, tc.req, now); err == nil || !strings.Contains(err.Error(), tc.why) {
			t.Errorf("%s：应当拒绝（%s），实际 %v", name, tc.why, err)
		}
	}
	// 过期后即使撤销表正常也不放行
	if _, err := Decide(c, Request{App: "invoice-sweep", Aud: "chat", Model: "qwen-max-prod", Revoked: notRevoked}, now.Add(time.Hour)); err == nil {
		t.Error("过期必须拒")
	}
}

package authz

import (
	"crypto/ed25519"
	"crypto/rand"
)

// GenerateKey 生成一对 Ed25519 密钥（独立出来便于测试与轮换）。
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	return pub, priv, err
}

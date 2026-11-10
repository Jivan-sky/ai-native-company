package authz

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestRateLimitWindow(t *testing.T) {
	rl := RateLimits{Path: filepath.Join(t.TempDir(), "rl.json")}
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < 3; i++ {
		ok, _, err := rl.Allow("m", 3, now)
		if err != nil || !ok {
			t.Fatalf("第 %d 次应当放行：ok=%v err=%v", i+1, ok, err)
		}
	}
	ok, wait, _ := rl.Allow("m", 3, now)
	if ok {
		t.Fatal("第 4 次应当被限流")
	}
	if wait <= 0 {
		t.Fatalf("被限流要给等待时间，实际 %v", wait)
	}
	// 换一分钟：重新计数
	ok, _, _ = rl.Allow("m", 3, now.Add(61*time.Second))
	if !ok {
		t.Fatal("跨窗口应当重新放行")
	}
	// 不同注册名互不影响
	ok, _, _ = rl.Allow("other", 3, now.Add(61*time.Second))
	if !ok {
		t.Fatal("另一个注册名不该被牵连")
	}
}

// 撤销生效时延：从写进撤销表到「下一次判定被拒」要多久。
// 这是「撤销后最坏多久生效」的上界（读表 + 判定），不是等一次轮询。
func TestRevocationLatency(t *testing.T) {
	dir := t.TempDir()
	r := Revocations{Path: filepath.Join(dir, "revoked.json")}
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var lat []time.Duration
	for i := 0; i < 50; i++ {
		jti := fmt.Sprintf("j-%d", i)
		tok, err := Sign(priv, "k1", Claims{
			Sub: "a", Aud: "chat", Scope: []string{"m"}, BudgetUSD: 1,
			IAT: now.Unix(), Exp: now.Add(10 * time.Minute).Unix(), JTI: jti,
		})
		if err != nil {
			t.Fatal(err)
		}
		c, err := Verify(pub, tok, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decide(c, Request{App: "a", Aud: "chat", Model: "m", Revoked: r.Revoked}, now); err != nil {
			t.Fatalf("撤销前应当放行：%v", err)
		}

		start := time.Now()
		if err := r.Revoke(jti, now.Add(10*time.Minute)); err != nil {
			t.Fatal(err)
		}
		c2, _ := Verify(pub, tok, now)
		_, derr := Decide(c2, Request{App: "a", Aud: "chat", Model: "m", Revoked: r.Revoked}, now)
		lat = append(lat, time.Since(start))
		if derr == nil {
			t.Fatal("撤销后必须被拒")
		}
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	p50 := lat[len(lat)/2]
	p99 := lat[len(lat)*99/100]
	t.Logf("撤销生效时延：p50=%v p99=%v（%d 次）", p50, p99, len(lat))
	if p99 > 200*time.Millisecond {
		t.Fatalf("p99 太慢：%v", p99)
	}
}

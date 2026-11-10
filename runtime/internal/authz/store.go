package authz

// 两个小存储：撤销表与预算账。都是**文件 + 文件锁**，不是内存 map。
//
// 为什么不用内存：`anc board` 与 `anc chat` 是两个进程，重启即丢；而且
// 「预算 1 元、10 个并发只许成功 1 个」这种性质，内存 map 在**跨进程**下根本不成立。
// 文件 + flock 是这台机器上最小可信的实现；换成 Redis/DB 时接口不变。
//
// 两条口径：
//  1. **读-改-写必须在锁里**：TryReserve 是判断 + 扣款，分开做就会超卖；
//  2. **失败就是拒绝**：文件读坏了/锁拿不到 → 返回错误，调用方必须 deny（fail-closed）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// fileStore 是带排他锁的 JSON 文件。
type fileStore struct{ Path string }

func (s fileStore) withLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	release, err := lockPath(s.Path + ".lock")
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

func (s fileStore) read(dst any) error {
	b, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, dst)
}

func (s fileStore) write(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Revocations 是撤销表：jti → 该令牌原本到期的时间。
// 过了到期时间的条目可以丢 —— 令牌自己已经失效，留着只会无限长。
type Revocations struct{ Path string }

func (r Revocations) Revoked(jti string) bool {
	ok := false
	err := fileStore{r.Path}.withLock(func() error {
		m := map[string]int64{}
		if err := (fileStore{r.Path}).read(&m); err != nil {
			return err
		}
		_, ok = m[jti]
		return nil
	})
	if err != nil {
		return true // 读不到 = 当它撤了（fail-closed）
	}
	return ok
}

func (r Revocations) Revoke(jti string, exp time.Time) error {
	return fileStore{r.Path}.withLock(func() error {
		m := map[string]int64{}
		if err := (fileStore{r.Path}).read(&m); err != nil {
			return err
		}
		m[jti] = exp.Unix()
		// 顺手清掉已过期的
		now := time.Now().Unix()
		for k, e := range m {
			if e < now {
				delete(m, k)
			}
		}
		return (fileStore{r.Path}).write(m)
	})
}

// Budgets 是每张票的已花金额。金额单位与 claims.budget_usd 一致（美元）。
//
// 预留而不是事后扣：并发下「剩 0.02 元」必须只有一个请求能过 ——
// 先扣再跑，跑完按实际结算（多退少补）。
type Budgets struct {
	Path    string
	Reserve float64 // 每次调用先扣多少；<=0 用 0.02
}

func (b Budgets) reserve() float64 {
	if b.Reserve <= 0 {
		return 0.02
	}
	return b.Reserve
}

// TryReserve 在锁里做「读已花 → 判断剩余 → 记账」。返回是否放行与当前已花。
func (b Budgets) TryReserve(jti string, budget float64) (bool, float64, error) {
	if budget <= 0 {
		return false, 0, fmt.Errorf("令牌没有预算（budget_usd <= 0）—— 没有上限的调用不算授权")
	}
	amt := b.reserve()
	ok := false
	spent := 0.0
	err := fileStore{b.Path}.withLock(func() error {
		m := map[string]float64{}
		if err := (fileStore{b.Path}).read(&m); err != nil {
			return err
		}
		spent = m[jti]
		if spent+amt > budget+1e-9 {
			return nil // 不够：不放行，也不改账
		}
		m[jti] = spent + amt
		spent = m[jti]
		ok = true
		return (fileStore{b.Path}).write(m)
	})
	if err != nil {
		return false, spent, err
	}
	return ok, spent, nil
}

// Settle 用实际金额替换那次预留（多退少补）。actual<=0 表示「不知道实际花费」，
// 那就保留预留不动 —— 不知道的时候不要假装知道。
func (b Budgets) Settle(jti string, reserved, actual float64) error {
	if actual <= 0 {
		return nil
	}
	return fileStore{b.Path}.withLock(func() error {
		m := map[string]float64{}
		if err := (fileStore{b.Path}).read(&m); err != nil {
			return err
		}
		next := m[jti] - reserved + actual
		if next < 0 {
			next = 0
		}
		m[jti] = next
		return (fileStore{b.Path}).write(m)
	})
}

// Spent 读某张票已花多少（测试与看板用）。
func (b Budgets) Spent(jti string) float64 {
	var v float64
	_ = fileStore{b.Path}.withLock(func() error {
		m := map[string]float64{}
		if err := (fileStore{b.Path}).read(&m); err != nil {
			return err
		}
		v = m[jti]
		return nil
	})
	return v
}

// Keys 管理签发用的密钥对：第一次用时生成，之后复用。
type Keys struct{ Dir string }

// Load 读（或生成）签发密钥。返回公钥与私钥。
func (k Keys) Load() (pub []byte, priv []byte, err error) {
	if err := os.MkdirAll(k.Dir, 0o700); err != nil {
		return nil, nil, err
	}
	privPath := filepath.Join(k.Dir, "ed25519.key")
	pubPath := filepath.Join(k.Dir, "ed25519.pub")
	if b, err := os.ReadFile(privPath); err == nil {
		p, err := os.ReadFile(pubPath)
		if err != nil {
			return nil, nil, fmt.Errorf("有私钥没公钥：%v", err)
		}
		return p, b, nil
	}
	pub2, priv2, err := GenerateKey()
	if err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(privPath, priv2, 0o600); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(pubPath, pub2, 0o644); err != nil {
		return nil, nil, err
	}
	return pub2, priv2, nil
}

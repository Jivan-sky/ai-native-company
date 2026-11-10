package authz

// 限流：按注册名（= 厂商配额）做固定窗口。
//
// 为什么按注册名而不是按票：`limits.rpm` 是厂商给的**那个端点**的配额，
// 所有应用共享它 —— 按票限流会让「多签几张票」绕过厂商配额。
//
// 仍然是文件 + 锁：与预算同一条口径（单机可信、换 Redis 时接口不变）。
// 窗口是固定窗口而不是令牌桶：先要能对账（「这一分钟用了几次」），顺滑留给以后。

import (
	"path/filepath"
	"time"
)

type window struct {
	Start int64 `json:"start"`
	Count int   `json:"count"`
}

// RateLimits 是「每个注册名一分钟内已经用了几次」。
type RateLimits struct{ Path string }

// Allow 判断这一次是否放行，并返回需要等待多久（不放行时）。
func (r RateLimits) Allow(key string, rpm int, now time.Time) (bool, time.Duration, error) {
	if rpm <= 0 {
		return true, 0, nil // 没配额 = 不限（配额为 0 的条目在注册表校验里已经过不去）
	}
	if r.Path == "" {
		r.Path = filepath.Join(".", "ratelimit.json")
	}
	ok := false
	var wait time.Duration
	err := fileStore{r.Path}.withLock(func() error {
		m := map[string]window{}
		if err := (fileStore{r.Path}).read(&m); err != nil {
			return err
		}
		w := m[key]
		if now.Unix()-w.Start >= 60 {
			w = window{Start: now.Unix(), Count: 0}
		}
		if w.Count >= rpm {
			wait = time.Duration(w.Start+60-now.Unix()) * time.Second
			if wait < time.Second {
				wait = time.Second
			}
			m[key] = w
			return (fileStore{r.Path}).write(m)
		}
		w.Count++
		m[key] = w
		ok = true
		return (fileStore{r.Path}).write(m)
	})
	if err != nil {
		return false, 0, err
	}
	return ok, wait, nil
}

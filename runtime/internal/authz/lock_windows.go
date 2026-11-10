//go:build windows

package authz

// Windows：没有 flock（Go 标准库里的 syscall.Flock 只在 Unix）。
// 用 O_EXCL 建锁文件做跨进程互斥；进程异常退出会留下锁文件，所以超过 30 秒的锁视为陈旧并清掉。
// 这是**权宜**实现（Windows 腿本来就还没验过），单机语义够用；换 Redis/DB 时这两份都不用改接口。

import (
	"fmt"
	"os"
	"time"
)

func lockPath(path string) (func(), error) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if st, statErr := os.Stat(path); statErr == nil && time.Since(st.ModTime()) > 30*time.Second {
			_ = os.Remove(path)
			continue
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, fmt.Errorf("拿不到锁：%s", path)
}

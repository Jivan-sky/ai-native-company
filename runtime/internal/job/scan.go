package job

import "fmt"

// fmtSscan 是 fmt.Sscanf 的薄封装：这里只需要「两位数字能不能读成整数」。
func fmtSscan(s string, out *int) (int, error) { return fmt.Sscanf(s, "%d", out) }

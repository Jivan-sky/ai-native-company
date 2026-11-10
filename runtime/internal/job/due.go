package job

// 「现在该不该跑」—— 调度唯一需要判断的事，所以单独一个纯函数，单测能喂任意时刻。

import (
	"strings"
	"time"
)

// Due 返回「现在该不该跑」与「下一次该跑的时刻」。
//
// 口径（先写死三条）：
//  1. **从没跑过就立刻跑**（新装的作业不该等一个周期）；
//  2. 只有 every → 上次成功之后满 every 就该跑；
//  3. every + at → 每天 at 点跑一次；**错过就补**（停机到第二天，tick 一上来就补当天的），
//     也就是说它答的是「今天这个点过了、而我没跑过」。
//
// 「失败也推进 next」不成立：失败时 LastOK=false，Due 仍会把它当「没跑过」——
// 否则一次偶发失败会让这个作业从此停摆。
func Due(s Spec, st *State, now time.Time) (bool, time.Time) {
	every := 24 * time.Hour
	if s.Schedule.Every != "" {
		if d, err := time.ParseDuration(s.Schedule.Every); err == nil && d > 0 {
			every = d
		}
	}
	loc := time.Local
	if tz := strings.TrimSpace(s.Schedule.TZ); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	n := now.In(loc)

	var last time.Time
	if st != nil && st.LastOK && !st.LastAt.IsZero() {
		last = st.LastAt.In(loc)
	}
	if last.IsZero() {
		return true, n
	}

	if at := strings.TrimSpace(s.Schedule.At); at != "" {
		if hh, mm, ok := parseHHMM(at); ok {
			next := time.Date(n.Year(), n.Month(), n.Day(), hh, mm, 0, 0, loc)
			if !next.After(last) {
				next = next.AddDate(0, 0, 1)
			}
			return !n.Before(next), next
		}
	}
	next := last.Add(every)
	return !n.Before(next), next
}

func parseHHMM(s string) (int, int, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	var hh, mm int
	for _, p := range parts {
		if len(p) != 2 {
			return 0, 0, false
		}
	}
	_, err1 := fmtSscan(parts[0], &hh)
	_, err2 := fmtSscan(parts[1], &mm)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

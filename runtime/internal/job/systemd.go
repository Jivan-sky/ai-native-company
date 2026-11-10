package job

// systemd 那一层：Linux 上把「每 N 分钟 tick 一次」做成 oneshot service + timer。
//
// 与 launchd 那边同一件事、同一套口径：**常驻交给系统**，anc 只在被叫醒时跑一下。
// 差别只在装在哪：
//   - root（容器 / 服务器）→ 系统级 `/etc/systemd/system`，`systemctl enable --now`
//   - 普通用户 → `~/.config/systemd/user`，`systemctl --user enable --now`
//
// 为什么 timer 而不是 cron：timer 能 `OnBootSec`（开机后多久第一次跑）与 `OnUnitActiveSec`
// （上一轮结束后再等多久），这两个正好对应我们要的「重启后自己回来」与「隔多久跑一次」。

import (
	"fmt"
	"strings"
)

// BuildSystemdUnits 生成 (service, timer) 两份 unit 文本。
func BuildSystemdUnits(label, binPath, vault, jobsDir, secretsFile string, everySeconds int) (string, string) {
	args := []string{binPath, "job", "tick", vault, "--dir", jobsDir}
	if strings.TrimSpace(secretsFile) != "" {
		args = append(args, "--secrets", secretsFile)
	}
	var svc strings.Builder
	svc.WriteString("[Unit]\n")
	fmt.Fprintf(&svc, "Description=ANC jobs tick (%s)\n", vault)
	svc.WriteString("Wants=network-online.target\nAfter=network-online.target\n\n")
	svc.WriteString("[Service]\nType=oneshot\n")
	fmt.Fprintf(&svc, "ExecStart=%s\n", strings.Join(args, " "))
	svc.WriteString("WorkingDirectory=/\n")

	var tm strings.Builder
	tm.WriteString("[Unit]\n")
	fmt.Fprintf(&tm, "Description=ANC jobs tick timer (%s)\n", vault)
	tm.WriteString("\n[Timer]\n")
	// 开机 20 秒后先跑一次（重启后「自己回来」靠这一条），之后每 N 秒一次。
	tm.WriteString("OnBootSec=20s\n")
	fmt.Fprintf(&tm, "OnUnitActiveSec=%ds\n", everySeconds)
	tm.WriteString("AccuracySec=1s\nUnit=")
	tm.WriteString(label)
	tm.WriteString(".service\n\n[Install]\nWantedBy=timers.target\n")
	return svc.String(), tm.String()
}

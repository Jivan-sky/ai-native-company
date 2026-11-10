package job

// launchd 那一层：生成 plist / 调 launchctl。
//
// 为什么 tick 走 launchd 而不是 ANC 自己常驻：D4 已经拍过「常驻交给上游 daemon」，
// ANC 不该再养一个常驻进程。launchd 每 N 分钟叫醒一次 `anc job tick`，跑完就退 ——
// 进程模型最简单，且**重启后由系统负责拉起来**（这正是要证明的那一条）。

import (
	"fmt"
	"os/exec"
	"strings"
)

// BuildPlist 生成用户级 LaunchAgent 的 plist。抽成函数是为了单测能验内容。
func BuildPlist(label, binPath, vault, jobsDir, secretsFile string, everySeconds int) string {
	args := []string{binPath, "job", "tick", vault, "--dir", jobsDir}
	if strings.TrimSpace(secretsFile) != "" {
		args = append(args, "--secrets", secretsFile)
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>`)
	b.WriteString(label)
	b.WriteString(`</string>
  <key>ProgramArguments</key>
  <array>
`)
	for _, a := range args {
		b.WriteString("    <string>")
		b.WriteString(xmlEscape(a))
		b.WriteString("</string>\n")
	}
	b.WriteString(`  </array>
  <key>StartInterval</key><integer>`)
	fmt.Fprintf(&b, "%d", everySeconds)
	b.WriteString(`</integer>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>/tmp/`)
	b.WriteString(label)
	b.WriteString(`.out.log</string>
  <key>StandardErrorPath</key><string>/tmp/`)
	b.WriteString(label)
	b.WriteString(`.err.log</string>
</dict>
</plist>
`)
	return b.String()
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// RunLaunchctl 跑一条 launchctl 命令（供 CLI 层调用）。
func RunLaunchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v：%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RunCmd 跑一条外部命令（launchctl / systemctl 共用）。
func RunCmd(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v：%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

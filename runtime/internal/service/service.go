// Package service 生成并装载「用户级」服务定义：一个 bot 一套。
//
// 为什么是用户级而不是系统级 daemon：DESIGN.md §1 —— 隔离靠 OS 账号，
// 谁装就是谁的账号（macOS LaunchAgent / systemd --user / Windows 计划任务）。
// 所以单元里**不写 UserName**：账号由「以哪个账号执行 install」决定，
// D1（严格一人一 OS 账号 vs 共享账号 + 独立进程）没拍板也不影响这份单元。
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ExpandHome 把开头的 `~/` 换成当前用户的家目录。
// Render 保持纯函数（不碰环境），环境访问只发生在这里 —— 可测性即正确性。
// 注意：它用的是**当前**机器的家目录，所以只在「本机装载/查询」时有意义；
// 跨平台只 render 不 install 时不要去展开它。
func ExpandHome(p string) string {
	if !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}

// Spec 渲染一份服务定义所需要的全部输入。
type Spec struct {
	CompanyID string // 进 label / 任务名 / 单元名
	Bot       string // 成员名
	AncBin    string // anc 可执行文件绝对路径
	WorkDir   string // 这个 bot 的 cwd
	LogDir    string // 日志落点（bot 的 state/ 下）
	Account   string // 只有 Windows 计划任务用；其余平台 = 谁装就是谁
	GOOS      string // 空 = 本机
	Version   string
	Now       time.Time
}

// Artifact 是一份服务定义的产物与它的装卸命令。
type Artifact struct {
	Kind      string // launchd / systemd / schtasks
	GOOS      string
	Path      string // 建议落点（用户级目录）
	Content   string
	Install   []string // 装载命令（argv）
	Uninstall []string
	Status    []string // 只读查询：装没装、跑没跑
	Notes     []string // 必须一并打给人看的注意事项
}

// InputsHash 是这份服务定义的输入指纹（不含时间），用于漂移比对。
func (s Spec) InputsHash() string {
	var b strings.Builder
	b.WriteString("svc-v1\n")
	fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s|%s\n", s.GOOS, s.CompanyID, s.Bot, s.AncBin, s.WorkDir, s.LogDir, s.Account)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Fingerprint 从已有单元里读回 anc 指纹（三种格式都带这一行注释）。
func Fingerprint(content string) (version, inputs, at string, ok bool) {
	for _, line := range strings.Split(content, "\n") {
		i := strings.Index(line, "anc:generated ")
		if i < 0 {
			continue
		}
		for _, f := range strings.Fields(line[i+len("anc:generated "):]) {
			k, v, found := strings.Cut(f, "=")
			if !found {
				continue
			}
			switch k {
			case "v":
				version = v
			case "inputs":
				inputs = v
			case "at":
				at = v
			}
		}
		return version, inputs, at, true
	}
	return "", "", "", false
}

// Label 是这个 bot 在服务管理器里的名字。
func (s Spec) Label() string { return "com.anc." + s.CompanyID + "." + s.Bot }

// UnitName 是 systemd 单元名。
func (s Spec) UnitName() string { return "anc-" + s.CompanyID + "-" + s.Bot }

// TaskName 是 Windows 计划任务名（带目录，便于整组删除）。
func (s Spec) TaskName() string { return `ANC\` + s.CompanyID + `\` + s.Bot }

// Render 按平台生成单元 + 装卸命令。纯函数：不碰磁盘、不看环境。
func Render(s Spec) (Artifact, error) {
	if s.GOOS == "" {
		s.GOOS = runtime.GOOS
	}
	if s.CompanyID == "" || s.Bot == "" {
		return Artifact{}, fmt.Errorf("service: companyID 与 bot 都不能为空")
	}
	if s.AncBin == "" {
		return Artifact{}, fmt.Errorf("service: ancBin 不能为空（写相对路径会在装卸后找不到自己）")
	}
	head := fmt.Sprintf("anc:generated v=%s inputs=%s at=%s —— 本文件由 `anc service` 生成，手改视为事故",
		s.Version, s.InputsHash(), s.Now.UTC().Format(time.RFC3339))

	var a Artifact
	switch s.GOOS {
	case "darwin":
		a = renderLaunchd(s, head)
	case "linux":
		a = renderSystemd(s, head)
	case "windows":
		a = renderTask(s, head)
	default:
		return Artifact{}, fmt.Errorf("service: 不支持的平台 %q（只有 darwin / linux / windows）", s.GOOS)
	}
	a.GOOS = s.GOOS
	a.Notes = append(a.Notes,
		"`anc serve` 尚未实现（阶段 C/D）——现在装载这份单元，只会得到一个反复退出的服务；先 render 出来核对，别急着 install。")
	return a, nil
}

func renderLaunchd(s Spec, head string) Artifact {
	args := []string{s.AncBin, "serve", "--bot", s.Bot}
	var arr strings.Builder
	for _, a := range args {
		fmt.Fprintf(&arr, "    <string>%s</string>\n", xmlEscape(a))
	}
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- %s -->
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
%s  </array>
  <key>WorkingDirectory</key><string>%s</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, head, xmlEscape(s.Label()), arr.String(), xmlEscape(s.WorkDir),
		xmlEscape(join(s.LogDir, "serve.log")), xmlEscape(join(s.LogDir, "serve.err.log")))

	return Artifact{
		Kind:    "launchd",
		Path:    join("~/Library/LaunchAgents", s.Label()+".plist"),
		Content: content,
		Install: []string{"launchctl", "load", "-w", "~/Library/LaunchAgents/" + s.Label() + ".plist"},
		Uninstall: []string{"launchctl", "unload", "-w",
			"~/Library/LaunchAgents/" + s.Label() + ".plist"},
		Status: []string{"launchctl", "list", s.Label()},
		Notes: []string{
			"用户级 LaunchAgent：以「安装它的那个账号」运行，换账号就要在那个账号下再装一次。",
			"GUI 登录后才会拉起；重启后若没人登录，bot 不会自己回来（见 DESIGN §0-D2，未拍板）。",
		},
	}
}

func renderSystemd(s Spec, head string) Artifact {
	unit := s.UnitName() + ".service"
	content := fmt.Sprintf(`# %s
[Unit]
Description=ANC bot %s-%s
After=network-online.target

[Service]
Type=simple
ExecStart=%s
WorkingDirectory=%s
Restart=on-failure
RestartSec=10
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, head, s.CompanyID, s.Bot,
		systemdJoin([]string{s.AncBin, "serve", "--bot", s.Bot}),
		systemdQuote(s.WorkDir),
		// systemd 把 `append:` 之后当同一个值，带空格的路径要连前缀一起引起来。
		systemdQuote("append:"+join(s.LogDir, "serve.log")),
		systemdQuote("append:"+join(s.LogDir, "serve.err.log")))

	return Artifact{
		Kind:      "systemd",
		Path:      join("~/.config/systemd/user", unit),
		Content:   content,
		Install:   []string{"systemctl", "--user", "enable", "--now", unit},
		Uninstall: []string{"systemctl", "--user", "disable", "--now", unit},
		Status:    []string{"systemctl", "--user", "status", unit},
		Notes: []string{
			"systemd --user：默认随登录会话，退出登录就停。要无人值守常驻通常还要开 linger（loginctl enable-linger）——本机未实测。",
			"ExecStart 里的路径带空格会被 systemd 拆开，所以这里做了引号包裹；换路径后请重新 render 而不是手改。",
		},
	}
}

func renderTask(s Spec, head string) Artifact {
	acct := s.Account
	acctNote := "UserId 用 --account 指定；不指定就填占位符，装载前必须换成本机真实账号"
	if acct == "" {
		acct = `请填运行账号`
	}
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!-- %s -->
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>ANC bot %s-%s</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%s</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>serve --bot %s</Arguments>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`, head, xmlEscape(s.CompanyID), xmlEscape(s.Bot),
		xmlEscape(acct), xmlEscape(acct), xmlEscape(s.AncBin), xmlEscape(s.Bot), xmlEscape(s.WorkDir))

	return Artifact{
		Kind:      "schtasks",
		Path:      join("~/anc/tasks", s.CompanyID+"-"+s.Bot+".task.xml"),
		Content:   content,
		Install:   []string{"schtasks", "/Create", "/TN", s.TaskName(), "/XML", "~/anc/tasks/" + s.CompanyID + "-" + s.Bot + ".task.xml", "/F"},
		Uninstall: []string{"schtasks", "/Delete", "/TN", s.TaskName(), "/F"},
		Status:    []string{"schtasks", "/Query", "/TN", s.TaskName(), "/V", "/FO", "LIST"},
		Notes: []string{
			acctNote + "。",
			"ExecutionTimeLimit=PT0S 是故意的：计划任务默认 72 小时后强杀长驻进程。",
			"schtasks /XML 的编码（UTF-8 vs UTF-16）本机未实测，装之前先用 /? 或拿一份样本在目标机上试一次。",
		},
	}
}

// ---------- 小工具 ----------

func join(dir, name string) string {
	if dir == "" {
		return name
	}
	return strings.TrimRight(dir, "/\\") + "/" + name
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// systemdJoin 把 argv 拼成 ExecStart 一行：带空格或特殊字符的参数加双引号。
// systemd 自己的引号规则不认反斜杠转义，只认成对引号，所以内部引号要拒绝而不是转义。
func systemdJoin(argv []string) string {
	out := make([]string, 0, len(argv))
	for _, a := range argv {
		out = append(out, systemdQuote(a))
	}
	return strings.Join(out, " ")
}

func systemdQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\%$") {
		return s
	}
	return `"` + strings.NewReplacer(`"`, ``, `\`, `/`).Replace(s) + `"`
}

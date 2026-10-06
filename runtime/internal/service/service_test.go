package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// spec 造一个输入；测试里改哪个字段就动哪个。
func spec(goos string) Spec {
	return Spec{
		CompanyID: "demo",
		Bot:       "alice",
		AncBin:    "/opt/anc/bin/anc",
		WorkDir:   "/Users/anc-alice/bot",
		LogDir:    "/Users/anc-alice/state",
		Account:   `MACHINE\anc-alice`,
		GOOS:      goos,
		Version:   "test",
		Now:       fixedNow,
	}
}

func TestEveryPlatformCarriesFingerprint(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			a, err := Render(spec(goos))
			if err != nil {
				t.Fatal(err)
			}
			ver, inputs, at, ok := Fingerprint(a.Content)
			if !ok {
				t.Fatal("单元里没有 anc 指纹 —— 就没法判断它是不是我们的产物、改没改过")
			}
			if ver != "test" || inputs != spec(goos).InputsHash() || at == "" {
				t.Fatalf("指纹读回不符：v=%q inputs=%q at=%q", ver, inputs, at)
			}
			if a.Path == "" || len(a.Install) == 0 || len(a.Status) == 0 {
				t.Fatalf("缺路径或装卸命令：%+v", a)
			}
			if !startsTheBot(a.Content, a.Kind) {
				t.Fatalf("单元里没有启动命令：\n%s", a.Content)
			}
		})
	}
}

// startsTheBot 判断单元里确实带了「anc serve --bot <成员>」。
// launchd 的 ProgramArguments 是一个参数一行，不能按整串找。
func startsTheBot(content, kind string) bool {
	if kind == "launchd" {
		return strings.Count(content, "<string>serve</string>") == 1 &&
			strings.Count(content, "<string>--bot</string>") == 1 &&
			strings.Count(content, "<string>alice</string>") == 1
	}
	return strings.Count(content, "serve --bot alice") == 1
}

// 启动命令只能出现一次（曾经拼成 `ExecStart=... serve --bot x serve --bot x`）。
func TestStartCommandAppearsOnce(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			a, err := Render(spec(goos))
			if err != nil {
				t.Fatal(err)
			}
			if !startsTheBot(a.Content, a.Kind) {
				t.Fatalf("启动命令不是恰好一次：\n%s", a.Content)
			}
		})
	}
}

func TestLaunchdShape(t *testing.T) {
	a, err := Render(spec("darwin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<key>Label</key><string>com.anc.demo.alice</string>",
		"<key>RunAtLoad</key><true/>",
		"<key>KeepAlive</key><true/>",
		"<key>WorkingDirectory</key><string>/Users/anc-alice/bot</string>",
		"<string>--bot</string>",
	} {
		if !strings.Contains(a.Content, want) {
			t.Fatalf("launchd 单元里缺 %q", want)
		}
	}
	if filepath.ToSlash(a.Path) != "~/Library/LaunchAgents/com.anc.demo.alice.plist" {
		t.Fatalf("落点不对：%s", a.Path)
	}
	// 单元里不许写账号：谁装就是谁（D1 没拍板也不影响这份单元）。
	if strings.Contains(a.Content, "UserName") {
		t.Fatal("用户级 LaunchAgent 不该带 UserName")
	}
}

func TestSystemdShape(t *testing.T) {
	a, err := Render(spec("linux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[Unit]", "[Service]", "[Install]", "WantedBy=default.target", "Restart=on-failure"} {
		if !strings.Contains(a.Content, want) {
			t.Fatalf("systemd 单元里缺 %q", want)
		}
	}
	if !strings.Contains(a.Content, "ExecStart=/opt/anc/bin/anc serve --bot alice") {
		t.Fatal("ExecStart 不对：路径没空格时不该加引号")
	}
}

// 路径带空格：systemd 的引号规则不认反斜杠转义，只能整体加引号。
func TestSystemdQuotesSpacedPaths(t *testing.T) {
	s := spec("linux")
	s.AncBin = "/opt/anc dir/anc"
	s.WorkDir = "/Users/anc alice/bot"
	a, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.Content, `ExecStart="/opt/anc dir/anc" serve --bot alice`) {
		t.Fatalf("带空格的路径没被整段引起来：\n%s", a.Content)
	}
	if !strings.Contains(a.Content, `WorkingDirectory="/Users/anc alice/bot"`) {
		t.Fatal("WorkingDirectory 的引号不对")
	}
	if strings.Contains(a.Content, `\"`) {
		t.Fatal("systemd 不认反斜杠转义，出现了不该有的转义")
	}
}

func TestWindowsTaskShape(t *testing.T) {
	a, err := Render(spec("windows"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<UserId>MACHINE\anc-alice</UserId>`,
		"<LogonTrigger>",
		"<RestartOnFailure>",
		// 默认 72 小时强杀长驻进程，必须显式关掉
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		"<RunLevel>LeastPrivilege</RunLevel>",
	} {
		if !strings.Contains(a.Content, want) {
			t.Fatalf("计划任务里缺 %q", want)
		}
	}
	if len(a.Install) == 0 || !strings.HasPrefix(strings.Join(a.Install, " "), "schtasks /Create") {
		t.Fatalf("装载命令不对：%v", a.Install)
	}
}

func TestXMLAndQuoteEscaping(t *testing.T) {
	s := spec("darwin")
	s.WorkDir = `/Users/a&b<c>/"bot"`
	a, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.Content, "a&b<c>") || strings.Contains(a.Content, `/"bot"`) {
		t.Fatal("XML 特殊字符没转义，plist 会解析失败")
	}
	if !strings.Contains(a.Content, "a&amp;b&lt;c&gt;") {
		t.Fatalf("转义结果不对：\n%s", a.Content)
	}
}

func TestRenderRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Spec)
	}{
		{"平台不支持", func(s *Spec) { s.GOOS = "plan9" }},
		{"没给可执行路径", func(s *Spec) { s.AncBin = "" }},
		{"没给 bot", func(s *Spec) { s.Bot = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := spec("linux")
			c.mut(&s)
			if _, err := Render(s); err == nil {
				t.Fatal("应当报错")
			}
		})
	}
}

// 空账号时 Windows 单元要留一个显然的占位符，不能悄悄填成空值。
func TestWindowsWithoutAccountLeavesObviousPlaceholder(t *testing.T) {
	s := spec("windows")
	s.Account = ""
	a, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.Content, "请填运行账号") {
		t.Fatal("没给 --account 时应当留占位符")
	}
}

func TestExpandHome(t *testing.T) {
	if got := ExpandHome("/abs/path"); got != "/abs/path" {
		t.Fatalf("绝对路径不该被动：%s", got)
	}
	got := ExpandHome("~/Library/x.plist")
	want := filepath.Join(mustHome(t), "Library", "x.plist")
	if got != want {
		t.Fatalf("展开结果不对：%s", got)
	}
}

func mustHome(t *testing.T) string {
	t.Helper()
	h, err := os.UserHomeDir()
	if err != nil {
		t.Skip("这台机器没有家目录概念")
	}
	return h
}

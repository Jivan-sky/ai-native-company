package gateway

import (
	"path/filepath"
	"strings"
	"testing"
)

// 表里每个字段都得填：漏一个，调用方就会退回到「自己拼一份」——
// 那正是这次抽象要消灭的东西。
func TestSpecHasNoEmptyFields(t *testing.T) {
	s := CCConnect
	if s.Name == "" || s.ConfigFile == "" || s.DefaultDisplay == "" ||
		s.SecretKeyPrefix == "" || s.BinSubdir == "" || s.HomeDirName == "" ||
		s.DaemonManifestName == "" || s.LogSubdir == "" || s.LogFileName == "" ||
		s.ServiceName == "" || s.SocketRel == "" || s.SessionsRel == "" {
		t.Errorf("CCConnect 有字段没填：%+v", s)
	}
	if len(s.BinNames) == 0 || len(s.DisplayModes) == 0 || len(s.InstallCmd) == 0 || len(s.RestartCmd) == 0 {
		t.Errorf("CCConnect 有列表是空的：%+v", s)
	}
}

// 出厂默认那个值必须在合法清单里 —— 否则「不写这一段」就是个非法配置。
func TestDefaultDisplayIsInModes(t *testing.T) {
	if !CCConnect.ValidDisplayMode(CCConnect.DefaultDisplay) {
		t.Errorf("默认档 %q 不在合法清单 %v 里", CCConnect.DefaultDisplay, CCConnect.DisplayModes)
	}
}

// 路径拼装：G3 / G4 的四条落点。
func TestPathHelpers(t *testing.T) {
	s := CCConnect
	home := filepath.Join("C:", "Users", "x")
	data := filepath.Join("D:", "data")

	cases := []struct{ got, want string }{
		{s.HomeDir(home), filepath.Join(home, ".cc-connect")},
		{s.ManifestPath(home), filepath.Join(home, ".cc-connect", "daemon.json")},
		{s.DefaultLogFile(home), filepath.Join(home, ".cc-connect", "logs", "cc-connect.log")},
		{s.BinDir(home), filepath.Join(home, ".anc", "bin")},
		{s.SocketPath(data), filepath.Join(data, "run", "api.sock")},
		{s.SessionsDir(data), filepath.Join(data, "sessions")},
		{s.ServiceUnitName(), "cc-connect.service"},
		{s.LoaderScriptName(), "cc-connect-daemon.ps1"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("落点拼错了：got %q want %q", c.got, c.want)
		}
	}
}

// Windows 上取 .exe、Linux 上取无后缀：候选列表两个都给，由存在性去挑。
func TestBinCandidatesKeepPlatformOrder(t *testing.T) {
	got := CCConnect.BinCandidates(filepath.Join("C:", "Users", "x"))
	if len(got) != 2 || !strings.HasSuffix(got[0], "cc-connect.exe") || strings.HasSuffix(got[1], ".exe") {
		t.Errorf("候选顺序不对（Windows 优先）：%v", got)
	}
}

// {config} 占位必须被换成真路径；换完不许留下占位符。
func TestInstallArgsReplaceConfigPlaceholder(t *testing.T) {
	cfg := filepath.Join("D:", "vault", "config.toml")
	args := CCConnect.InstallArgs(cfg)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "{config}") {
		t.Fatalf("占位没被换掉：%v", args)
	}
	if !strings.Contains(joined, "--no-capture-secrets") {
		t.Errorf("G2 那条（凭据不进任务定义）丢了：%v", args)
	}
	// 改返回值不许改到表里。
	args[0] = "mutated"
	if CCConnect.InstallCmd[0] == "mutated" {
		t.Error("InstallArgs 返回的是表里那片切片本身 —— 调用方能改到真相源")
	}
}

func TestValidDisplayMode(t *testing.T) {
	for _, ok := range []string{"", "quiet", "compact", "full"} {
		if !CCConnect.ValidDisplayMode(ok) {
			t.Errorf("%q 应该合法", ok)
		}
	}
	for _, bad := range []string{"FULL", "loud", "silent"} {
		if CCConnect.ValidDisplayMode(bad) {
			t.Errorf("%q 不该合法", bad)
		}
	}
}

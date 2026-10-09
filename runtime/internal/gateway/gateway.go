// Package gateway 是 SPEC §4.7 ① 「网关四条不变量」在代码里的落点。
//
// 为什么是**一张表**而不是一套接口方法：§4.7 把「换一个网关」定义成
// 「加一张表 + 一个适配器」，不是重写一套。所以网关的形态在这里是**数据** ——
// cc-connect 只是表里的一行。第二种网关要接的，是往这里再加一行，
// 以及（如果它的行为不是纯取值能表达的）在调用方加一个分支，
// 而不是把所有调用点再搜一遍。
//
// 这张表只装**形态**，不装行为：怎么装、怎么重启、日志落哪、会话落哪、认哪些枚举值。
// 真正的动作（跑命令、读文件）留在各调用方 —— 这样这张表可以整份被测试对着比。
package gateway

import "path/filepath"

// Spec 是一个网关的全部形态。字段按 §4.7 ① 的四条不变量分组。
type Spec struct {
	// Name 进报告、进指纹；不含空格。
	Name string

	// ── G1 声明式全量配置 ─────────────────────────────────────────────
	// ConfigFile 是渲染产物落点的文件名（`anc render` 全量生成的那一份）。
	ConfigFile string
	// DisplayModes 是 [display].mode 的合法取值，顺序 = 从静到吵。
	// 上游只认这几个，写错它**拒绝启动**（不是显示难看，是全部 bot 下线）。
	DisplayModes []string
	// DefaultDisplay 是上游自己的出厂默认（我们不写这一段时它就取这个值）。
	DefaultDisplay string
	// RelaySection 为 true 表示上游有一份 [relay]（bot 间通道）的配置段。
	// §4.7 禁令 2：它的默认值我们不知道，所以必须显式写全。
	RelaySection bool

	// ── G2 凭据只从进程环境解析 ──────────────────────────────────────
	// SecretKeyPrefix 是凭据桥注入的键名空间（配置里只留 ${ENV} 引用）。
	SecretKeyPrefix string

	// ── G3 装 / 重启 / 日志落点 ──────────────────────────────────────
	// BinNames 按平台优先级排：Windows 上取 .exe。
	BinNames []string
	// BinSubdir 是「本机自带的那份二进制」放在家目录下的哪一层。
	BinSubdir string
	// HomeDirName 是上游自己的落点（一个账号一份）。
	HomeDirName string
	// DaemonManifestName 是上游写的清单文件 —— 日志落点从它里面读，不猜。
	DaemonManifestName string
	// LogSubdir / LogFileName 是清单读不到时的兜底落点。
	LogSubdir   string
	LogFileName string
	// ServiceName 是单元名 / 计划任务名（不带扩展名）。
	ServiceName string
	// InstallCmd / RestartCmd 是子命令（不含可执行文件名）。
	// InstallCmd 的参数里允许出现 {config} 占位，由调用方替换成配置绝对路径。
	InstallCmd []string
	RestartCmd []string

	// ── G4 会话落盘可读 ─────────────────────────────────────────────
	// SocketRel 是数据目录下那个「真拨一次」用的 socket（相对 data 目录）。
	SocketRel string
	// SessionsRel 是数据目录下的会话落盘目录（相对 data 目录）。
	SessionsRel string
}

// CCConnect 是 §4.2 的默认 provider（MIT，Go 单二进制）。
var CCConnect = Spec{
	Name: "cc-connect",

	ConfigFile:     "config.toml",
	DisplayModes:   []string{"quiet", "compact", "full"},
	DefaultDisplay: "quiet",
	RelaySection:   true,

	SecretKeyPrefix: "ANC_",

	BinNames:           []string{"cc-connect.exe", "cc-connect"},
	BinSubdir:          filepath.Join(".anc", "bin"),
	HomeDirName:        ".cc-connect",
	DaemonManifestName: "daemon.json",
	LogSubdir:          "logs",
	LogFileName:        "cc-connect.log",
	ServiceName:        "cc-connect",
	InstallCmd:         []string{"daemon", "install", "--config", "{config}", "--no-capture-secrets", "--force"},
	RestartCmd:         []string{"daemon", "restart", "--force"},

	SocketRel:   filepath.Join("run", "api.sock"),
	SessionsRel: "sessions",
}

// HomeDir 是上游自己的落点（一个账号一份）。
func (s Spec) HomeDir(home string) string { return filepath.Join(home, s.HomeDirName) }

// ManifestPath 是上游写的清单文件 —— 日志落点从它里面读，不猜。
func (s Spec) ManifestPath(home string) string {
	return filepath.Join(s.HomeDir(home), s.DaemonManifestName)
}

// DefaultLogFile 是清单读不到时的兜底日志落点。
func (s Spec) DefaultLogFile(home string) string {
	return filepath.Join(s.HomeDir(home), s.LogSubdir, s.LogFileName)
}

// BinDir 是「本机自带的那份二进制」放哪。
func (s Spec) BinDir(home string) string { return filepath.Join(home, s.BinSubdir) }

// BinCandidates 列出可执行文件的候选绝对路径（不含 PATH 查找）。
func (s Spec) BinCandidates(home string) []string {
	out := make([]string, 0, len(s.BinNames))
	for _, n := range s.BinNames {
		out = append(out, filepath.Join(s.BinDir(home), n))
	}
	return out
}

// LoaderScriptName 是 Windows 上那份被计划任务拉起的包装脚本。
// 它由上游生成、上游重装会整份重写（--force），所以装载器必须能认出我们注入的那一段。
func (s Spec) LoaderScriptName() string { return s.ServiceName + "-daemon.ps1" }

// ServiceUnitName 是 systemd 单元名 / 计划任务名。
func (s Spec) ServiceUnitName() string { return s.ServiceName + ".service" }

// SocketPath / SessionsDir 把 G4 的两条落点拼到数据目录下。
func (s Spec) SocketPath(dataDir string) string  { return filepath.Join(dataDir, s.SocketRel) }
func (s Spec) SessionsDir(dataDir string) string { return filepath.Join(dataDir, s.SessionsRel) }

// InstallArgs / RestartArgs 把 {config} 占位换成真路径。
func (s Spec) InstallArgs(configPath string) []string {
	out := make([]string, len(s.InstallCmd))
	for i, a := range s.InstallCmd {
		if a == "{config}" {
			a = configPath
		}
		out[i] = a
	}
	return out
}

// RestartArgs 是重启子命令（原样一份拷贝 —— 调用方不该改到表里）。
func (s Spec) RestartArgs() []string {
	out := make([]string, len(s.RestartCmd))
	copy(out, s.RestartCmd)
	return out
}

// ValidDisplayMode 判一个 [display].mode 取值认不认识（空 = 取上游出厂默认，合法）。
func (s Spec) ValidDisplayMode(mode string) bool {
	if mode == "" {
		return true
	}
	for _, m := range s.DisplayModes {
		if m == mode {
			return true
		}
	}
	return false
}

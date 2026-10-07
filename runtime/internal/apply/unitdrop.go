package apply

import (
	"path/filepath"
	"strings"
)

// 这一层是 Linux 腿的凭据桥：把 secrets.env 变成 systemd 服务的环境变量。
//
// 为什么不往上游生成的单元文件里插一段（Windows 腿那样的做法）：上游
// `daemon install --force` 每次都会把单元文件整份重写回它自己那份，插进去的东西会被冲掉，
// 于是要维护「边界 + 指纹 + 被冲掉看得出来」一整套。systemd 的 drop-in
// （<单元名>.d/*.conf）是**我们完全拥有**的文件，上游不碰它 —— 天然幂等，
// 也不用去解析别人的文件。
//
// 落点与全文都由这里产出，不碰文件系统 —— 回显与测试都要它可复现。
// 边界标记与指纹跟 Windows 腿共用（都是 # 注释，systemd 也认），
// 所以两边的回显与「被改过」的判据是同一个口径。

// UnitDropInDir 是某份单元的 drop-in 目录：<单元所在目录>/<单元名>.d
func UnitDropInDir(unitPath string) string {
	return filepath.Join(filepath.Dir(unitPath), filepath.Base(unitPath)+".d")
}

// UnitDropInFile 是我们要写的那一份 drop-in 的绝对路径。
// 名字带 anc：现场一眼看得出是谁写的，将来要卸载也只删这一个文件。
func UnitDropInFile(unitPath string) string {
	return filepath.Join(UnitDropInDir(unitPath), "anc-secrets.conf")
}

// UnitBlock 生成 drop-in 全文。
func UnitBlock(secretsPath, version string) string {
	body := unitBody(secretsPath)
	return strings.Join([]string{
		LoaderBegin,
		"# v=" + version + " fp=" + shortHash(body),
		body,
		LoaderEnd,
	}, "\n")
}

// unitBody 是 drop-in 的正文（不含边界与指纹行），指纹算的就是它。
func unitBody(secretsPath string) string {
	return `# 凭据不进单元文件（daemon install 要带 --no-capture-secrets），只从收紧过权限的 secrets.env 读。
# 上游没有 dotenv：${ENV} 只从**进程环境**解析；systemd 用 EnvironmentFile 把这份文件读进服务环境。
# 路径**不加引号**：systemd 的 EnvironmentFile= 不去引号，加了引号整串会被当成非绝对路径、整条忽略
# （实测报 “path is not absolute, ignoring”）。所以这份路径不能带空白。
[Service]
EnvironmentFile=` + secretsPath
}

// environmentFilePathIn 从 `EnvironmentFile="<路径>"` 那一行取路径（回读用）。
// 与 secretsPathIn（Windows 的 $ancSecrets）同理：回读的只是**我们自己写进去的那一行**。
// 带不带引号都认 —— 手工改过引号不该让回读瞎掉。
func environmentFilePathIn(line string) (string, bool) {
	v, ok := strings.CutPrefix(strings.TrimSpace(line), "EnvironmentFile=")
	if !ok {
		return "", false
	}
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}
	if v == "" {
		return "", false
	}
	return v, true
}

// InspectUnit 读回一份 drop-in 的现状。判据（边界 + 指纹 + Intact）与 Windows 腿是同一套，
// 只有「凭据文件在哪」这一行不同：那边是 PowerShell 的 $ancSecrets，这边是 EnvironmentFile。
func InspectUnit(text string) Loader {
	l := InspectLoader(text)
	if l.Found && l.Secrets == "" {
		for _, ln := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
			if p, ok := environmentFilePathIn(ln); ok {
				l.Secrets = p
				break
			}
		}
	}
	return l
}

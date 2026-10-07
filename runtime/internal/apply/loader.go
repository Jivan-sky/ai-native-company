package apply

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// 托管区边界。为什么要有标记：这段代码注入的是**上游生成的文件**（Windows 上是
// ~/.cc-connect/cc-connect-daemon.ps1），上游重装会整份重写（--force），
// 所以装载器必须能认出「哪一段是我们的」，才能幂等替换、才能看出「被冲掉了 / 被人改了」。
const (
	LoaderBegin = "# >>> anc:secrets —— anc apply 生成，手改会被覆盖 >>>"
	LoaderEnd   = "# <<< anc:secrets <<<"
)

// 注入结果。三态各自对应一个真实场景，不合并成 bool：人要看的是「这次到底动了没有」。
const (
	ActionAdded     = "added"     // 上游脚本里没有托管区，插进去了
	ActionUpdated   = "updated"   // 有，但内容变了，整段换掉
	ActionUnchanged = "unchanged" // 有且逐字节相同 —— 重跑 apply 不该产生 diff
)

// LoaderBlock 生成托管区全文。secretsPath 用**绝对路径**：
// 这段脚本由计划任务拉起，cwd 不保证是家目录。
func LoaderBlock(secretsPath, version string) string {
	body := loaderBody(strings.ReplaceAll(secretsPath, "'", "''"))
	return strings.Join([]string{
		LoaderBegin,
		"# v=" + version + " fp=" + shortHash(body),
		body,
		LoaderEnd,
	}, "\n")
}

// loaderBody 是托管区的正文（不含边界与指纹行）。指纹算的就是它 ——
// 所以改了里面的路径、加了一行，指纹都会变。
func loaderBody(path string) string {
	return `# 凭据不进任务定义（daemon install 要带 --no-capture-secrets），只从收紧过权限的 secrets.env 读。
# 上游没有 dotenv：${ENV} 只从进程环境解析，缺了它 daemon 起不来（不是配置报错）。
$ancSecrets = '` + path + `'
if (-not (Test-Path -LiteralPath $ancSecrets)) { throw "ANC: 找不到凭据文件 $ancSecrets" }
foreach ($ancLine in Get-Content -LiteralPath $ancSecrets) {
  $ancText = $ancLine.Trim()
  if ($ancText -eq '' -or $ancText.StartsWith('#')) { continue }
  $ancEq = $ancText.IndexOf('=')
  if ($ancEq -lt 1) { continue }
  [Environment]::SetEnvironmentVariable($ancText.Substring(0, $ancEq).Trim(), $ancText.Substring($ancEq + 1).Trim(), 'Process')
}`
}

// InjectLoader 把托管区放进上游脚本：已有就整段替换（幂等），没有就插到最前面。
// 行尾跟随原文件（Windows 上上游写的是 CRLF，不要擅自改法定书）。
func InjectLoader(original, block string) (string, string) {
	eol := "\n"
	if strings.Contains(original, "\r\n") {
		eol = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(original, "\r\n", "\n"), "\n")
	blockLines := strings.Split(strings.ReplaceAll(block, "\r\n", "\n"), "\n")
	start, end := loaderSpan(lines)
	if start >= 0 {
		if equalLines(lines[start:end+1], blockLines) {
			return original, ActionUnchanged
		}
		out := append(append([]string{}, lines[:start]...), blockLines...)
		out = append(out, lines[end+1:]...)
		return strings.Join(out, eol), ActionUpdated
	}
	out := append(append([]string{}, blockLines...), "")
	out = append(out, lines...)
	return strings.Join(out, eol), ActionAdded
}

// Loader 是一份上游脚本里托管区的现状。
type Loader struct {
	Found    bool   // 找到了托管区
	Declared string // 块里声明的指纹
	Actual   string // 按块内容重算的指纹
	Secrets  string // 块里写的 secrets.env 路径
}

// Intact 报告托管区还在、且没被人改过。
// false 有两种可能，回显时要说清是哪种：整段被上游冲掉了（Found=false），
// 还是块内容被手改了（Found=true 但指纹对不上）。
func (l Loader) Intact() bool { return l.Found && l.Declared != "" && l.Declared == l.Actual }

// BlockFingerprint 返回一份托管区的指纹（= 块里声明的那串）。
// 导出是为了回显：现场要能一眼比对「命令打出来的」和「文件里写的」是不是同一个。
func BlockFingerprint(block string) string {
	lines := strings.Split(strings.ReplaceAll(block, "\r\n", "\n"), "\n")
	start, end := loaderSpan(lines)
	if start < 0 {
		return shortHash(strings.Join(lines, "\n"))
	}
	body := lines[start+1 : end]
	if len(body) > 0 && strings.Contains(body[0], "fp=") {
		body = body[1:]
	}
	return shortHash(strings.Join(body, "\n"))
}

// InspectLoader 读回托管区现状。
func InspectLoader(text string) Loader {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	start, end := loaderSpan(lines)
	if start < 0 {
		return Loader{}
	}
	l := Loader{Found: true}
	body := lines[start+1 : end]
	if len(body) > 0 && strings.Contains(body[0], "fp=") {
		l.Declared = strings.TrimSpace(body[0][strings.Index(body[0], "fp=")+len("fp="):])
		body = body[1:]
	}
	l.Actual = shortHash(strings.Join(body, "\n"))
	for _, ln := range body {
		if p, ok := secretsPathIn(ln); ok {
			l.Secrets = p
			break
		}
	}
	return l
}

// loaderSpan 找出托管区的行区间；没有托管区返回 (-1, -1)。
func loaderSpan(lines []string) (int, int) {
	start := -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if start < 0 && strings.HasPrefix(t, "# >>> anc:secrets") {
			start = i
			continue
		}
		if start >= 0 && strings.HasPrefix(t, "# <<< anc:secrets") {
			return start, i
		}
	}
	return -1, -1
}

// secretsPathIn 从 `$ancSecrets = '<路径>'` 里取出路径。
// PowerShell 的单引号里，两个连续单引号表示一个单引号 —— 所以要还原。
func secretsPathIn(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "$ancSecrets") {
		return "", false
	}
	q := strings.IndexByte(t, '\'')
	if q < 0 {
		return "", false
	}
	rest := t[q:]
	if len(rest) < 2 || rest[len(rest)-1] != '\'' {
		return "", false
	}
	return strings.ReplaceAll(rest[1:len(rest)-1], "''", "'"), true
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

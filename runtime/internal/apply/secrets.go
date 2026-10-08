// Package apply 是「装载」这一步里不许出错的那部分纯逻辑。
//
// 它只管两件与凭据有关的事：
//
//  1. 体检：gateway 的 config 里凭据一律写成 ${ENV} 引用，明文只住 secrets.env。
//     装载前必须知道「产物引用了哪些键、这份文件里有没有」—— 缺键的表现**不是**配置报错，
//     而是 daemon 起不来（实测 v1.3.4：`env var placeholder references unset variable`、
//     `failed to create platform ... app_id and app_secret are required`）。
//
//  2. 桥：上游 daemon 的装载体没有 dotenv（实测 v1.3.4 二进制里搜不到），
//     ${ENV} 只从**进程环境**解析。所以必须在拉起 gateway 之前把 secrets.env
//     读进进程环境。这段代码注入的是**上游生成的文件**，所以必须幂等、留指纹、能看出漂移。
//
// 这一层不碰 org、不碰渲染、不碰命令行 —— 那些在各自的包里。
package apply

import (
	"fmt"
	"regexp"
	"strings"
)

// SecretsFile 是一份解析过的凭据文件。
type SecretsFile struct {
	Path   string
	Keys   []string // 出现顺序（去重）
	Values map[string]string
	Bad    []string // 解析不了的行（带行号）—— 回显给 owner，不吞
}

var reKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// LegalKey 判断一个键名写不写得进凭据文件（与解析同一把尺子）。
//
// 用它的地方只有一处：体检时把「这个键只是没写」和「这个键根本写不出来」分开报。
// 两者都拦，但后者照着「补键」的提示去补也永远补不上 —— 得先改键名是怎么拼出来的。
func LegalKey(k string) bool { return reKey.MatchString(k) }

// ParseSecrets 解析 KEY=VALUE。空行与 # 开头的行忽略；只按**第一个** = 切分，
// 值里可以再有 =（base64 常见）。同一个键出现多次取最后一条 —— 与 shell 的 source 一致，
// 人接手这份文件时的直觉也是这个。
func ParseSecrets(path, text string) SecretsFile {
	f := SecretsFile{Path: path, Values: map[string]string{}}
	seen := map[string]bool{}
	for i, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 1 {
			f.Bad = append(f.Bad, fmt.Sprintf("第 %d 行没有 =：%s", i+1, line))
			continue
		}
		k := strings.TrimSpace(line[:eq])
		if !reKey.MatchString(k) {
			f.Bad = append(f.Bad, fmt.Sprintf("第 %d 行的键名 %q 不像环境变量名", i+1, k))
			continue
		}
		if !seen[k] {
			seen[k] = true
			f.Keys = append(f.Keys, k)
		}
		f.Values[k] = strings.TrimSpace(line[eq+1:])
	}
	return f
}

// Missing 返回产物引用了、这份文件里却没有的键：保留传进来的顺序，但**去重** ——
// 同一个键缺一次就是缺，报两遍只会让现场数不清到底缺几个。
func Missing(refs []string, f SecretsFile) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range refs {
		if seen[r] {
			continue
		}
		seen[r] = true
		if _, ok := f.Values[r]; !ok {
			out = append(out, r)
		}
	}
	return out
}

// Blank 返回「写了键但值是空的」的键 —— 多半是从控制台粘漏了半行。
// 它与缺键同一档处置（装载后一样起不来），但分开报：一个是没写，一个是写了空。
func (f SecretsFile) Blank() []string {
	var out []string
	for _, k := range f.Keys {
		if strings.TrimSpace(f.Values[k]) == "" {
			out = append(out, k)
		}
	}
	return out
}

// Has 判断某个键在不在（供回显用）。
func (f SecretsFile) Has(key string) bool {
	_, ok := f.Values[key]
	return ok
}

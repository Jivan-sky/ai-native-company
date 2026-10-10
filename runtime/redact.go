package main

// 脱敏表落定 —— 进程启动时一次。
//
// 为什么放在 main 的最前面而不是各个子命令里：审计的承诺是「**落盘即脱敏**」，
// 而写口有五条（归集 / 补记 / 信封关卡 / 读出口 / 审批留痕）。把脱敏表在启动时
// 一次落定，五条路都吃同一份 —— 散在各子命令里落定，早晚会漏一条。
//
// 语言与代价：与 `anc` 同一个 Go 二进制，零新依赖、零新进程、体积不变。
// 见 internal/audit/redact.go。

import (
	"os"
	"path/filepath"
	"strings"

	"anc/internal/apply"
	"anc/internal/audit"
)

// secretsFileForRedact 与 `anc apply --secrets` 的默认值**同一处口径**：
// `~/.anc/secrets.env`（见 apply.go）。读不到 = 没有已知值那一腿，不报错 ——
// 出厂规则表照样生效，而「这台机器上有没有凭据文件」不是审计该管的事。
func secretsFileForRedact() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".anc", "secrets.env")
}

// looksLikeSecretName 判断一个凭据文件里的键名**是不是装密钥的那种**。
//
// 为什么要筛：ANC 的凭据文件里理论上只该有密钥，但真有人往里塞一个
// `ANC_BASE_URL` 也说得过去 —— 把那种键也变成抹除规则，会把审计要看的东西
// （它读的是哪个地址）一并抹掉。所以按名字收口：只有名字里带密钥语义的那几个词，
// 才升级成「按名字抹」的规则。**值那一腿不筛**（文件里的每个值都按已知密钥值抹）。
func looksLikeSecretName(name string) bool {
	u := strings.ToUpper(strings.TrimSpace(name))
	for _, mark := range []string{"_SECRET", "_TOKEN", "_KEY", "_PASSWORD", "_PASSWD", "_CREDENTIAL"} {
		if strings.Contains(u, mark) {
			return true
		}
	}
	return false
}

// armRedactor 落定进程级脱敏表，返回带上的已知密钥值条数（供 `anc audit rules` 回显）。
func armRedactor() int {
	rules := audit.BuiltinRedactRules()
	var known []audit.KnownSecret

	if p := secretsFileForRedact(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			f := apply.ParseSecrets(p, string(b))
			var names []string
			for _, k := range f.Keys {
				known = append(known, audit.KnownSecret{Name: k, Value: f.Values[k]})
				if looksLikeSecretName(k) {
					names = append(names, k)
				}
			}
			rules = append(rules, audit.RulesForKeys(names)...)
		}
	}
	audit.SetRedactor(audit.NewRedactor(rules, known))
	return len(audit.Effective().Rules())
}

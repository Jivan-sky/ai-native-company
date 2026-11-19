package main

import "anc/internal/org"

// roleOrSystem 把主体解析成岗位；解析不出就记 "system"。
//
// 不记空串：审计写口已经拒收空 role（判据 4），所以调用方必须给一个明确答案 ——
// "查无此人" 与 "系统发起" 在这里合并成 system，并在 Detail 里由各调用方自己说明来源。
func roleOrSystem(vault, ref string) string {
	if r := org.RoleOfVault(vault, ref); r != "" {
		return r
	}
	return "system"
}

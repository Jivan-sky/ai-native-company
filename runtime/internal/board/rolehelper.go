package board

import "anc/internal/org"

// roleOrSystem：看板侧的同一个口径（与 main 包那份同义，各包一份，避免互相依赖）。
func roleOrSystem(vault, ref string) string {
	if r := org.RoleOfVault(vault, ref); r != "" {
		return r
	}
	return "system"
}

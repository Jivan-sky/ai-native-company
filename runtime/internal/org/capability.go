package org

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 能力 = 模块 × 动作 × 范围。这是「权限隔离」真正落笔的地方：
//
//	不是「你能看哪几个页面」，而是「你能调用哪段逻辑」。
//
// 规则放数据里（`<vault>/capabilities.md`），代码只解释它 —— 跟 domains.md / roles.md 同一条纪律。
// 没有这个文件 = 没有能力 = **一律拒**（fail-closed）：门禁是数据，数据不在就不放行。
type Cap struct {
	Module string // crm | erp | ticket | board | audit | members | approvals | data | jobs
	Action string // read | write | approve | execute | admin
	Scope  string // all | dept | self（v1 只做判定，范围留给条目级过滤）
}

type CapTable map[string][]Cap

// FileName 是能力表在 vault 里的位置。跟 domains.md 并列，人改、代码读。
const CapFileName = "capabilities.md"

// LoadCapabilities 读 `<vault>/capabilities.md` 的表格。文件不在 = 空表（= 全拒），不是错误：
// 刚 init 的机器不该因为「还没配权限」而红，但也不该因此放行任何东西。
func LoadCapabilities(vault string) (CapTable, error) {
	p := filepath.Join(vault, CapFileName)
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return CapTable{}, nil
		}
		return nil, err
	}
	defer f.Close()

	t := CapTable{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := splitRow(line)
		if len(cells) < 4 {
			continue
		}
		role, module, action := strings.ToLower(cells[0]), strings.ToLower(cells[1]), strings.ToLower(cells[2])
		// 表头与分隔行跳过：`| role | module | ... |` 与 `|---|`
		if role == "role" || strings.HasPrefix(role, "-") || module == "" {
			continue
		}
		t[role] = append(t[role], Cap{Module: module, Action: action, Scope: strings.ToLower(cells[3])})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读 %s：%w", CapFileName, err)
	}
	return t, nil
}

func splitRow(line string) []string {
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// Allow 判「这个角色能不能对这块做这个动作」。
//
// `admin` 是一个通配动作：有了它，这块业务的所有动作都算有（范围仍按它自己那行）。
// `*` 作模块名表示所有模块。这两条是**写进数据里的约定**，不是代码里的特例分支。
func (t CapTable) Allow(role, module, action string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	module = strings.ToLower(strings.TrimSpace(module))
	action = strings.ToLower(strings.TrimSpace(action))
	if role == "" || module == "" || action == "" {
		return false
	}
	for _, c := range t[role] {
		if c.Module != module && c.Module != "*" {
			continue
		}
		if c.Action == action || c.Action == "admin" {
			return true
		}
	}
	return false
}

// Modules 返回这个角色装了哪些模块（给界面用：能进的页就是这些）。
// 去重、按字典序 —— 输出要稳定，不然界面上那一排会自己跳。
func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func (t CapTable) Modules(role string) []string {
	seen := map[string]bool{}
	for _, c := range t[strings.ToLower(strings.TrimSpace(role))] {
		seen[c.Module] = true
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sortStrings(out)
	return out
}

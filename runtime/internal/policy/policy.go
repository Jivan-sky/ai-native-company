// Package policy —— 数据访问策略：谁能看哪张表的哪一列。
//
// 三条口径：
//
//  1. **默认拒绝**：没被明确允许的列，一律不返回。fail-closed 才有意义。
//  2. **按（主体，表，列）判定**：主体可以是具体人/应用，也可以是 `*`；
//     规则从上往下第一条命中即生效 —— 顺序本身就是意图，不玩隐式优先级。
//  3. **拒绝是整条查询失败**：不做「悄悄少一列」。静默丢列会让人以为查到了全部，
//     那是最坏的一种假绿。脱敏（mask）则保留列、替换值。
package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Schema = "anc.policy/v1"

type Policy struct {
	Schema     string `json:"schema"`
	DataSource string `json:"datasource"`
	// Default 没写规则时的动作：deny（默认）| allow | mask
	Default string `json:"default,omitempty"`
	// Derived 决定「目录里没有的列」（计算列，如 count(*)）怎么办：deny（默认）| allow。
	// 默认拒绝是安全的；客户显式写 allow 才能查聚合，且这些列会作为「派生列」进台账。
	Derived string `json:"derived,omitempty"`
	Rules   []Rule `json:"rules"`
	// RowFilters 是行级过滤：把查询结果再套一层 WHERE。
	// 写法是**包装**而不是拼接：SELECT * FROM (你的查询) rls WHERE (条件) ——
	// 这样不需要解析 SQL，也不会被用户的 WHERE/ORDER BY 位置影响。
	RowFilters []RowFilter `json:"row_filters,omitempty"`
}

type Rule struct {
	Subject string   `json:"subject"` // 成员/应用名，或 "*"
	Table   string   `json:"table"`   // 表名，或 "*"
	Columns []string `json:"columns"` // 列名；"*" 表示该表全部列
	Action  string   `json:"action"`  // allow | mask | deny
}

func Load(dir, datasource string) (*Policy, error) {
	p := filepath.Join(dir, datasource+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 没有策略 = 全部拒绝（调用方按 nil 处理）
		}
		return nil, err
	}
	var pol Policy
	if err := json.Unmarshal(b, &pol); err != nil {
		return nil, fmt.Errorf("策略文件读不懂：%v", err)
	}
	if err := pol.validate(datasource); err != nil {
		return nil, err
	}
	return &pol, nil
}

func (p Policy) validate(ds string) error {
	if p.Schema != Schema {
		return fmt.Errorf("策略 schema=%q，只认 %q", p.Schema, Schema)
	}
	if p.DataSource != ds {
		return fmt.Errorf("策略写在数据源 %q 下，但 datasource=%q", ds, p.DataSource)
	}
	switch p.Default {
	case "", "deny", "allow", "mask":
	default:
		return fmt.Errorf("default=%q（deny | allow | mask）", p.Default)
	}
	switch p.Derived {
	case "", "deny", "allow":
	default:
		return fmt.Errorf("derived=%q（deny | allow）", p.Derived)
	}
	for i, f := range p.RowFilters {
		if strings.TrimSpace(f.Table) == "" || strings.TrimSpace(f.Where) == "" {
			return fmt.Errorf("第 %d 条行过滤缺 table / where", i+1)
		}
		if strings.Contains(f.Where, ";") {
			return fmt.Errorf("第 %d 条行过滤的 where 里不许有分号", i+1)
		}
	}
	for i, r := range p.Rules {
		if strings.TrimSpace(r.Subject) == "" || strings.TrimSpace(r.Table) == "" || len(r.Columns) == 0 {
			return fmt.Errorf("第 %d 条规则缺 subject / table / columns", i+1)
		}
		switch r.Action {
		case "allow", "mask", "deny":
		default:
			return fmt.Errorf("第 %d 条规则 action=%q（allow | mask | deny）", i+1, r.Action)
		}
	}
	return nil
}

// Action 返回（主体，表，列）上的动作。
func (p *Policy) Action(subject, table, column string) string {
	if p == nil {
		return "deny"
	}
	for _, r := range p.Rules {
		if !match(r.Subject, subject) || !match(r.Table, table) {
			continue
		}
		for _, c := range r.Columns {
			if match(c, column) {
				return r.Action
			}
		}
	}
	if p.Default == "" {
		return "deny"
	}
	return p.Default
}

func (p *Policy) derivedAction() string {
	if p == nil || p.Derived == "" {
		return "deny"
	}
	return p.Derived
}

// match 支持三种：`*`（全通）、前缀 `dir/*`（文件目录用）、精确匹配。
func match(pattern, v string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		// `dir/*` 也要匹配 `dir` 本身（列目录时传的是目录路径，没有结尾斜杠）。
		if strings.EqualFold(strings.TrimSuffix(prefix, "/"), v) {
			return true
		}
		return strings.HasPrefix(strings.ToLower(v), strings.ToLower(prefix))
	}
	return strings.EqualFold(pattern, v)
}

// Decision 是对一条查询的判定结果。
type Decision struct {
	Masked  []string `json:"masked,omitempty"`
	Denied  string   `json:"denied,omitempty"`
	Derived []string `json:"derived,omitempty"` // 目录里没有的列（计算列），按 derived 设置放行
}

// Apply 按列策略处理结果：deny → 整条拒绝；mask → 值替换成 ***；allow → 原样。
// known 是目录里出现过的列名集合（表名 → 列集合）；用它来区分「计算列」与「策略外的列」。
func (p *Policy) Apply(subject string, tables map[string]string, known map[string]bool, cols []string, rows [][]*string) (Decision, error) {
	var d Decision
	for _, c := range cols {
		table := tables[c] // 没映射到表就只有 table="*" 的规则能命中
		if known != nil && !known[c] && p.derivedAction() == "allow" {
			d.Derived = append(d.Derived, c)
			continue
		}
		switch p.Action(subject, table, c) {
		case "allow":
		case "mask":
			d.Masked = append(d.Masked, c)
		default:
			d.Denied = c
			return d, fmt.Errorf("策略拒绝：%s 不能看列 %s", subject, c)
		}
	}
	for _, c := range d.Masked {
		idx := indexOf(cols, c)
		if idx < 0 {
			continue
		}
		for i := range rows {
			if idx < len(rows[i]) && rows[i][idx] != nil {
				v := "***"
				rows[i][idx] = &v
			}
		}
	}
	return d, nil
}

func indexOf(xs []string, v string) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return -1
}

type RowFilter struct {
	Table string `json:"table"`
	Where string `json:"where"`
}

// Wrap 如果命中了行过滤，把查询包一层并要求只返回满足条件的行。
// 命中判据：SQL 文本里出现了那条过滤的表名（大小写不敏感）。
// 这是**保守**的启发式：宁可多套一层（可能报列不存在），也不放过。
func (p *Policy) Wrap(sql string) (string, []string) {
	if p == nil || len(p.RowFilters) == 0 {
		return sql, nil
	}
	low := strings.ToLower(sql)
	var conds, applied []string
	for _, f := range p.RowFilters {
		if !strings.Contains(low, strings.ToLower(f.Table)) {
			continue
		}
		conds = append(conds, "("+f.Where+")")
		applied = append(applied, f.Table+": "+f.Where)
	}
	if len(conds) == 0 {
		return sql, nil
	}
	return "SELECT * FROM (" + strings.TrimSuffix(strings.TrimSpace(sql), ";") + ") AS anc_rls WHERE " +
		strings.Join(conds, " AND "), applied
}

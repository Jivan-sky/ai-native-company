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
	Rules   []Rule `json:"rules"`
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

func match(pattern, v string) bool {
	return pattern == "*" || strings.EqualFold(pattern, v)
}

// Decision 是对一条查询的判定结果。
type Decision struct {
	Masked  []string `json:"masked,omitempty"`
	Denied  string   `json:"denied,omitempty"`
	Unknown []string `json:"unknown,omitempty"` // 目录里没有的列（可能是计算列）
}

// Apply 按列策略处理结果：deny → 整条拒绝；mask → 值替换成 ***；allow → 原样。
// known 是目录里出现过的列名集合（表名 → 列集合）；用它来区分「计算列」与「策略外的列」。
func (p *Policy) Apply(subject string, tables map[string]string, cols []string, rows [][]*string) (Decision, error) {
	var d Decision
	for _, c := range cols {
		table := tables[c] // 没映射到表就只有 table="*" 的规则能命中
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

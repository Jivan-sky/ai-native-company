package catalog

// 值形状分类：**只在库里跑聚合计数**，一行数据都不取回来。
//
// 做法：对候选列跑一条
//
//	SELECT count(*), count(*) FILTER (WHERE col ~ '邮箱'), count(*) FILTER (WHERE col ~ '手机') …
//	FROM (SELECT col FROM t WHERE col IS NOT NULL LIMIT n) s
//
// 得到「命中率」，再与列名规则合并成建议（带证据）。**建议不是拍板**：
// 拍板走 `anc catalog confirm`，记谁、何时、依据。

import (
	"context"
	"fmt"
	"strings"

	"anc/internal/ds"
)

// ValueRules 是一组「值长什么样」的检测规则。正则都写成 PG 语法。
type ValueRules struct {
	SampleRows int
	Patterns   []ValuePattern
}

type ValuePattern struct {
	Level string `json:"level"` // pii | sensitive
	Name  string `json:"name"`  // email / phone / idcard / bankcard
	Regex string `json:"regex"`
}

// DefaultRules 是出厂规则：只覆盖最常见的四类个人标识 / 金额串。
func DefaultRules() ValueRules {
	return ValueRules{SampleRows: 2000, Patterns: []ValuePattern{
		{Level: "pii", Name: "email", Regex: `^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$`},
		{Level: "pii", Name: "phone", Regex: `^1[3-9][0-9]{9}$`},
		{Level: "pii", Name: "idcard", Regex: `^[0-9]{17}[0-9Xx]$`},
		{Level: "sensitive", Name: "bankcard", Regex: `^[0-9]{12,19}$`},
	}}
}

// Evidence 是一条分类证据。
type Evidence struct {
	Table   string  `json:"table"`
	Column  string  `json:"column"`
	Level   string  `json:"level"`
	Pattern string  `json:"pattern"`
	Hits    int64   `json:"hits"`
	Total   int64   `json:"total"`
	Rate    float64 `json:"rate"`
}

// MinRate 是「算命中」的下限：低于它只能算巧合。
const MinRate = 0.30

// ScanValues 对候选文本列做值形状检测，返回证据（可直接用于建议）。
func ScanValues(ctx context.Context, src ds.Source, password string, snap Snapshot) ([]Evidence, error) {
	rules := DefaultRules()
	var out []Evidence
	for _, t := range snap.Tables {
		for _, c := range t.Columns {
			if !isTexty(c.Type) {
				continue
			}
			var sql string
			switch src.Type {
			case "postgres":
				sql = buildValueSQL(t.Name, c.Name, rules)
			case "mysql":
				sql = buildValueSQLMySQL(t.Name, c.Name, rules)
			default:
				return nil, fmt.Errorf("值形状分类不支持 %q（postgres / mysql）", src.Type)
			}
			res, err := ds.Query(ctx, src, password, sql)
			if err != nil {
				return nil, fmt.Errorf("%s.%s：%v", t.Name, c.Name, err)
			}
			if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
				continue
			}
			total := atoi(res.Rows[0][0])
			if total == 0 {
				continue
			}
			for i, p := range rules.Patterns {
				idx := i + 1
				if idx >= len(res.Rows[0]) {
					break
				}
				hits := atoi(res.Rows[0][idx])
				if hits == 0 {
					continue
				}
				rate := float64(hits) / float64(total)
				if rate < MinRate {
					continue
				}
				out = append(out, Evidence{
					Table: t.Name, Column: c.Name, Level: p.Level,
					Pattern: p.Name, Hits: hits, Total: total, Rate: rate,
				})
			}
		}
	}
	return out, nil
}

func isTexty(t string) bool {
	for _, k := range []string{"char", "text", "citext"} {
		if strings.Contains(strings.ToLower(t), k) {
			return true
		}
	}
	return false
}

// buildValueSQL 生成聚合 SQL。标识符走双引号转义（表名列名来自目录，不是用户输入，
// 但仍然按最坏情况处理：引号里的引号要翻倍）。
func buildValueSQL(table, col string, rules ValueRules) string {
	var aggs []string
	for _, p := range rules.Patterns {
		aggs = append(aggs, fmt.Sprintf(`count(*) FILTER (WHERE %s::text ~ '%s')`,
			quoteIdent(col), escapeLiteral(p.Regex)))
	}
	return fmt.Sprintf(`SELECT count(*)%s FROM (SELECT %s FROM %s WHERE %s IS NOT NULL LIMIT %d) s`,
		func() string {
			if len(aggs) == 0 {
				return ""
			}
			return ", " + strings.Join(aggs, ", ")
		}(),
		quoteIdent(col), quoteIdent(table), quoteIdent(col), rules.SampleRows)
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func escapeLiteral(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func atoi(p *string) int64 {
	if p == nil {
		return 0
	}
	var n int64
	_, _ = fmt.Sscanf(*p, "%d", &n)
	return n
}

// buildValueSQLMySQL 生成 MySQL 版聚合 SQL：REGEXP 返回 1/0，用 SUM 计数。
func buildValueSQLMySQL(table, col string, rules ValueRules) string {
	var aggs []string
	for _, p := range rules.Patterns {
		aggs = append(aggs, fmt.Sprintf(`SUM(%s REGEXP '%s')`, quoteIdentMySQL(col), escapeLiteral(p.Regex)))
	}
	sel := "COUNT(*)"
	if len(aggs) > 0 {
		sel += ", " + strings.Join(aggs, ", ")
	}
	return fmt.Sprintf(`SELECT %s FROM (SELECT %s FROM %s WHERE %s IS NOT NULL LIMIT %d) s`,
		sel, quoteIdentMySQL(col), quoteIdentMySQL(table), quoteIdentMySQL(col), rules.SampleRows)
}

func quoteIdentMySQL(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// Package catalog —— 数据目录：内省 schema、存快照、算指纹、做 diff。
//
// 三条口径：
//
//  1. **只要元数据，不要行**：内省只读 information_schema；分类靠库内聚合计数，
//     样本值一个字节都不落盘（零外流能查）。
//  2. **快照 + 指纹**：每次扫描存一份带指纹的快照；指纹变了就是 schema 漂移。
//  3. **diff 要认出「改名」**：把「删了一列」与「一列改名」分开——后者是治理事故的第一名。
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"anc/internal/ds"
)

const Schema = "anc.catalog/v1"

type Snapshot struct {
	Schema      string    `json:"schema"`
	DataSource  string    `json:"datasource"`
	ScannedAt   time.Time `json:"scanned_at"`
	Fingerprint string    `json:"fingerprint"`
	Tables      []Table   `json:"tables"`
}

type Table struct {
	Schema  string   `json:"schema,omitempty"`
	Name    string   `json:"name"`
	Comment string   `json:"comment,omitempty"`
	Columns []Column `json:"columns"`
}

type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	IsPK     bool   `json:"is_pk,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

const qTables = `SELECT c.relname AS table_name, COALESCE(obj_description(c.oid, 'pg_class'), '') AS comment
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'r' AND n.nspname NOT IN ('pg_catalog','information_schema')
ORDER BY 1`

const qColumns = `SELECT c.relname AS table_name, a.attname AS column_name,
  format_type(a.atttypid, a.atttypmod) AS data_type,
  NOT a.attnotnull AS nullable,
  COALESCE(col_description(c.oid, a.attnum), '') AS comment
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
WHERE c.relkind = 'r' AND n.nspname NOT IN ('pg_catalog','information_schema')
ORDER BY 1, a.attnum`

const qPK = `SELECT c.relname AS table_name, a.attname AS column_name
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_index i ON i.indrelid = c.oid AND i.indisprimary
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY(i.indkey)
WHERE c.relkind = 'r' AND n.nspname NOT IN ('pg_catalog','information_schema')`

// Scan 内省一个数据源，产出快照（不含行数据）。
func Scan(ctx context.Context, src ds.Source, password string) (Snapshot, error) {
	if src.Type == "mysql" {
		return scanMySQL(ctx, src, password)
	}
	res, err := ds.QueryMany(ctx, src, password, []string{qTables, qColumns, qPK})
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Schema: Schema, DataSource: src.Name, ScannedAt: time.Now().UTC()}
	ptrs := map[string]*Table{}
	for _, row := range res[0].Rows {
		if len(row) < 2 {
			continue
		}
		snap.Tables = append(snap.Tables, Table{Name: val(row[0]), Comment: val(row[1])})
	}
	for i := range snap.Tables {
		ptrs[snap.Tables[i].Name] = &snap.Tables[i]
	}
	for _, row := range res[1].Rows {
		if len(row) < 5 {
			continue
		}
		t, ok := ptrs[val(row[0])]
		if !ok {
			continue
		}
		t.Columns = append(t.Columns, Column{
			Name: val(row[1]), Type: val(row[2]), Nullable: val(row[3]) == "t", Comment: val(row[4]),
		})
	}
	for _, row := range res[2].Rows {
		if len(row) < 2 {
			continue
		}
		if t, ok := ptrs[val(row[0])]; ok {
			for i := range t.Columns {
				if t.Columns[i].Name == val(row[1]) {
					t.Columns[i].IsPK = true
				}
			}
		}
	}
	sort.Slice(snap.Tables, func(i, j int) bool { return snap.Tables[i].Name < snap.Tables[j].Name })
	snap.Fingerprint = Fingerprint(snap)
	return snap, nil
}

// Fingerprint 是 schema 的指纹：只看「结构」，不看扫描时间。
func Fingerprint(s Snapshot) string {
	h := sha256.New()
	for _, t := range s.Tables {
		fmt.Fprintf(h, "T:%s:%s\n", t.Name, t.Comment)
		for _, c := range t.Columns {
			fmt.Fprintf(h, "C:%s:%s:%t:%t:%s\n", c.Name, c.Type, c.Nullable, c.IsPK, c.Comment)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Dir 是快照存放处（控制面，不进客户库）。
func Dir(vault string) string { return filepath.Join(filepath.Dir(vault), "catalog") }

func Save(vault string, s Snapshot) error {
	if err := os.MkdirAll(Dir(vault), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(Dir(vault), s.DataSource+".json.tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(Dir(vault), s.DataSource+".json"))
}

func Load(vault, name string) (Snapshot, error) {
	var s Snapshot
	b, err := os.ReadFile(filepath.Join(Dir(vault), name+".json"))
	if err != nil {
		return s, fmt.Errorf("还没有这个数据源的目录快照：%s", name)
	}
	return s, json.Unmarshal(b, &s)
}

// Change 是 diff 的一条。
type Change struct {
	Kind   string `json:"kind"` // added_table | removed_table | added_column | removed_column | changed_column | renamed_column
	Table  string `json:"table"`
	Column string `json:"column,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Diff 比较两份快照。改名识别：同表里「一列消失 + 一列新增，且类型与可空相同」→ 判为改名。
func Diff(old, cur Snapshot) []Change {
	var out []Change
	oldT := map[string]Table{}
	for _, t := range old.Tables {
		oldT[t.Name] = t
	}
	curT := map[string]Table{}
	for _, t := range cur.Tables {
		curT[t.Name] = t
	}
	for name, t := range curT {
		o, ok := oldT[name]
		if !ok {
			out = append(out, Change{Kind: "added_table", Table: name})
			continue
		}
		out = append(out, diffColumns(name, o, t)...)
	}
	for name := range oldT {
		if _, ok := curT[name]; !ok {
			out = append(out, Change{Kind: "removed_table", Table: name})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].Column < out[j].Column
	})
	return out
}

func diffColumns(table string, o, n Table) []Change {
	var out []Change
	oC := map[string]Column{}
	for _, c := range o.Columns {
		oC[c.Name] = c
	}
	nC := map[string]Column{}
	for _, c := range n.Columns {
		nC[c.Name] = c
	}
	var removed []Column
	for name, c := range oC {
		if _, ok := nC[name]; !ok {
			removed = append(removed, c)
		}
	}
	var added []Column
	for name, c := range nC {
		if _, ok := oC[name]; !ok {
			added = append(added, c)
		}
	}
	if len(removed) == 1 && len(added) == 1 &&
		removed[0].Type == added[0].Type && removed[0].Nullable == added[0].Nullable {
		out = append(out, Change{Kind: "renamed_column", Table: table, Column: added[0].Name,
			Detail: fmt.Sprintf("%s → %s（类型 %s 未变）", removed[0].Name, added[0].Name, added[0].Type)})
		removed, added = nil, nil
	}
	for _, c := range removed {
		out = append(out, Change{Kind: "removed_column", Table: table, Column: c.Name})
	}
	for _, c := range added {
		out = append(out, Change{Kind: "added_column", Table: table, Column: c.Name, Detail: c.Type})
	}
	for _, c := range n.Columns {
		if o, ok := oC[c.Name]; ok && o.Type != c.Type {
			out = append(out, Change{Kind: "changed_column", Table: table, Column: c.Name,
				Detail: o.Type + " → " + c.Type})
		}
	}
	return out
}

// Suggestion 是按规则给出的分级**建议**（不等于拍板）。
type Suggestion struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	Level  string `json:"level"` // pii | sensitive | internal | public
	Why    string `json:"why"`
}

// Classify 只用列名规则给建议；真正拍板要人确认（值形状扫描是下一刀）。
func Classify(s Snapshot) []Suggestion {
	var out []Suggestion
	for _, t := range s.Tables {
		for _, c := range t.Columns {
			name := strings.ToLower(c.Name)
			switch {
			case strings.Contains(name, "email") || strings.Contains(name, "phone") ||
				strings.Contains(name, "mobile") || strings.Contains(name, "id_card") ||
				strings.Contains(name, "idcard") || strings.Contains(name, "passport"):
				out = append(out, Suggestion{t.Name, c.Name, "pii", "列名命中个人标识"})
			case strings.Contains(name, "salary") || strings.Contains(name, "amount") ||
				strings.Contains(name, "price") || strings.Contains(name, "revenue"):
				out = append(out, Suggestion{t.Name, c.Name, "sensitive", "列名命中金额/收入"})
			}
		}
	}
	return out
}

func val(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

const myTables = `SELECT table_name, COALESCE(table_comment, '')
FROM information_schema.tables
WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY 1`

const myColumns = `SELECT table_name, column_name, column_type,
  CASE WHEN is_nullable = 'YES' THEN '1' ELSE '0' END,
  COALESCE(column_comment, ''),
  CASE WHEN column_key = 'PRI' THEN '1' ELSE '0' END
FROM information_schema.columns
WHERE table_schema = DATABASE() ORDER BY 1, ordinal_position`

func scanMySQL(ctx context.Context, src ds.Source, password string) (Snapshot, error) {
	res, err := ds.QueryMany(ctx, src, password, []string{myTables, myColumns})
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Schema: Schema, DataSource: src.Name, ScannedAt: time.Now().UTC()}
	ptrs := map[string]*Table{}
	for _, row := range res[0].Rows {
		if len(row) < 2 {
			continue
		}
		snap.Tables = append(snap.Tables, Table{Name: val(row[0]), Comment: val(row[1])})
	}
	for i := range snap.Tables {
		ptrs[snap.Tables[i].Name] = &snap.Tables[i]
	}
	for _, row := range res[1].Rows {
		if len(row) < 6 {
			continue
		}
		t, ok := ptrs[val(row[0])]
		if !ok {
			continue
		}
		t.Columns = append(t.Columns, Column{
			Name: val(row[1]), Type: val(row[2]), Nullable: val(row[3]) == "1",
			Comment: val(row[4]), IsPK: val(row[5]) == "1",
		})
	}
	sort.Slice(snap.Tables, func(i, j int) bool { return snap.Tables[i].Name < snap.Tables[j].Name })
	snap.Fingerprint = Fingerprint(snap)
	return snap, nil
}

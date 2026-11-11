package main

// anc db —— 数据源：看有哪些、跑一条只读查询。
//
// 只连不搬：结果只打到屏幕/接口，不落真相源。查询本身进 audit（谁查了、几行、多久）。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"anc/internal/audit"
	"anc/internal/catalog"
	"anc/internal/ds"
	"anc/internal/policy"
)

const dbUsage = `anc db —— 数据源（只读）

用法：
  anc db ls    <vault>
  anc db query <vault> <数据源名> --sql "SELECT …" [--secrets <文件>] [--json]

口径：
  · 只读是两层：只许单条 SELECT/WITH + 服务端 default_transaction_read_only=on；
  · 有行数上限与超时（在数据源定义里写死，调用方改不了）；
  · 密码只按名字引用（secret），值住 secrets 文件；
  · 结果不落真相源；查了什么、几行、多久，进 audit。
`

func cmdDB(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, dbUsage)
		return 2
	}
	switch args[0] {
	case "ls":
		return cmdDBLs(args[1:])
	case "query":
		return cmdDBQuery(args[1:])
	}
	fmt.Fprint(os.Stderr, dbUsage)
	return 2
}

func dsDir(vault string) string { return filepath.Join(filepath.Dir(vault), "datasources") }

func cmdDBLs(args []string) int {
	fs := flag.NewFlagSet("db ls", flag.ContinueOnError)
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 {
		fmt.Fprint(os.Stderr, dbUsage)
		return 2
	}
	ents, err := os.ReadDir(dsDir(posArgs[0]))
	if err != nil {
		fmt.Printf("还没有数据源（%s）\n", dsDir(posArgs[0]))
		return 0
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Println("还没有数据源。")
		return 0
	}
	for _, n := range names {
		s, err := ds.Load(posArgs[0], n)
		if err != nil {
			fmt.Printf("  ⚠️  %s：%v\n", n, err)
			continue
		}
		fmt.Printf("  %-16s %-10s 只读 上限 %d 行 超时 %s\n", s.Name, s.Type, s.MaxRows, firstNonEmpty(s.Timeout, "15s"))
	}
	return 0
}

func cmdDBQuery(args []string) int {
	fs := flag.NewFlagSet("db query", flag.ContinueOnError)
	sqlText := fs.String("sql", "", "要跑的查询（只读）")
	secretsFile := fs.String("secrets", "", "凭据文件（默认 <vault>/../secrets.env）")
	asJSON := fs.Bool("json", false, "JSON 出口")
	subject := fs.String("as", "anc", "以谁的身份查（策略按这个主体判）")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 2 || strings.TrimSpace(*sqlText) == "" {
		fmt.Fprint(os.Stderr, dbUsage)
		return 2
	}
	vault, name := posArgs[0], posArgs[1]
	src, err := ds.Load(vault, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	sec := *secretsFile
	if sec == "" {
		sec = filepath.Join(filepath.Dir(vault), "secrets.env")
		if _, err := os.Stat(sec); err != nil {
			home, _ := os.UserHomeDir()
			sec = filepath.Join(home, ".anc", "secrets.env")
		}
	}
	password := loadSecrets(sec)[src.Secret]
	if strings.TrimSpace(password) == "" {
		fmt.Fprintf(os.Stderr, "错误：密钥 %s 没配（%s）\n", src.Secret, sec)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := ds.Query(ctx, src, password, *sqlText)

	// 策略：默认拒绝；拒绝是整条查询失败，脱敏是保留列、替换值。
	pol, perr := policy.Load(filepath.Join(filepath.Dir(vault), "policies"), name)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "错误：策略读不动：%v\n", perr)
		return 1
	}
	var dec policy.Decision
	if err == nil {
		var aerr error
		dec, aerr = pol.Apply(*subject, columnTableMap(vault, name), knownColumns(vault, name), res.Columns, res.Rows)
		if aerr != nil {
			err = aerr
		}
	}
	at := time.Now().Format(time.RFC3339)
	result := audit.ResultOK
	why := fmt.Sprintf("%d 行 · %s · 主体 %s", res.Returned, res.Duration.Round(time.Millisecond), *subject)
	if len(dec.Masked) > 0 {
		why += " · 脱敏列 " + strings.Join(dec.Masked, ",")
	}
	if len(dec.Derived) > 0 {
		why += " · 派生列 " + strings.Join(dec.Derived, ",")
	}
	if err != nil {
		result, why = audit.ResultFailed, err.Error()
	}
	_, _ = audit.Append(vault, audit.Record{
		ID: fmt.Sprintf("db:%s:%d", name, time.Now().UnixNano()), At: at,
		Actor: *subject, Action: audit.ActionRead, Object: "ds:" + name,
		Result: result, Why: why, Tool: "db", Source: "db", Ref: res.SQLHash,
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if *asJSON {
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	fmt.Println(strings.Join(res.Columns, "\t"))
	for _, row := range res.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			if v == nil {
				cells[i] = "NULL"
				continue
			}
			cells[i] = *v
		}
		fmt.Println(strings.Join(cells, "\t"))
	}
	note := ""
	if res.Truncated {
		note = fmt.Sprintf("（截断到 %d 行）", res.Returned)
	}
	fmt.Fprintf(os.Stderr, "\n%d 行 · %s%s\n", res.Returned, res.Duration.Round(time.Millisecond), note)
	return 0
}

// knownColumns 是目录里出现过的列名集合（用来区分「派生列」与「策略外的列」）。
func knownColumns(vault, dsName string) map[string]bool {
	snap, err := catalog.Load(vault, dsName)
	if err != nil {
		return nil // 没有目录：所有列都按策略判（更严）
	}
	out := map[string]bool{}
	for _, t := range snap.Tables {
		for _, c := range t.Columns {
			out[c.Name] = true
		}
	}
	return out
}

func columnTableMap(vault, dsName string) map[string]string {
	snap, err := catalog.Load(vault, dsName)
	if err != nil {
		return nil
	}
	count := map[string]int{}
	owner := map[string]string{}
	for _, t := range snap.Tables {
		for _, c := range t.Columns {
			count[c.Name]++
			owner[c.Name] = t.Name
		}
	}
	out := map[string]string{}
	for col, n := range count {
		if n == 1 {
			out[col] = owner[col]
		}
	}
	return out
}

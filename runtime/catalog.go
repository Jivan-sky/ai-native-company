package main

// anc catalog —— 数据目录：扫 schema、看快照、比漂移。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/catalog"
	"anc/internal/ds"
)

const catalogUsage = `anc catalog —— 数据目录（只要元数据，不要行）

用法：
  anc catalog scan <vault> <数据源> [--secrets <文件>] [--json]
  anc catalog show <vault> <数据源> [--table <表>] [--json]
  anc catalog diff <vault> <数据源> [--secrets <文件>] [--json]

口径：
  · 内省只读 information_schema，不取行数据；
  · 快照带指纹（只看结构）；diff 会把「删列」与「改名」分开；
  · 分级建议只按列名规则，拍板要人确认（值形状扫描是下一刀）。
`

func cmdCatalog(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, catalogUsage)
		return 2
	}
	switch args[0] {
	case "scan":
		return cmdCatalogScan(args[1:])
	case "show":
		return cmdCatalogShow(args[1:])
	case "diff":
		return cmdCatalogDiff(args[1:])
	}
	fmt.Fprint(os.Stderr, catalogUsage)
	return 2
}

func catalogSecrets(vault, flagVal string) string {
	if strings.TrimSpace(flagVal) != "" {
		return flagVal
	}
	p := filepath.Join(filepath.Dir(vault), "secrets.env")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".anc", "secrets.env")
}

func doScan(vault, name, secrets string) (catalog.Snapshot, error) {
	src, err := ds.Load(vault, name)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	pw := loadSecrets(secrets)[src.Secret]
	if strings.TrimSpace(pw) == "" {
		return catalog.Snapshot{}, fmt.Errorf("密钥 %s 没配（%s）", src.Secret, secrets)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return catalog.Scan(ctx, src, pw)
}

func cmdCatalogScan(args []string) int {
	fs := flag.NewFlagSet("catalog scan", flag.ContinueOnError)
	secrets := fs.String("secrets", "", "凭据文件")
	asJSON := fs.Bool("json", false, "JSON 出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 2 {
		fmt.Fprint(os.Stderr, catalogUsage)
		return 2
	}
	vault, name := posArgs[0], posArgs[1]
	snap, err := doScan(vault, name, catalogSecrets(vault, *secrets))
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if err := catalog.Save(vault, snap); err != nil {
		fmt.Fprintf(os.Stderr, "错误：快照存不下来：%v\n", err)
		return 1
	}
	if *asJSON {
		b, _ := json.MarshalIndent(snap, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	cols := 0
	for _, t := range snap.Tables {
		cols += len(t.Columns)
	}
	fmt.Printf("✅ 已扫 %s：%d 张表 / %d 列 · 指纹 %s\n", name, len(snap.Tables), cols, snap.Fingerprint)
	for _, s := range catalog.Classify(snap) {
		fmt.Printf("   建议 %-10s %-18s %s（%s）\n", s.Level, s.Table+"."+s.Column, "", s.Why)
	}
	return 0
}

func cmdCatalogShow(args []string) int {
	fs := flag.NewFlagSet("catalog show", flag.ContinueOnError)
	table := fs.String("table", "", "只看一张表")
	asJSON := fs.Bool("json", false, "JSON 出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 2 {
		fmt.Fprint(os.Stderr, catalogUsage)
		return 2
	}
	snap, err := catalog.Load(posArgs[0], posArgs[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if *asJSON {
		b, _ := json.MarshalIndent(snap, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	fmt.Printf("目录 %s · 指纹 %s · 扫于 %s\n\n", snap.DataSource, snap.Fingerprint, snap.ScannedAt.Format(time.RFC3339))
	for _, t := range snap.Tables {
		if *table != "" && t.Name != *table {
			continue
		}
		fmt.Printf("  %s%s\n", t.Name, comment(t.Comment))
		for _, c := range t.Columns {
			pk := ""
			if c.IsPK {
				pk = " PK"
			}
			null := "NOT NULL"
			if c.Nullable {
				null = "NULL"
			}
			fmt.Printf("    %-20s %-18s %-8s%s%s\n", c.Name, c.Type, null, pk, comment(c.Comment))
		}
	}
	return 0
}

func cmdCatalogDiff(args []string) int {
	fs := flag.NewFlagSet("catalog diff", flag.ContinueOnError)
	secrets := fs.String("secrets", "", "凭据文件")
	asJSON := fs.Bool("json", false, "JSON 出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 2 {
		fmt.Fprint(os.Stderr, catalogUsage)
		return 2
	}
	vault, name := posArgs[0], posArgs[1]
	old, err := catalog.Load(vault, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v（先跑一次 scan）\n", err)
		return 1
	}
	cur, err := doScan(vault, name, catalogSecrets(vault, *secrets))
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	ch := catalog.Diff(old, cur)
	if *asJSON {
		b, _ := json.MarshalIndent(ch, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	if len(ch) == 0 {
		fmt.Printf("没有漂移（指纹 %s 未变）\n", cur.Fingerprint)
		return 0
	}
	fmt.Printf("发现 %d 处漂移（%s → %s）：\n", len(ch), old.Fingerprint, cur.Fingerprint)
	for _, c := range ch {
		fmt.Printf("  %-16s %s.%s %s\n", c.Kind, c.Table, c.Column, c.Detail)
	}
	return 0
}

func comment(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return "  — " + s
}

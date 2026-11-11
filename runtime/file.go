package main

// anc file —— 本地文件目录数据源：列目录 / 读文件（策略按路径前缀判）。

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"anc/internal/audit"
	"anc/internal/files"
	"anc/internal/policy"
)

const fileUsage = `anc file —— 本地文件目录（只读）

用法：
  anc file ls  <vault> <数据源> [路径] [--as <主体>]
  anc file cat <vault> <数据源> <路径> [--as <主体>] [--max-bytes N]

口径：
  · 只读；不跟随符号链接；「../」与绝对路径一律拒绝；
  · 策略按**路径前缀**判（规则 table 写成 "10-knowledge/*"）；
  · 读文件有字节上限，超了明说「截断」。
`

func cmdFile(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, fileUsage)
		return 2
	}
	switch args[0] {
	case "ls":
		return cmdFileLs(args[1:])
	case "cat":
		return cmdFileCat(args[1:])
	}
	fmt.Fprint(os.Stderr, fileUsage)
	return 2
}

func filePolicy(vault, ds, subject, path string) (bool, string) {
	pol, err := policy.Load(filepath.Join(filepath.Dir(vault), "policies"), ds)
	if err != nil {
		return false, "策略读不动：" + err.Error()
	}
	switch pol.Action(subject, path, "*") {
	case "allow":
		return true, ""
	case "mask":
		return false, "该路径按策略是脱敏，文件内容不支持脱敏"
	default:
		return false, "策略拒绝：" + subject + " 不能访问 " + path
	}
}

func fileAudit(vault, ds, subject, path, result, why string) {
	_, _ = audit.Append(vault, audit.Record{
		ID: fmt.Sprintf("file:%s:%d", ds, time.Now().UnixNano()), At: time.Now().Format(time.RFC3339),
		Actor: subject, Action: audit.ActionRead, Object: "file:" + ds + ":" + path,
		Result: result, Why: why, Tool: "file", Source: "files",
	})
}

func cmdFileLs(args []string) int {
	fs := flag.NewFlagSet("file ls", flag.ContinueOnError)
	as := fs.String("as", "anc", "以谁的身份看")
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) < 2 || len(posArgs) > 3 {
		fmt.Fprint(os.Stderr, fileUsage)
		return 2
	}
	vault, name := posArgs[0], posArgs[1]
	rel := ""
	if len(posArgs) == 3 {
		rel = posArgs[2]
	}
	src, err := files.Load(vault, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	check := rel
	if check == "" {
		check = "."
	}
	if ok, why := filePolicy(vault, name, *as, check); !ok {
		fmt.Fprintf(os.Stderr, "错误：%s\n", why)
		fileAudit(vault, name, *as, check, audit.ResultDenied, why)
		return 1
	}
	ents, truncated, err := files.List(src, rel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	// 逐项再判：父目录允许，不代表子项允许
	var shown []files.Entry
	for _, e := range ents {
		p := e.Path
		if e.Dir {
			p += "/"
		}
		if ok, _ := filePolicy(vault, name, *as, p); ok {
			shown = append(shown, e)
		}
	}
	sort.Slice(shown, func(i, j int) bool { return shown[i].Path < shown[j].Path })
	for _, e := range shown {
		kind := "文件"
		if e.Dir {
			kind = "目录"
		}
		link := ""
		if e.Link {
			link = " → 软链（不跟随）"
		}
		fmt.Printf("  %-4s %-8d %s  %s%s\n", kind, e.Size, e.MTime, e.Path, link)
	}
	note := ""
	if truncated {
		note = "（条目达到上限，已截断）"
	}
	fmt.Fprintf(os.Stderr, "\n%d 项%s\n", len(shown), note)
	fileAudit(vault, name, *as, check, audit.ResultOK, fmt.Sprintf("%d 项", len(shown)))
	return 0
}

func cmdFileCat(args []string) int {
	fs := flag.NewFlagSet("file cat", flag.ContinueOnError)
	as := fs.String("as", "anc", "以谁的身份看")
	maxBytes := fs.Int("max-bytes", 0, "覆盖数据源里的字节上限")
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 3 {
		fmt.Fprint(os.Stderr, fileUsage)
		return 2
	}
	vault, name, rel := posArgs[0], posArgs[1], posArgs[2]
	src, err := files.Load(vault, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	if *maxBytes > 0 {
		src.MaxBytes = *maxBytes
	}
	if ok, why := filePolicy(vault, name, *as, rel); !ok {
		fmt.Fprintf(os.Stderr, "错误：%s\n", why)
		fileAudit(vault, name, *as, rel, audit.ResultDenied, why)
		return 1
	}
	b, truncated, err := files.Read(src, rel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	os.Stdout.Write(b)
	if truncated {
		fmt.Fprintf(os.Stderr, "\n…（已截断到 %d 字节）\n", len(b))
	}
	fileAudit(vault, name, *as, rel, audit.ResultOK, fmt.Sprintf("%d 字节%s", len(b), map[bool]string{true: "（截断）", false: ""}[truncated]))
	return 0
}

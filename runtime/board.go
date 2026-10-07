package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"anc/internal/board"
	"anc/internal/org"
)

const boardUsage = `anc org export —— org 真相源 → 看板消费的只读 JSON

用法：
  anc org export <vault 目录>

打到 stdout（消费方自己重定向）；非红档发现走 stderr，不污染 JSON。
这是**只读出口**：不写真相源、不碰机器、不生成任何能给 gateway 吃的东西。

刻意不导出 feishu.app_id / open_id —— 看板是观测面，不是凭据面（SPEC §2.3 / §6-3）。
`

// cmdOrgExport 做投影，不做任何写操作。
func cmdOrgExport(args []string) int {
	fs := flag.NewFlagSet("org export", flag.ContinueOnError)
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 {
		fmt.Fprint(os.Stderr, boardUsage)
		return 2
	}
	abs, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 2
	}
	o, err := org.Load(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 1
	}
	text, err := board.Of(o, time.Now()).JSON()
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 1
	}
	printIssuesTo(os.Stderr, o.Warnings)
	fmt.Print(text)
	return 0
}

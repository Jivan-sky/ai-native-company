package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"

	"anc/internal/board"
	"anc/internal/org"
)

const boardServeUsage = `anc board serve —— 起 ANC 看板（只读）

用法：
  anc board serve <vault 目录> [--addr 127.0.0.1:8787] [--data <data 目录>]

看板读的是真相源（vault）的**实时投影**：改完 vault 刷新即见，不缓存。
前端（手写的 html + 构建期打出的 js）已经嵌在这个二进制里 —— 不需要 node、不需要外网。

选项：
  --addr <host:port>   监听地址（默认只绑本机 127.0.0.1；绑到别处会打警告）
  --data <目录>        gateway 的 data 目录（默认 <vault>/../data）—— 给了它，「运行态」
                       页才接得上；默认口径同 anc probe 命令，两边看的是同一份现场。

安全口径：
  - 只读：只放行 GET / HEAD，没有任何写入口；
  - 默认只绑本机 —— 看板里有组织与项目信息，要给别人看请走 ssh 端口转发，
    别把 0.0.0.0 直接开出去；
  - 投影里不含凭据面字段（feishu app_id / open_id 等一律不进）。

退出：Ctrl-C。
`

// cmdBoardServe 起只读看板。它不写真相源、不装载服务 —— 与阶段 C 的 `anc serve` 不是同一件事。
func cmdBoardServe(args []string) int {
	fs := flag.NewFlagSet("board serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8787", "监听地址")
	data := fs.String("data", "", "gateway data 目录（运行态页用）")
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 {
		fmt.Fprint(os.Stderr, boardServeUsage)
		return 2
	}
	abs, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 2
	}
	// 路径写错就别起服务（起一个只会 500 的服务是骗人）；内容有问题则照起 ——
	// 看板正是用来看「哪里坏了」的，前端会把红档摆在最上面。
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "错误：vault 目录不存在或不是目录：%s\n", abs)
		return 1
	}
	// 运行态目录：默认与 `anc probe` 同一口径（<vault>/../data）。
	// 目录不在**不拦**看板 —— 真相源投影仍然要看得到；运行态页会自己报「没接上」。
	dataDir := *data
	if dataDir == "" {
		dataDir = filepath.Join(filepath.Dir(abs), "data")
	} else if a, err := filepath.Abs(dataDir); err == nil {
		dataDir = a
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：监听 %s 失败：%v\n", *addr, err)
		return 1
	}
	defer func() { _ = ln.Close() }()

	if o, err := org.Load(abs); err == nil {
		printIssues(o.Warnings)
	} else {
		fmt.Fprintf(os.Stderr, "⚠️  真相源现在有红档发现（看板会把它摆在最上面）：\n%v\n", err)
	}

	urlHost := ln.Addr().String()
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		if tcp.IP.IsUnspecified() {
			urlHost = net.JoinHostPort("127.0.0.1", strconv.Itoa(tcp.Port))
		}
		if !tcp.IP.IsLoopback() {
			fmt.Fprintf(os.Stderr,
				"⚠️  绑到了 %s：看板里有组织与项目信息，只该给本机、或经 ssh 端口转发的人看。\n", ln.Addr())
		}
	}

	fmt.Printf("ANC 看板已起（只读）：\n")
	fmt.Printf("  地址   http://%s\n", urlHost)
	fmt.Printf("  vault  %s\n", abs)
	if st, err := os.Stat(dataDir); err == nil && st.IsDir() {
		fmt.Printf("  data   %s（运行态页已接入）\n", dataDir)
	} else {
		fmt.Printf("  data   %s（没有这个目录 —— 运行态页会报「没接上」）\n", dataDir)
	}
	fmt.Printf("  退出   Ctrl-C\n\n")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	srv := &http.Server{
		Handler:           (&board.Server{Vault: abs, DataDir: dataDir, Now: time.Now}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		fmt.Println("\n收到中断，正在关闭看板…")
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			fmt.Fprintf(os.Stderr, "关闭时出错：%v\n", err)
			return 1
		}
		return 0
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
}

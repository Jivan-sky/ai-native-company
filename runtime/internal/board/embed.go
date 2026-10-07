package board

import (
	"embed"
	"io/fs"
)

// UI 是看板前端的**构建期产物**：手写的 index.html + esbuild 打出来的 app.js。
//
// 嵌进二进制是刻意的：看板要能在客户机器上「一个 exe 就起来」——
// 前端源码用 TS（浏览器生态的本体语言），但只在**构建期**存在；
// 交付物是纯静态文件，运行时不需要 node、不需要外网、不多带一个运行时。
//
// 改了 front 端源码要重新生成 app.js（见 ../boardui/README 或 package.json 的 build 脚本），
// 并把产物一起提交 —— 否则二进制里的还是旧界面。
//
//go:embed ui
var UI embed.FS

// UIFS 去掉 ui/ 前缀，直接交给 http.FileServerFS。
func UIFS() fs.FS {
	sub, err := fs.Sub(UI, "ui")
	if err != nil {
		// 路径是编译期常量：这里失败说明 embed 配置坏了，与运行时输入无关。
		panic("board: 内嵌 UI 目录缺失: " + err.Error())
	}
	return sub
}

package board

import (
	"io"
	"net/http"
)

// /next —— 新前端（React + AntD）的实验入口。
//
// 为什么要一个独立入口：换框架是一次重写，不能大爆炸。旧界面原样保留在 /，
// 新界面一页一页搬到 /next，每搬完一页跑一遍不变量检查；全搬完再互换。
//
// 现在只有一页（审计）。它和新界面共用同一个 /api/*，契约一个字节没改。
const nextHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>ANC · 新前端实验</title>
<style>html,body{margin:0;background:#f5f7fa}</style>
</head><body><div id="root"></div>
<script>window.__errs=[];window.onerror=function(m,src,l,c,e){window.__errs.push(String(m)+" @"+(src||"")+":"+l);};window.addEventListener("unhandledrejection",function(e){window.__errs.push("promise: "+String(e.reason&&e.reason.message||e.reason));});</script>
<script type="module" src="/app-next.js"></script></body></html>`

func (s *Server) handleNext(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, nextHTML)
}

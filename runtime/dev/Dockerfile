# ANC 开发与验证镜像 —— **只在开发期用**，不进交付物。
#
# 交付物是 dist/ 里六个平台的单 exe（build.ps1 打），这个镜像不参与打包。
#
# 为什么要在容器里跑 Go：
#   1. 交付物是三平台单 exe，验证腿必须能复现「同一版 Go + Linux」。宿主是 macOS，
#      本机跑绿不等于 Linux 腿绿（2026-10-19 那条「macOS 暂不支持」就是缺这条腿）。
#   2. Go 版本钉在这一个文件里，换机器不再出现「我这儿是绿的」。
FROM golang:1.25

# 交付物是纯 Go、无 cgo（交叉编译六个目标的前提），开发期保持同一口径。
# GOTOOLCHAIN=local：不许镜像偷偷下载别的工具链 —— 版本只认这一行。
ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local

WORKDIR /src

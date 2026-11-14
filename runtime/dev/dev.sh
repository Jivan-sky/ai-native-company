#!/usr/bin/env bash
# ANC 开发验证入口（宿主机一侧）。宿主机不需要装 Go —— 所有 Go 动作都在容器里跑。
#
# 为什么是容器：交付物是三平台单 exe，验证腿必须能复现「同一版 Go + Linux」。
# 本机 macOS 上装的 Go 跑绿，不代表 Linux 腿绿；版本也会随机器漂。容器把这两件事都钉死。
#
# 用法：./dev.sh <命令>
#
#   ui        真浏览器跑一遍界面不变量（需要 ego-browser，且看板已在跑）
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
COMPOSE=(docker compose -f "$HERE/compose.yaml")
BOX="$ROOT/.devbox"
LOG="$BOX/log"

die() { printf '错误：%s\n' "$*" >&2; exit 1; }

usage() {
	cat <<'EOF'
anc 开发验证入口（容器里跑，宿主只装 docker）

  ./dev.sh fmt       gofmt 检查（有未格式化的文件就红）
  ./dev.sh vet       go vet
  ./dev.sh test      go test -count=1 ./...
  ./dev.sh build     编译到 .devbox/bin/anc（Linux 二进制，宿主不执行）
  ./dev.sh cross     六个目标交叉编译（linux/darwin/windows × amd64/arm64）
  ./dev.sh smoke     端到端烟测：init → org init → render → 投影 → 热层 → 看板
  ./dev.sh bench     「不问人能不能拿到」的测量：在沙箱 vault + suite-v0 上跑一遍
  ./dev.sh negctl    负控：把 vault 每次弄坏一处，分数必须掉（证明它会说「不」）
  ./dev.sh calib     标定：黄金样本（sha256 + 期望值）+ 故障注入，读取器一漂就红
  ./dev.sh all       fmt + vet + test + cross + smoke（交付前跑这一条）
  ./dev.sh board     起常驻看板（redis + board），地址打在下面
  ./dev.sh down      停掉 redis / board
  ./dev.sh clean     删 .devbox/（二进制、沙箱 vault、日志全在这一个目录里）
  ./dev.sh sh        进容器交互 shell（挂载同 dev 服务）

产出都在 .devbox/：bin/anc、sandbox/、cross/、log/。仓库本身不动（源码是只读挂载）。
EOF
}

[[ $# -eq 1 ]] || { usage; exit 2; }

command -v docker >/dev/null || die "找不到 docker（装上 Docker Desktop 再来）"
docker info >/dev/null 2>&1 || die "docker 守护进程没起来（Docker Desktop 启动了吗）"

mkdir -p "$BOX" "$LOG"

# 所有一次性命令都走这里：同一个服务定义，同一份挂载，不各写一遍。
in_dev() { "${COMPOSE[@]}" run --rm dev sh -c "$1"; }

case "$1" in
  types)
    # 契约漂移检查：拿真服务返回核对前端类型（不引任何运行时，只在开发期跑）
    node "$HERE/gen-types.mjs"
    ;;

  ui)
    ego-browser nodejs < "$HERE/ui-invariants.mjs"
    ;;fmt)
	in_dev '
    out=$(gofmt -l . 2>&1)
    if [ -n "$out" ]; then
      printf "以下文件没格式化：\n%s\n" "$out"
      exit 1
    fi
    echo "✅ gofmt 干净"
  '
	;;
vet)
	in_dev 'go vet ./... && echo "✅ go vet 通过"'
	;;
test)
	in_dev 'go test -count=1 ./...'
	;;
build)
	in_dev 'mkdir -p /out/bin && go build -trimpath -o /out/bin/anc . && ls -l /out/bin/anc'
	;;
cross)
	in_dev '
    set -e
    mkdir -p /out/cross
    for os in linux darwin windows; do
      for arch in amd64 arm64; do
        ext=""
        [ "$os" = windows ] && ext=".exe"
        GOOS=$os GOARCH=$arch go build -trimpath -o "/out/cross/$os-$arch/anc$ext" .
        printf "  ✅ %s/%s\n" "$os" "$arch"
      done
    done
    echo "✅ 六个目标交叉编译通过"
  '
	;;
smoke)
	in_dev 'sh /src/dev/smoke.sh'
	;;
bench)
	in_dev '
    set -e
    (cd /src && go build -trimpath -o /out/bin/anc .)
    [ -d /out/sandbox/acme ] || { echo "还没有沙箱 vault —— 先跑 ./dev.sh smoke"; exit 1; }
    sh /src/testdata/bench/seed-v0.sh /out/sandbox/acme
    /out/bin/anc bench /out/sandbox/acme --suite /src/testdata/bench/suite-v0
  '
	;;
negctl)
	in_dev 'sh /src/dev/negctl.sh'
	;;
calib)
	in_dev 'go test -count=1 -run "Calib|Fault" ./internal/trail'
	printf '\n真记录对拍（可选，需要本机有记录与 zstd）：\n'
	printf '  dev/calib/compare.sh <vault> <config> <harness> <记录根> <anc 二进制>\n'
	printf '  dev/calib/invariance.py <vault> <config> <harness> <记录根> <anc 二进制>\n'
	;;
all)
	"$0" fmt
	"$0" vet
	"$0" test
	"$0" cross
	"$0" smoke
	;;
board)
	[[ -f "$BOX/bin/anc" ]] || die "还没有 .devbox/bin/anc —— 先跑 ./dev.sh smoke"
	[[ -d "$BOX/sandbox/acme" ]] || die "还没有 .devbox/sandbox/acme —— 先跑 ./dev.sh smoke"
	"${COMPOSE[@]}" up -d redis board
	printf '\n看板： http://127.0.0.1:%s/\n停掉： ./dev.sh down\n' "${ANC_BOARD_PORT:-8787}"
	;;
down)
	"${COMPOSE[@]}" down --remove-orphans
	;;
clean)
	# 删之前先核对绝对路径：只允许删本工程下的 .devbox。
	[[ "$BOX" == "$ROOT/.devbox" ]] || die "拒绝清理：$BOX 不是 $ROOT/.devbox"
	rm -rf "$BOX"
	echo "✅ 已删 $BOX"
	;;
sh)
	"${COMPOSE[@]}" run --rm dev sh
	;;
*)
	usage
	exit 2
	;;
esac

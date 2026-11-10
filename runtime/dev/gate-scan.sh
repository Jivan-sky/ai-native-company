#!/bin/sh
# 授权闸验收：模型出口**无票必须被拒**。现在应当是红的（闸还没接）。
#
# 用法：gate-scan.sh <base-url>
#
# 三条：
#   1. POST /api/chat 不带票 → 401/403
#   2. POST /api/chat 带假票 → 401/403
#   3. 上面两条任一返回 2xx/501 → 说明「这条路根本没有闸」
set -eu
BASE=${1:?用法：gate-scan.sh <base-url>}
fail=0

check() {
	name=$1; shift
	code=$(curl -sS -o /dev/null -w '%{http_code}' "$@" 2>/dev/null) || code=000
	case "$code" in
	401|403) echo "✅ ${name} → ${code}（拒了）" ;;
	*) echo "❌ ${name} → ${code}（应当 401/403；2xx 或 501 都说明没有闸）"; fail=1 ;;
	esac
}

check "无票" -X POST -H 'Content-Type: application/json' -d '{"prompt":"x"}' "$BASE/api/chat"
check "假票" -X POST -H 'Content-Type: application/json' -H 'Authorization: Bearer not-a-real-token' \
	-d '{"prompt":"x"}' "$BASE/api/chat"

if [ "$fail" -ne 0 ]; then
	echo "结论：不通过 —— 模型出口还没有授权闸（P0 未落地）"
	exit 1
fi
echo "结论：通过 —— 无票/假票都进不来"

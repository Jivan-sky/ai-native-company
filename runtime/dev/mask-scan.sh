#!/bin/sh
# 打码验收：往注册表里放一个**真密钥**，然后把所有出口扫一遍 —— 出现原文一次即失败。
#
# 这是 P0 的验收脚本，设计成**先在红、后在绿**：
#   · 现在（还没有 /api/models 这条路）它必须红，红的原因是「这条路不存在」；
#   · broker 落地之后跑同一份脚本，红的原因就只剩「真的泄漏了」。
#
# 用法：mask-scan.sh <base-url> <vault> [--secrets <文件>] [--scan <目录> ...]
#
# 检查三件：
#   1. GET <base>/api/models 存在且 200（能力在不在）
#   2. 该响应体里**不含** secrets 里的任何密钥值
#   3. --scan 给的目录（默认 vault）里所有文本文件**不含**任何密钥值
set -eu

BASE=${1:?用法：mask-scan.sh <base-url> <vault> [--secrets <文件>] [--scan <目录> ...]}
VAULT=${2:?}
shift 2

SECRETS=""
SCAN=""
while [ $# -gt 0 ]; do
	case "$1" in
	--secrets) SECRETS=$2; shift 2 ;;
	--scan) SCAN="$SCAN $2"; shift 2 ;;
	*) echo "未知参数：$1" >&2; exit 2 ;;
	esac
done
[ -n "$SCAN" ] || SCAN=$VAULT
if [ -z "$SECRETS" ]; then
	SECRETS="$HOME/.anc/secrets.env"
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fail_capability=0
fail_leak=0

# 1) 能力在不在
code=$(curl -sS -o "$TMP/models.json" -w '%{http_code}' "$BASE/api/models" 2>/dev/null) || code=000
[ -n "$code" ] || code=000
if [ "$code" != "200" ]; then
	echo "❌ /api/models HTTP $code —— 这条路还不存在（P0 未落地）"
	fail_capability=1
else
	echo "✅ /api/models 200"
fi

# 收集密钥值（只收长度 >= 8 的，避免把空值/短串当泄漏）
: >"$TMP/values"
if [ -f "$SECRETS" ]; then
	python3 - "$SECRETS" "$TMP/values" <<'PY'
import re, sys
src, out = sys.argv[1], sys.argv[2]
vals = []
for line in open(src, encoding="utf-8", errors="replace"):
    line = line.strip()
    if not line or line.startswith("#") or "=" not in line:
        continue
    v = line.split("=", 1)[1].strip().strip('"').strip("'")
    if len(v) >= 8:
        vals.append(v)
open(out, "w").write("\n".join(vals))
PY
else
	echo "… 没有 secrets 文件（$SECRETS）：本次只验能力，不验泄漏"
fi

# 2) 响应体不许有原文
if [ -s "$TMP/values" ] && [ "$fail_capability" -eq 0 ]; then
	if python3 - "$TMP/models.json" "$TMP/values" <<'PY'
import sys, pathlib
body = pathlib.Path(sys.argv[1]).read_text(errors="replace")
vals = [v for v in pathlib.Path(sys.argv[2]).read_text().splitlines() if v]
bad = [v for v in vals if v in body]
if bad:
    print(f"❌ /api/models 里出现了 {len(bad)} 个密钥原文")
    sys.exit(1)
print("✅ /api/models 只出现打码形态")
PY
	then :; else fail_leak=1; fi
fi

# 3) 落盘目录不许有原文
if [ -s "$TMP/values" ]; then
	if python3 - $TMP/values $SCAN <<'PY'
import sys, pathlib
vals = [v for v in pathlib.Path(sys.argv[1]).read_text().splitlines() if v]
roots = sys.argv[2:]
hits = []
for root in roots:
    for p in pathlib.Path(root).rglob("*"):
        if not p.is_file() or p.stat().st_size > 2_000_000:
            continue
        try:
            txt = p.read_text(errors="ignore")
        except Exception:
            continue
        for v in vals:
            if v in txt:
                hits.append((str(p), v[:4] + "…"))
if hits:
    print(f"❌ 落盘里发现 {len(hits)} 处密钥原文（文件, 前缀）：")
    for f, pre in hits[:5]:
        print(f"   {f}  {pre}")
    sys.exit(1)
print(f"✅ 落盘 {len(roots)} 个目录里没有密钥原文")
PY
	then :; else fail_leak=1; fi
fi

if [ "$fail_capability" -ne 0 ] || [ "$fail_leak" -ne 0 ]; then
	echo "结论：不通过（能力缺失=${fail_capability} · 泄漏=${fail_leak}）"
	exit 1
fi
echo "结论：通过 —— 打码路径成立"

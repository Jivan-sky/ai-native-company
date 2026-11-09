#!/bin/sh
# 标定：独立神谕 vs `anc` 的两面（按行使的台账 / 按会话的 trail），两组数各自对拍。
#
#   1) 工具调用计数：神谕（只认字节）vs `anc audit collect --json`，按工具名逐项比
#   2) token 账：神谕 vs `anc trail --json` 的 totals，四类字段逐项比
#
# 两组都绿才算这条链路「读得对」。任一不一致就退出码 1，并把差异打到屏幕。
#
# 现在**没**做的事（别把这份输出当成标定完成）：不变性（重压缩/重排/换架构）、
# 语料统计（每家只有 1 份记录）、第三方 manifest。见 DESIGN §7.1.84。
#
# 用法：compare.sh <vault> <gateway config> <harness> <记录根> [anc 二进制]
set -eu

VAULT=${1:?用法：compare.sh <vault> <config> <harness> <记录根> [anc]}
CFG=${2:?}
HARNESS=${3:?}
ROOT=${4:?}
BIN=${5:-/out/bin/anc}
HERE=$(cd "$(dirname "$0")" && pwd)

fail=0

# ---------- 1) 工具调用计数 ----------
o=$(python3 "$HERE/oracle.py" "$ROOT")
a=$("$BIN" audit collect "$VAULT" --harness "$HARNESS" --claude-home "$ROOT" --json 2>/dev/null |
	python3 -c 'import json,sys,collections; d=json.load(sys.stdin); c=collections.Counter(r["tool"] for r in d["records"]); print(json.dumps({"total":len(d["records"]),"by_tool":dict(c)},ensure_ascii=False,sort_keys=True))')
printf '  行使  神谕 %s\n' "$o"
printf '        台账 %s\n' "$a"
python3 - "$o" "$a" <<'PY' || fail=1
import json, sys
a, b = json.loads(sys.argv[1]), json.loads(sys.argv[2])
if a != b:
    print("  ⚠️ 计数不一致：")
    for k in sorted(set(a["by_tool"]) | set(b["by_tool"])):
        x, y = a["by_tool"].get(k, 0), b["by_tool"].get(k, 0)
        if x != y:
            print(f"     {k}: 记录 {x} / 台账 {y}")
    sys.exit(1)
PY

# ---------- 2) token 账 ----------
u=$(python3 "$HERE/usage.py" "$ROOT" "$HARNESS")
t=$("$BIN" trail "$VAULT" --harness "$HARNESS" --claude-home "$ROOT" --config "$CFG" --json 2>/dev/null |
	python3 -c 'import json,sys; d=json.load(sys.stdin); t=d["totals"]; print(json.dumps({"in":t["in"],"out":t["out"],"cache_read":t["cache_read"],"cache_write":t["cache_write"]},sort_keys=True))')
printf '  token 神谕 %s\n' "$u"
printf '        trail %s\n' "$t"
python3 - "$u" "$t" <<'PY' || fail=1
import json, sys
a, b = json.loads(sys.argv[1]), json.loads(sys.argv[2])
bad = {k: (a.get(k), b.get(k)) for k in ("in", "out", "cache_read", "cache_write") if a.get(k) != b.get(k)}
if bad:
    print("  ⚠️ token 账不一致：", bad)
    sys.exit(1)
PY

if [ "$fail" -eq 0 ]; then
	echo "  ✅ ${HARNESS}：计数与 token 账都一致"
else
	echo "  ❌ ${HARNESS}：有差异（见上）"
fi
exit "$fail"

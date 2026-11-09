#!/bin/sh
# 标定第一刀：**工具调用计数**的独立对拍。
#
# 神谕（oracle.py）只认字节、不认 anc 的代码；台账那边跑的是 `anc audit collect --json`。
# 两边按「工具名 → 次数」逐项比；不一致就红。
#
# 现在只对拍了这一组数（工具调用）。token 账、轮数、失败数还没进这个脚本 ——
# 见 DESIGN §7.1.84 的「没做的」，别把这份输出当成「标定完成」。
#
# 用法：compare.sh <vault> <harness> <记录根> [anc 二进制]
set -eu

VAULT=${1:?用法：compare.sh <vault> <harness> <记录根> [anc 二进制]}
HARNESS=${2:?}
ROOT=${3:?}
BIN=${4:-/out/bin/anc}

HERE=$(cd "$(dirname "$0")" && pwd)

oracle_total=0
oracle_json=$(python3 "$HERE/oracle.py" "$ROOT" | python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin), ensure_ascii=False, sort_keys=True))')
anc_json=$("$BIN" audit collect "$VAULT" --harness "$HARNESS" --claude-home "$ROOT" --json 2>/dev/null | python3 -c '
import json,sys,collections
d=json.load(sys.stdin); c=collections.Counter(r["tool"] for r in d["records"])
print(json.dumps({"total":len(d["records"]),"by_tool":dict(c)}, ensure_ascii=False, sort_keys=True))')

printf '神谕  %s\n台账  %s\n' "$oracle_json" "$anc_json"
python3 - "$oracle_json" "$anc_json" <<'PY'
import json, sys
a, b = json.loads(sys.argv[1]), json.loads(sys.argv[2])
if a == b:
    print("✅ 逐工具一致（%d 次）" % a["total"])
    sys.exit(0)
print("⚠️ 不一致：")
ok = True
for k in sorted(set(a["by_tool"]) | set(b["by_tool"])):
    x, y = a["by_tool"].get(k, 0), b["by_tool"].get(k, 0)
    if x != y:
        ok = False
        print(f"   {k}: 记录 {x} / 台账 {y}")
sys.exit(0 if ok else 1)
PY

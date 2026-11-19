#!/usr/bin/env bash
# 权限判定点计数（治理判据 2）。
#
# 为什么要有它：判定点散落不是一次事故，是**每加一个功能就多一处**的自然结果。
# 光靠"我们以后注意"挡不住；要让它变成一次构建失败。
#
# 规则：
#   1 判 allow/deny 的函数必须带 `//go:authz-entry <轴名>` 标记；
#   2 标记总数必须等于白名单里的条目数（多一处 = 有人新开了判定点，没登记）；
#   3 白名单里每一条必须写明「为什么它是独立的一轴」—— 没有理由的条目不许进。
#
# 用法：bash dev/authz-entry.sh
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 2

# 白名单：文件:函数 | 轴 | 为什么它独立存在
WHITELIST=(
  "internal/org/authority.go:Exercise|委派轴|跨域授权在那一刻是否可行使（撤回/有效期/签发者/转授链）"
  "internal/org/capability.go:Allow|岗位轴|角色×模块×动作，来源 capabilities.md"
  "internal/org/access.go:Allows|可见性轴|这次能看见哪些域；全项目唯一实现"
  "internal/board/fileview.go:fileAllowed|数据策略轴|某个数据源某个路径：allow/mask/deny"
  "internal/policy/policy.go:Action|数据策略引擎|策略表逐格判定，默认拒"
)

have=$(grep -rn "^//go:authz-entry" --include=*.go . | wc -l | tr -d ' ')
want=${#WHITELIST[@]}
fail=0

echo "== 判定点：标记 ${have} 个 · 白名单 ${want} 条 =="

# 1 每个白名单条目都要真的被标记
for entry in "${WHITELIST[@]}"; do
  loc="${entry%%|*}"
  file="${loc%%:*}"; fn="${loc##*:}"
  if ! grep -q "//go:authz-entry" "$file" 2>/dev/null; then
    echo "❌ $file 没有 authz-entry 标记（白名单里有它）"; fail=1
  elif ! grep -q "func .*${fn}(" "$file" 2>/dev/null; then
    echo "❌ $file 里找不到函数 ${fn} —— 白名单过期了？"; fail=1
  fi
done

# 2 数量必须相等（多一处 = 新判定点没登记）
if [ "$have" -ne "$want" ]; then
  echo "❌ 标记数 ${have} ≠ 白名单 ${want} —— 有人新增或删除了判定点，必须显式登记"
  echo "   新增的："
  grep -rn "^//go:authz-entry" --include=*.go . | sed 's/^/     /'
  fail=1
fi

# 3 自测：白名单机制本身不能失效
if [ "${#WHITELIST[@]}" -eq 0 ]; then
  echo "❌ 自测失败：白名单是空的，这个检查等于没开"; exit 3
fi

if [ "$fail" -eq 0 ]; then
  echo "✅ 判定点与白名单一致（每一轴都有且仅有一处实现，且都写了为什么）"
fi
exit "$fail"

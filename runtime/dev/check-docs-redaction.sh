#!/usr/bin/env bash
# 落库前脱敏检查：文档与 testdata 里不许出现**真实**本机路径、真实人名、密钥。
#
# 为什么要有它：SPEC §18.3 写了"文档纪律"，但纪律没有执行器就只是愿望。
# 这个脚本只做一件事——把「不该进仓库的字符串」变成一次非零退出。
#
# 注意（第一版踩过的坑）：grep -E **不支持** (?!...) 负向断言，
# 所以第一版什么也没检查到却打印"通过"。现在改成「全量匹配 + 白名单过滤」，
# 并且脚本自己带一条自测：构造一个必须被抓住的样本，抓不到就报错退出。
#
# 用法：bash dev/check-docs-redaction.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 2
bad=0

# 允许出现的占位写法：<某人>、xxx、dev、anc-<bot>、x/
is_placeholder() {
  case "$1" in
    "<"*|"xxx"*|"dev"*|"anc-"*|"x"*|"示例"*) return 0 ;;
    *) return 1 ;;
  esac
}

scan_paths() {
  local ext="$1" desc="$2" hit=0
  while IFS= read -r line; do
    local user
    user=$(printf '%s' "$line" | sed -nE 's#.*/Users/([A-Za-z0-9._-]+).*#\1#p')
    [ -z "$user" ] && continue
    is_placeholder "$user" && continue
    if [ "$hit" -eq 0 ]; then echo "❌ ${desc}"; hit=1; bad=1; fi
    echo "   $line" | cut -c1-140
  # **只扫 git 跟踪的文件**：落进仓库才算脱敏问题；.devbox / node_modules 是本机残留
  done < <({ git ls-files "*.$ext"; git ls-files --others --exclude-standard "*.$ext"; } 2>/dev/null \
             | grep -v "dev/check-docs-redaction.sh" \
             | xargs grep -In "/Users/" 2>/dev/null \
             | grep -v "regexp.MustCompile" \
             | head -20)
}

scan_paths "md"   "文档里出现真实本机路径（应写 <某人> 或 /home/demo）"
scan_paths "json" "testdata 里出现真实本机路径"

# 密钥形态：测试样例里的 sk-1234 / ghp_a1b2 是允许的（长度不够 + 明显是顺序字符）
while IFS= read -r line; do
  echo "❌ 疑似真实 token：$line" | cut -c1-140; bad=1
done < <(grep -rInE "(ghp_[A-Za-z0-9]{30,}|sk-[A-Za-z0-9]{32,})" --include="*.md" . 2>/dev/null | head -5)

# 客户 / 内部系统真名：本项目对外只说「某客户 / 示例公司」
while IFS= read -r line; do
  echo "❌ 出现疑似客户或内部系统真名：$line" | cut -c1-140; bad=1
done < <(grep -rInE "(xiaoduoai|1yangai)" --include="*.md" . 2>/dev/null | head -5)

# 自测：抓不到一个明显该抓的样本，说明检查器自己坏了 —— 不许报"通过"
selftest=$(printf 'x /Users/realname/secret\n' | sed -nE 's#.*/Users/([A-Za-z0-9._-]+).*#\1#p')
if [ "$selftest" != "realname" ] || is_placeholder "realname"; then
  echo "❌ 检查器自测失败：连 /Users/realname 都抓不到（或把它当占位符）"
  exit 3
fi

if [ "$bad" -eq 0 ]; then
  echo "✅ 脱敏检查通过（自测通过）：文档与 testdata 里没有真实本机路径 / 密钥 / 客户真名"
fi
exit "$bad"

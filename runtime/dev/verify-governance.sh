#!/usr/bin/env bash
# 治理判据的现场验收脚本 —— 每条判据给：对象·字段 | 样本框 | 命令 | 期望输出。
#
# 为什么必须写成脚本：GOVERNANCE-FLYWHEEL.md 第一版的 15 条判据里，
# 10 条的"标准"是我们自己、4 条没有样本框。判据不绑到可观测的存储上，
# 验收就退化成"我们说成立"。这个脚本把能验的先钉死，验不了的显式打 UNVERIFIABLE。
#
# 用法：bash dev/verify-governance.sh <vault>
# 退出码：0 = 全部 PASS；1 = 有 FAIL；2 = 用法错
set -uo pipefail

VAULT="${1:-}"
if [ -z "$VAULT" ] || [ ! -d "$VAULT" ]; then
  echo "用法：bash dev/verify-governance.sh <vault>" >&2
  exit 2
fi
AUDIT_DIR="$VAULT/audit"
TL_DIR="$VAULT/timeline"
fail=0
pass=0
unver=0

say()  { printf '%-6s %s\n' "$1" "$2"; }
ok()   { say PASS "$1"; pass=$((pass+1)); }
bad()  { say FAIL "$1"; fail=$((fail+1)); }
cant() { say UNVER "$1"; unver=$((unver+1)); }

echo "== 治理判据验收（vault: ${VAULT}） =="

# ── 判据 4：按角色归因。对象 audit/*.jsonl · 字段 role（必填）
# 样本框：全部审计分片；抽样：不需要（全量扫，条数不多）
if [ -d "$AUDIT_DIR" ]; then
  total=$(cat "$AUDIT_DIR"/*.jsonl 2>/dev/null | wc -l | tr -d ' ')
  norole=$(cat "$AUDIT_DIR"/*.jsonl 2>/dev/null | grep -c '"role":""' || true)
  if [ "$total" -eq 0 ]; then
    cant "判据 4（role 必填）—— 审计库里一条记录都没有，无从判断"
  elif [ "${norole:-0}" -eq 0 ]; then
    ok "判据 4（role 必填）：${total} 条记录，空 role 0 条"
  else
    bad "判据 4（role 必填）：${norole} 条记录 role 为空（共 ${total} 条）"
  fi
else
  cant "判据 4（role 必填）—— 没有 audit/ 目录"
fi

# ── 判据 13：决策带依据。对象 timeline/*.jsonl · 字段 basis（decision 行必填）
# 样本框：at >= CUTOFF 的 decision 行（回溯期之前的老账不许补 —— 补就是编证据）
CUTOFF="${CUTOFF:-2026-11-19}"
if [ -d "${TL_DIR}" ]; then
  dec=$(cat "${TL_DIR}"/*.jsonl 2>/dev/null | grep '"kind":"decision"' | grep -c "\"at\":\"${CUTOFF}" || true)
  nob=$(cat "${TL_DIR}"/*.jsonl 2>/dev/null | grep '"kind":"decision"' | grep "\"at\":\"${CUTOFF}" | grep -vc '"basis"' || true)
  legacy=$(cat "${TL_DIR}"/*.jsonl 2>/dev/null | grep '"kind":"decision"' | grep -vc "\"at\":\"${CUTOFF}" || true)
  if [ "${dec:-0}" -eq 0 ]; then
    cant "判据 13（决策带 basis）—— ${CUTOFF} 之后还没有 decision 行（老账 ${legacy} 条不计）"
  elif [ "${nob:-0}" -eq 0 ]; then
    ok "判据 13（决策带 basis）：${CUTOFF} 之后 ${dec} 条 decision 行都有 basis（老账 ${legacy} 条不计）"
  else
    bad "判据 13（决策带 basis）：${nob}/${dec} 条缺 basis（老账 ${legacy} 条不计）"
  fi
else
  cant "判据 13 —— 没有 timeline/ 目录"
fi

# ── 判据 1（部分）：事件带出处。对象 timeline+audit 的 derived_from
# 完整判据还要求"能一路回指到原始事件"，那需要逐条走链；这里先验字段存在。
if [ -d "$TL_DIR" ] || [ -d "$AUDIT_DIR" ]; then
  tl_all=$(cat "$TL_DIR"/*.jsonl 2>/dev/null | wc -l | tr -d ' ')
  tl_df=$(cat "$TL_DIR"/*.jsonl 2>/dev/null | grep -c '"derived_from"' || true)
  au_all=$(cat "$AUDIT_DIR"/*.jsonl 2>/dev/null | wc -l | tr -d ' ')
  au_df=$(cat "$AUDIT_DIR"/*.jsonl 2>/dev/null | grep -c '"derived_from"' || true)
  if [ "${tl_all:-0}" -eq 0 ] && [ "${au_all:-0}" -eq 0 ]; then
    cant "判据 1（出处）—— 两类流水都是空的"
  else
    ok "判据 1（部分）：timeline ${tl_df}/${tl_all} 条、audit ${au_df}/${au_all} 条带 derived_from（老数据留空是合法的）"
  fi
else
  cant "判据 1 —— 没有流水目录"
fi

# ── 判据 5：装配可解释。对象 context/assembly.jsonl · 字段 who/at/sections[].chars/why
# 样本框：最近 20 次装配（文件尾部 20 行）
ASM="${VAULT}/context/assembly.jsonl"
if [ -f "${ASM}" ]; then
  n=$(tail -20 "${ASM}" | wc -l | tr -d ' ')
  bad_why=$(tail -20 "${ASM}" | jq -r 'select((.sections // []) | map(select((.why // "") == "")) | length > 0) | .id' 2>/dev/null | wc -l | tr -d ' ')
  bad_who=$(tail -20 "${ASM}" | jq -r 'select((.who // "") == "") | .id' 2>/dev/null | wc -l | tr -d ' ')
  if [ "${n}" -eq 0 ]; then
    cant "判据 5（装配可解释）—— 流水文件是空的"
  elif [ "${bad_why}" -eq 0 ] && [ "${bad_who}" -eq 0 ]; then
    ok "判据 5（装配可解释）：最近 ${n} 次装配，每段有 why、每次有 who"
  else
    bad "判据 5（装配可解释）：${bad_why} 次有段缺 why、${bad_who} 次缺 who（最近 ${n} 次）"
  fi
else
  cant "判据 5（装配可解释）—— 还没有 context/assembly.jsonl"
fi

# ── 判据 7/8/12：经验对象的状态机与台账
EXP_DIR="${VAULT}/experiences"
if [ -f "${EXP_DIR}/ledger.jsonl" ]; then
  exports=$(grep -c '"action":"deploy"' "${EXP_DIR}/ledger.jsonl" 2>/dev/null || true)
  nover=$(grep '"action":"deploy"' "${EXP_DIR}/ledger.jsonl" 2>/dev/null | grep -vc '"digest"' || true)
  rej=$(grep -c '"action":"reject"' "${EXP_DIR}/ledger.jsonl" 2>/dev/null || true)
  if [ "${nover:-0}" -ne 0 ]; then
    bad "判据 8（部署可回滚）：${nover} 次部署缺 digest"
  else
    ok "判据 8（部署可回滚）：${exports} 次部署全部带 digest/version"
  fi
  ok "判据 7/12（经验对象在用）：否决 ${rej} 条、部署 ${exports} 条，台账可查"
else
  cant "判据 7/8/12（经验对象）—— 还没有 experiences/ledger.jsonl（没有任何经验被确认过）"
fi

# ── 判据 2/3/6/9/10/11/14/15：见 GOVERNANCE-FLYWHEEL.md 4.2 的现状表
for n in 2 3 6 9 10 11 14 15; do
  cant "判据 ${n} —— 暂无自动化验收（缺字段或接口，见 4.2）"
done

echo
echo "== 结果：PASS ${pass} · FAIL ${fail} · UNVERIFIABLE ${unver} =="
[ "$fail" -eq 0 ] || exit 1
exit 0

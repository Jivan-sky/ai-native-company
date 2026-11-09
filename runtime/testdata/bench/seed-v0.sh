#!/bin/sh
# seed-v0.sh —— 给沙箱 vault 补上 suite-v0 需要的那两样东西：项目一行 + 一条读授权。
#
# 为什么要有这个脚本：suite-v0 测的是「装配有没有把东西送到手上」，前提是那些东西
# **在真相源里**。而 `anc org init` 生成的骨架故意留白（没有项目、没有授权）——
# 留白不是缺陷，但拿一张空表去量装配，量出来的只会是「表是空的」。
#
# 幂等：已经有的不重复写。
set -eu

VAULT=${1:?用法：seed-v0.sh <vault>}

[ -f "$VAULT/projects.md" ] || {
	printf '错误：%s 不像一个 vault（没有 projects.md）\n' "$VAULT" >&2
	exit 1
}

if grep -qF '| trade-q3 |' "$VAULT/projects.md"; then
	echo "  · projects.md 已有 trade-q3（跳过）"
else
	sed -i '/^|---/a | trade-q3 | Q3 结算改造 | trade | manager | 2026-07-01 → 2026-09-30 | https://example.feishu.cn/docx/AbCdEf123 |' \
		"$VAULT/projects.md"
	echo "  · projects.md 加了 trade-q3"
fi

# 成员要「划域」才看得见自己那块业务。
#
# 这一条不是夹具的凑数：`anc org init` 生成的成员 persona **没有 domains 字段**，
# 于是骨架刚生成时，每个成员的工作上下文都是空的（只有一句「还没给你划域」），
# 而 `anc org check` 仍然是绿的 —— 这是 2026-11-09 跑基线时抓到的假绿，记在 DESIGN §7.1.77。
# 量「配置正确之后装配能不能送到」之前，先把域划上。
for m in alice bob; do
	p="$VAULT/members/$m/persona.md"
	[ -f "$p" ] || continue
	if grep -q '^domains:' "$p"; then
		sed -i 's/^domains: .*/domains: [trade]/' "$p"
	else
		sed -i '/^role: /a domains: [trade]' "$p"
	fi
	echo "  · members/$m 划到 trade"
done

mkdir -p "$VAULT/grants"
GRANT="$VAULT/grants/read-knowledge.md"
# 无条件重写：夹具要**确定**（幂等 = 每次都是同一份内容），不然上一轮留下的旧写法会粘在这里，
# 量出来的数就成了「上一条夹具的残余」。
cat >"$GRANT" <<'EOF'
---
grant:
  from: member:bob
  to: member:alice
  action: read
  object: 10-knowledge
  ttl: forever
  reason: 基准夹具：Alice 读知识库
---

suite-v0 的夹具：Alice 读知识库（授权逐主体）。
EOF
echo "  · grants/read-knowledge.md 写入一条读授权"

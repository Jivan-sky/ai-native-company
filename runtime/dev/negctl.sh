#!/bin/sh
# negctl.sh —— 负控：把 vault 故意弄坏一处，分数**必须**掉，而且掉在指名道姓的那一条上。
#
# 为什么要有这一步：一个只会输出 100% 的测量器不是测量器，是橡皮图章。
# 证明它「会说不会」的办法只有一个 —— 我们亲手破坏，再看它报不报得出来。
set -eu

BIN=${ANC_BIN:-/out/bin/anc}
V=${1:-/out/sandbox/acme}
SUITE=${2:-/src/testdata/bench/suite-v0}
BASE=${ANC_OUT:-/out}/negctl

[ -d "$V" ] || {
	printf '错误：%s 不存在 —— 先跑 ./dev.sh bench\n' "$V" >&2
	exit 1
}

rm -rf "$BASE"
mkdir -p "$BASE"

# 跑一轮，只读四个数：pass^k 率、送达率、要问人几处、任务数。
measure() {
	name=$1
	vault=$2
	out=$("$BIN" bench "$vault" --suite "$SUITE" --json 2>/dev/null || true)
	rate=$(printf '%s' "$out" | sed -n 's/.*"pass_k_rate": \([0-9.]*\).*/\1/p' | head -1)
	deliv=$(printf '%s' "$out" | sed -n 's/.*"delivery_rate": \([0-9.]*\).*/\1/p' | head -1)
	ask=$(printf '%s' "$out" | sed -n 's/.*"asks": \([0-9]*\).*/\1/p' | tail -1)
	printf '  %-18s pass^k=%-6s 送达率=%-6s 要问人=%s\n' "$name" "${rate:-?}" "${deliv:-?}" "${ask:-?}"
}

# 拷一份 → 弄坏一处 → 用同一份 suite 量。破坏命令里 $1 是那份 vault 的路径。
mutate() {
	name=$1
	script=$2
	vault=$BASE/$name
	cp -a "$V" "$vault"
	sh -c "$script" sh "$vault"
	measure "$name" "$vault"
}

printf '\n负控（同一份 suite，每次只坏一处）\n'
cp -a "$V" "$BASE/base"
measure "基线" "$BASE/base"

mutate "删授权" 'rm -f "$1/grants/read-knowledge.md"'
mutate "删域行" 'sed -i "/^| trade |/d" "$1/domains.md"'
mutate "换掉工具" 'sed -i "s/^allowed_tools: .*/allowed_tools: [Bash]/" "$1/roles/manager/persona.md"'
mutate "撤成员域" 'sed -i "s/^domains: .*/domains: []/" "$1/members/alice/persona.md"'

printf '\n判据：每一处破坏都必须在某一列上看得出来（pass^k 掉 / 送达率掉 / 要问人涨）。\n'
printf '      全都看不出来 = 仪器坏了，不是 vault 好。\n'

#!/bin/sh
# ANC 端到端烟测 —— 在容器里跑（/src = 源码只读挂载，/out = 宿主可见的产出目录）。
#
# 验的是一条真实链路，不是「几个命令不报错」：
#   装配（init）→ 真相源（org init / check）→ 渲染（render）→ 投影（export / context / search）
#   → 热层（task 队列）→ 看板（真起 HTTP 服务、真发请求）
#
# 每一步都断言具体的字样或文件；不通过就当场红，并把实际输出摊开（不猜、不跳过）。
# 不验的：真 cc-connect 装载、真飞书收发、真 macOS 腿 —— 那三件在容器里验不出来（见 README）。
set -eu

SRC=${ANC_SRC:-/src}
OUT=${ANC_OUT:-/out}
BIN=$OUT/bin/anc
SANDBOX=$OUT/sandbox
ROOT=$SANDBOX/acme
LOG=$OUT/log/smoke

fail() { printf '\n❌ %s\n' "$*" >&2; exit 1; }
step() { printf '\n▶ %s\n' "$*"; }
ok() { printf '  ✅ %s\n' "$*"; }

# has <文件> <子串> <说明>：文件里没有这句就红，并把实际输出打出来（让人看见它到底说了什么）。
has() {
	if ! grep -qF -- "$2" "$1"; then
		printf '  ---- 实际输出（前 40 行）----\n' >&2
		sed -n '1,40p' "$1" >&2
		fail "$3"
	fi
}

case "$ROOT" in
"$SANDBOX"/*) ;;
*) fail "拒绝清理：$ROOT 不在 $SANDBOX 下（这个脚本会 rm -rf 它）" ;;
esac

rm -rf "$LOG" "$ROOT"
mkdir -p "$LOG" "$OUT/bin" "$SANDBOX"

# ---------- 1. 编译 ----------
step "编译 anc"
(cd "$SRC" && go build -trimpath -o "$BIN" .) || fail "go build 失败"
ok "$BIN"

# ---------- 2. 装配 ----------
step "anc version"
"$BIN" version >"$LOG/version.log" 2>&1 || fail "anc version 退出码非 0"
has "$LOG/version.log" "anc " "version 没打出名字"

step "anc init：客户运行层"
"$BIN" init "$ROOT" --client 某车队 --tier 1 --form F1 >"$LOG/init.log" 2>&1 || {
	sed -n '1,40p' "$LOG/init.log" >&2
	fail "anc init 失败"
}
[ -f "$ROOT/30-delivery/00-判层卡.md" ] || fail "init 没生成判层卡"
[ -f "$ROOT/20-ops/00-七个连接件.md" ] || fail "init 没生成七连接件"
ok "树骨架 + 模板已落盘"

step "anc doctor：环境自检（只报告，不拦后面）"
if "$BIN" doctor "$ROOT" >"$LOG/doctor.log" 2>&1 && grep -qF "全部通过" "$LOG/doctor.log"; then
	ok "环境自检通过"
else
	printf '  ⚠️  doctor 没全绿（容器多半是外网受限）—— 不拦，日志 %s\n' "$LOG/doctor.log"
fi

# ---------- 3. 真相源 ----------
step "anc org init：进同一棵树（D5 拍板的口径）"
"$BIN" org init "$ROOT" --client 某车队 --id acme --members alice,bob --admin alice --force \
	>"$LOG/orginit.log" 2>&1 || {
	sed -n '1,40p' "$LOG/orginit.log" >&2
	fail "anc org init 失败"
}
[ -f "$ROOT/company/company.md" ] || fail "org init 没生成 company.md"
ok "company / roles / members 已生成"

step "填红线：allowed_tools 非空（骨架是故意留空的）"
sed -i 's/^allowed_tools: \[\]/allowed_tools: [Read, Grep, Glob]/' "$ROOT"/roles/*/persona.md
grep -qF 'allowed_tools: [Read, Grep, Glob]' "$ROOT/roles/manager/persona.md" ||
	fail "allowed_tools 没填上（模板是不是改过了？）"
ok "roles/manager 与 roles/devbot 已预授权"

step "填红线：成员要划域（不划域 = 拿不到任何一块业务，2026-11-09 起是红档）"
for m in alice bob; do
	sed -i '/^role: /a domains: [trade]' "$ROOT/members/$m/persona.md"
done
grep -qF 'domains: [trade]' "$ROOT/members/alice/persona.md" || fail "成员划域没写进去"
ok "alice / bob 划到 trade（devbot 与管理员豁免）"

step "划一块业务域 + 一个业务 agent + 一篇知识"
sed -i '/^|---/a | trade | 大宗贸易 | 合同签订与执行 | manager | 10-knowledge | 邮件 | 保留英文：pipeline |' \
	"$ROOT/domains.md"
grep -qF '| trade |' "$ROOT/domains.md" || fail "domains.md 没写进那一行"
cat >"$ROOT/agents.md" <<'EOF'
# 业务 agent

| slug | name | description | domain | harness | tools | role |
|---|---|---|---|---|---|---|
| opsbot | 运营助手 | 冒烟用：跑通队列闭环 | trade | claudecode | Read, Grep | manager |
EOF
printf '# 结算口径\n\n结算周期按自然月，逾期三天内补账。\n' >"$ROOT/10-knowledge/settle.md"
ok "domains.md / agents.md / 10-knowledge/settle.md"

step "anc org check：校验通过才算真相源立住"
"$BIN" org check "$ROOT" >"$LOG/check.log" 2>&1 || {
	sed -n '1,60p' "$LOG/check.log" >&2
	fail "anc org check 退出码非 0"
}
has "$LOG/check.log" "OK" "org check 没给出 OK"
has "$LOG/check.log" "devbot, manager" "角色没读全"
ok "org 校验通过"

# ---------- 4. 渲染 ----------
step "anc render（默认 dry-run，--apply 才落盘）"
"$BIN" render "$ROOT" >"$LOG/render-dry.log" 2>&1 || fail "render dry-run 失败"
has "$LOG/render-dry.log" "未落盘" "dry-run 竟然落盘了"
"$BIN" render "$ROOT" --apply >"$LOG/render.log" 2>&1 || {
	sed -n '1,60p' "$LOG/render.log" >&2
	fail "render --apply 失败"
}
[ -f "$SANDBOX/gateway/config.toml" ] || fail "render 没写出 gateway/config.toml"
has "$LOG/render.log" "已落盘" "render 没走到落盘那一步"
ok "gateway/config.toml 已生成"

# ---------- 5. 只读投影 ----------
step "anc org export（看板消费的只读 JSON）"
"$BIN" org export "$ROOT" >"$LOG/export.json" 2>"$LOG/export.err" || {
	sed -n '1,40p' "$LOG/export.err" >&2
	fail "org export 失败"
}
has "$LOG/export.json" "anc.board/v3" "投影 schema 不是 v3"
ok "schema anc.board/v3"

step "anc context pack（一个 agent 的工作上下文）"
"$BIN" context pack "$ROOT" --who alice >"$LOG/ctx.json" 2>"$LOG/ctx.err" || {
	sed -n '1,40p' "$LOG/ctx.err" >&2
	fail "context pack 失败"
}
has "$LOG/ctx.json" '"who":"alice"' "上下文没认出 alice"
ok "alice 的上下文已装配"

step "anc search（按域可见范围检索）"
"$BIN" search "$ROOT" --q 结算 --who alice >"$LOG/search.log" 2>&1 || fail "search 失败"
has "$LOG/search.log" "settle.md" "检索没命中刚写进去的那篇"
ok "BM25 命中 10-knowledge/settle.md"

# ---------- 6. 热层 + 任务队列 ----------
step "anc hot ping（热层是 Redis，独立小进程）"
"$BIN" hot ping >"$LOG/hot.log" 2>&1 || {
	sed -n '1,20p' "$LOG/hot.log" >&2
	fail "热层没接上（redis 服务起来了吗）"
}
has "$LOG/hot.log" "PONG" "热层 PING 没回 PONG"
ok "热层在线"

step "anc task：add → pull → ack → ls（拉取式队列闭环）"
"$BIN" task add "$ROOT" --to opsbot --title "冒烟：队列闭环" --by anc \
	>"$LOG/task-add.log" 2>&1 || {
	sed -n '1,30p' "$LOG/task-add.log" >&2
	fail "task add 失败"
}
TASK_ID=$("$BIN" task pull "$ROOT" --who opsbot --limit 1 --json 2>"$LOG/task-pull.err" |
	sed -n 's/.*"id": "\(t-[0-9]*\)".*/\1/p' | head -1)
[ -n "$TASK_ID" ] || {
	sed -n '1,30p' "$LOG/task-pull.err" >&2
	fail "task pull 没拿到任务"
}
"$BIN" task ack "$ROOT" "$TASK_ID" --who opsbot --status done --fence 1 \
	>"$LOG/task-ack.log" 2>&1 || {
	sed -n '1,30p' "$LOG/task-ack.log" >&2
	fail "task ack 失败"
}
"$BIN" task ls "$ROOT" --who opsbot --stage done >"$LOG/task-ls.log" 2>&1 || fail "task ls 失败"
has "$LOG/task-ls.log" "$TASK_ID" "ack 之后 done 态里没有这条"
ok "任务 $TASK_ID 走完 add→pull→ack"

step "anc audit config（装机凭据体检：权限 / 产物明文 / 真相源秘密文件 / 越权模式）"
"$BIN" audit config "$ROOT" >"$LOG/audit-config.log" 2>&1 || {
	sed -n '1,30p' "$LOG/audit-config.log" >&2
	fail "audit config 退出码非 0"
}
has "$LOG/audit-config.log" "没有需要处理的项" "体检结论不对"
ok "七项全过（不存在的东西按「跳过」，不是「通过」）"

step "anc gate（交付链路的门：只出结论，不设门禁）"
"$BIN" gate "$ROOT" >"$LOG/gate.log" 2>&1 || fail "gate 退出码非 0"
ok "五道门已出结论"

# ---------- 7. 看板：真起服务、真发 HTTP ----------
step "anc board serve（起真服务，四个端点各验一次）"
"$BIN" board serve "$ROOT" --addr 127.0.0.1:8787 >"$LOG/board.log" 2>&1 &
BOARD=$!
trap 'kill $BOARD 2>/dev/null || true' EXIT INT TERM

i=0
while [ "$i" -lt 50 ]; do
	if curl -fsS -o /dev/null "http://127.0.0.1:8787/" 2>/dev/null; then break; fi
	i=$((i + 1))
	sleep 0.2
done
if [ "$i" -ge 50 ]; then
	sed -n '1,40p' "$LOG/board.log" >&2
	fail "看板 10 秒内没起来"
fi

code=$(curl -sS -o "$LOG/index.html" -w '%{http_code}' http://127.0.0.1:8787/)
[ "$code" = "200" ] || fail "首页 HTTP $code"
has "$LOG/index.html" "ANC 看板" "首页不是看板（内嵌前端没带上？）"

js=$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8787/app.js)
[ "$js" = "200" ] || fail "app.js HTTP $js"

curl -sS http://127.0.0.1:8787/api/board >"$LOG/api-board.json" || fail "/api/board 打不开"
has "$LOG/api-board.json" "anc.board/v3" "/api/board 不是看板 schema"

rt=$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8787/api/runtime)
[ "$rt" = "200" ] || fail "/api/runtime HTTP $rt"

kill "$BOARD" 2>/dev/null || true
trap - EXIT INT TERM
ok "首页 / app.js / /api/board / /api/runtime 全是 200"

# ---------- 收尾 ----------
printf '\n════════════════════════════════════════\n'
printf '✅ 端到端烟测通过\n'
printf '   vault    %s\n' "$ROOT"
printf '   gateway  %s\n' "$SANDBOX/gateway/config.toml"
printf '   日志     %s\n' "$LOG"

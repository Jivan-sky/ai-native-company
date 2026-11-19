# 治理长程任务 · 检查点

更新时间：2026-11-19 09:30（+08）
接续方式：读本文件 → 读 `GOVERNANCE-LONG-RUN.md` → 跑 `bash dev/verify-governance.sh <vault>`

## 一、已完成（可验收）

| 判据 | 状态 | 落点 | 怎么验 |
|---|---|---|---|
| 4 账本按角色归因 | ✅ | `audit.Record.Role` 必填，空则 `ErrNoRole`；11 处构造点补齐 | `verify-governance.sh`：60 条记录 0 空值 |
| 5 装配可解释 | ✅（开关开） | `context/assembly.jsonl`，`--assembly-log` / `ANC_ASSEMBLY_LOG=1`，默认关 | 同上：最近 N 次装配每段有 why |
| 7 经验未确认不得生效 | ✅ | `internal/exp` + `anc experience`，deploy 先要 approve | CLI 实测 exit 1 + `ErrNotApproved` |
| 8 部署可回滚 | 🟡 部分 | version + digest + `experiences/ledger.jsonl` | 台账里每条 deploy 带 digest |
| 12 飞轮有出口 | 🟡 部分 | reject / retire 必须写理由并进台账 | 台账可查；revive 命令未做 |
| 1 出处 | 🟡 部分 | `derived_from`（timeline + audit）；审批/结账主链已带 | 字段在；逐条走链待经验对象铺开 |
| 13 决策带依据 | 🟡 部分 | `basis`（proposal:/task:）；缺 `commitment_version` | 11-19 之后写入的 decision 行 |
| 6 撤权即时生效 | ✅（专家核过） | `Exercise` 按 version + revoked_at | 边界：只管过闸三面 |

四类对象（A 授权本体 / X 例外 / N 更正 / T 废止）已落，且**共用同一个判定器**：
- 例外：`kind: exception`，必须有理由、签发人、到期且 ≤7 天；加载与写入两口都校验
- 更正：`timeline.Entry.corrects`，只追加不改写；`ScanGovernance` 后到者赢、同刻按 id 可重放
- 废止：`timeline.Entry.retires`，与更正分开

## 二、没做（按优先级）

1. **判据 2（权限判定单一入口）** —— 现在至少 5 处：`Exercise` / `board.gate.authorize` /
   `Viewer.CanSeeDomain` / retrieval 里又实现一遍 / policy。做法：逐个打 `//go:authz-entry`
   标记 + `dev/authz-entry.sh` 计数与白名单比对，多一处即失败。
2. **判据 3（账本完整性）** —— 现在是无链无签名的 JSONL，有写权就能改历史。
   做法：每行 `prev_hash` + 每日 head 签名 + `anc audit verify`。
3. **判据 9（破坏性契约变更）** —— `gen-types.mjs` 只拦"前端编字段"；
   删字段/改名/标可选三种全绿。做法：版本化快照 `contracts/*.json`，破坏性变更 `exit 1`。
4. **判据 10/11（任务卡与判定树）** —— 卡是手写样例、无版本机制；判定树谓词无阈值。
   做法：卡由 `anc task card <id>` 生成；判定树做成可执行分类器 + 20 道带答案的题。
5. **判据 14/15** —— "同类"无口径（需要客户对象与标题相似度）；`PendingView` 无 `worst_case`。

## 三、下一轮的第一件事

判据 2：先数清"判定点"（返回 allow/deny 且影响读写的函数），再决定哪 3 处保留、
其余如何合并。**先数再动**，别一上来就合并 —— 合并错了比分散更危险。

## 四、踩过的坑（别再踩）

1. **装配流水写成"读一次写一条"** → 被 `TestContextFreezeRefusalWritesNothing` 抓住，
   而且违反"看板不落盘"承诺 → 改成 opt-in。
2. **dangling helper**：引用 `roleOrSystem` 却没定义，逐包补一份（main / board / approvals）。
3. **Errorf 用 %w 但没 import errors**：编译期才发现，别凭记忆写 import。
4. **bash 里 `$VAR` 后面紧跟中文标点** → 变量名被吃进中文，`set -u` 直接报 unbound。
   一律写 `${VAR}`。
5. **`git add -A` 前没写 .gitignore** → 曾把 node_modules 178MB 提交进仓库（历史里还在）。

# 前端契约（从跑着的服务抓的原始素材）

> 按一份「contract-first」前端评审 prompt 执行的产物。
> **关键差异**：那份 prompt 假设 Vue3 + OpenAPI；我们是**原生 TS + esbuild，没有 OpenAPI**。
> 所以"唯一真相源"改成：**从跑着的看板把每个端点的真实返回抓下来**，再从中反推契约。
> 抓取时间 2026-11-15，18 个端点，原始文件在 `/tmp/contract/*.json`。

## Step 1 · 能力清单（从真实 payload 反推，不靠人转述）

| 端点 | schema | 顶层字段 | 对应的人要干的事 |
|---|---|---|---|
| `/api/board` | `anc.board/v3` | company, roles, members, domains, projects, agents, grants, routing | 看"这家公司长什么样" |
| `/api/approvals` | `anc.approvals/v1` | write_path, signals, count, pending | **签**（唯一有写口的读端点） |
| `/api/timeline` | `anc.timeline/v2` | counts, total, entries, cases, series | 追一件事的来龙去脉 |
| `/api/audit` | `anc.audit/v1` | bands, scopes, total, records, bad, limit | 追一次行使 |
| `/api/context` | `anc.context/v1` | who, domain, roster, tasks_wired, note, pack | 看"某个 agent 拿到什么" |
| `/api/grants` | `anc.board.grants/v1` | total, shown, scope, rows, subjects, findings | 看谁被允许对谁做什么 |
| `/api/runtime` | `anc.runtime/v1` | gateway, gateway_why, bots, handlers | 看 bot 能不能回话 |
| `/api/dataflow` | `anc.dataflow/v1` | relay, exec, bots, gaps, missing | 看数据从哪流到哪 |
| `/api/hot` | `anc.hot/v2` | ttl_secs, stale_after_secs, total, stale, overdue, blocked, review | 看在办的事 |
| `/api/agents` | `anc.agents/v1` | stale_after_secs, total, online, entries | 看 agent 在不在 |
| `/api/assets` | `anc.assets/v1` | dirs, charters, note | 看手上有什么料 |
| `/api/distill` | — | （候选 / 排除 / 缺口） | 看哪些能沉淀 |
| `/api/jobs` | `anc.jobs/v1` | jobs | 看定时作业 |
| `/api/apps` | `anc.apps/v1` | apps | 看装了哪些应用 |
| `/api/models` | `anc.models/v1` | entries | 看注册了哪些模型 |
| `/api/datasources` | `anc.datasources/v1` | datasources | 看连了哪些库 |
| `/api/catalog` | —（**返回 error**） | `{"error":"缺 ds 参数"}` | 看某个库的表 |
| `/api/files` | —（**返回 error**） | `{"error":"缺 ds"}` | 看目录快照 |

**写口三条**（契约里唯一会改东西的）：`/api/approvals/decide`、`/api/tasks/settle`、`/api/intake/{preview,apply,stop}`、`/api/authz/{grant,revoke}`、`/api/auth/{login,register,logout}`。

## Step 2 · 非快乐状态清单（从真实响应抓的，不是想象的）

| 状态 | 真实形状 | 前端现在的处理 |
|---|---|---|
| 未接线 | `{"wired": false, "why": "热层没接上 —— …"}` | ✅ 有（`unknownState`） |
| 缺参数 | `{"error": "缺 ds 参数"}` | ✅ 有（但文案是后端原话，偏技术） |
| 未登录 | `{"enabled": true, "error": "没登录"}` | ⚠️ 只做了登录页，**看板本身还没拦** |
| 权限拒绝 | `{"error":"「devbot」（岗位 devbot）没有 audit.read 的权限"}` | ✅ 显示后端原话 |
| 未配置 | `{"error":"飞书 SSO 未配置","hint":"…"}` | ✅ 说明页 |
| 空集合 | `{"total":0,"records":[]}` | ✅ 空状态 |
| 服务器错 | 未统一 —— **见下面的缺陷** | ⚠️ |

**发现一处真实的契约不一致**：错误形状有两种——`{"error": ...}`（catalog / files / auth）
和 `{"wired": false, "why": ...}`（热层类）。前端要分两套解析。这是**后端契约问题**，
按规矩记下来交给后端，不在前端猜。

## Step 3 · 缺陷分类（对我们现有前端的审计）

按那份 prompt 的固定分类，逐条给证据：

| 类别 | 我们的实例 | 证据 |
|---|---|---|
| **A 契约漂移** | 无（前端类型是照 payload 手写的，`core/types.ts` 里逐条对得上 schema） | — |
| **B 结构反模式** | `styles/` 七份 CSS 互相覆盖，靠 `index.css` 末尾的 `!important` 收口 | `index.css` 的「骨架层」 |
| **C 类型不安全** | 多处 `as HTMLSelectElement` / `as unknown as` | `shell.ts`、`approvals.ts` |
| **D 状态与异步** | 曾出现"点一下弹回去""轮询重建整页"——已修，但同类仍有：抽屉打开状态不进 URL | 不变量检查 |
| **E UX 完整性** | 错误态多数只有文字、没有"重试"；未登录时看板不拦 | 本文 Step 2 |
| **F 范围漂移** | 无（前端没有复制后端业务规则；口径已撤下） | — |

## Step 4 · 架构计划（下一步要做的）

1. **类型从 payload 生成**：写一个小脚本 `dev/gen-types.mjs`，读 `/tmp/contract/*.json` 的键与类型，与 `core/types.ts` 逐字段比对，**不一致就报错**。这是把"契约漂移"变成自动失败的唯一办法（等价于那份 prompt 里的 OpenAPI 校验）。
2. **错误形状收口**：前端只认一个 `readError(body)`，把 `error` / `why` / `hint` 三种来源归一。
3. **未登录拦截**：`/api/auth/me` 返回 401 时整站重定向到 `/login`（开了账号才生效）。
4. **错误态补动作**：每个"答不了"配一个「重试」。

## 强制自审（每次交付都要过）

1. 我有没有新增或改动任何后端能力、端点、业务规则？ —— **没有**（本轮只读）
2. 我有没有编造契约里不存在的字段 / 枚举 / 错误码？ —— **没有**；发现的不一致（错误形状两种）如实记录，未擅自统一
3. 我有没有滑进"后端实现细节"而不是"用户完成任务"？ —— 没有；Step 2 全是按用户动作组织的
4. 每个 UI 元素能否回溯到契约条目或一条写下来的恢复规则？ —— 台账行 → `pending[]`；回执 → `decide` 的 `verdict/by/to`；模块格 → `board.domains`

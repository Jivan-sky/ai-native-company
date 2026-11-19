# P0 · 盘：五类治理对象的现状

> 2026-11-19 摸的底。判据是「字段齐不齐、能不能回指」，不是"有没有做过"。
> 这一页是 `GOVERNANCE-FLYWHEEL.md` 第八节 P0 的产出，下一阶段（P1 规）按它定字段规范。

## 一、逐类盘点

| 对象 | 现状 | 有的字段 | 缺什么（判据要的） |
|---|---|---|---|
| **C 上下文** | `/api/context` 有 pack；本轮刚加 `cost`（每段 chars + why） | who, identity, domains, directory, skills, projects, todo/doing, grants, gaps；**新增**：sections[].chars/.why、total | ① 无 `derived_from`（这份 pack 由哪些文件/规则生成）② 无 pack 版本号 ③ why 只在段级，没有"为什么被排除" |
| **D 决策** | timeline `kind=decision` + approvals 回执（verdict/by/to/at） | id, case, at, kind, status, by, **role**, to, title, detail, refs | ① 无 `basis`（依据哪条 grant / 哪个提案 id）② 无 `derived_from`（这决定基于哪些材料） |
| **B 卡点** | **只有状态，没有事件**。`hot.StageBlocked` 是个 stage；`outbox.KindBlocked` 只发通知 | — | 需要独立事件：卡在哪一步、等谁、从什么时候开始、谁解的 |
| **R 突破** | **完全没有这个概念** | — | 需要：对应哪个卡点、用了什么办法、代价、能不能复用 |
| **E 经验** | `distill` 有"候选"（按 case 绿档挑），但那是**从 case 提炼**，不是从 D/B/R 提炼；也没有人工确认与部署流程 | case, head_event, evidence（本轮加的） | ① 生成源应改为 D/B/R ② 无人工确认记录 ③ 无部署版本与回滚 ④ 无 `reused_count` |

## 二、横向：三个字段全缺

| 字段 | 为什么必须有 | 现在 |
|---|---|---|
| `derived_from[]` | 判据 1 要求"每条经验能回指原始事件"——没有它，血缘断在第一跳 | **全仓没有** |
| `basis`（依据 id） | 决策要能回答"凭什么"，这是审计的第一问 | 无（approvals 回执里只有 verdict 文本） |
| 版本与生效时间 | 判据 8 要求部署可回滚 | 只有 skill 草稿有 `head_event`，规则与手册没有 |

## 三、一个已经做对的地方（不要退回去）

`audit.Record` 已有：`actor` + `on_behalf_of` + `ref` + `tool` + `source`。
差一个 **`role`** 字段（IETF AI 治理草案要求按角色归因）——加上它就满足判据 4，
而"按人查"继续做成视图，不必改存储。

## 四、P1 要定的字段规范（草案，供下一轮讨论）

```yaml
# 一切事件共用（timeline / audit / 未来的 B、R、E）
id:            string   # 稳定、不可复用
at:            RFC3339
actor:         string   # 谁
role:          string   # 什么岗位（必填，按角色归因）
kind:          decision | blocker | breakthrough | experience | context_pack
derived_from:  [string] # 上游事件 id / 文件:行号；空数组 = 明确声明"无上游"
basis:         string   # 决策/行使的依据（grant id / 提案 id / 规则 id）
```

**硬规矩**：`derived_from` 为空且 `basis` 为空的事件，不允许被下游引用（引擎里拦，不靠自觉）。

## 五、这一页的结论

飞轮卡在**第一跳**：事件有，血缘没有。所以 P1 不是"改模型"，是**加三个字段并回填**。
B 与 R 是新增对象，成本最高，但它们正是"卡点→突破→经验"这条链的中间两环；
缺了它们，distill 只能从"结果"猜过程。

## 六、下一步（P1 的具体动作）

1. `audit.Record` 加 `Role`；写入点补岗位（来自 org，不靠调用方自报）。
2. timeline 与 audit 各加 `DerivedFrom []string` 与 `Basis string`（`json:"derived_from,omitempty"`）。
3. 回填规则：能从 `ref` / `src` 推出的自动推；推不出的留空，并在 `/api/*` 上标明"血缘缺失"。
4. 门禁：`dev/dev.sh types` 增加一条——新增事件必须带 `derived_from` 或显式 `basis`。

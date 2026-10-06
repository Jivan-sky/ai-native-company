# docs/ —— 实现视图（不是口径）

这四份文档同步自上游 `HA7CH/ai-native-company` @ `410bc3b`（MIT）。它们**细化实现，不改口径**；与 `../SPEC.md` 冲突一律以 SPEC 为准。

| 文件 | 是什么 |
|---|---|
| `M1-DESIGN.md` | M1 技术设计：`anc init` 拉起最小公司（飞书 × Claude Code）—— org schema、persona 渲染管线、launchd 三件套、原子写与三重校验、测试策略、W1 实测清单、4+2 周排期 |
| `PLAN.md` | 工程里程碑 M0–M5。**≠ SPEC §8 的三个阶段**：那是客户侧交付节奏，这是本库自己的工程排期 |
| `LIGHT-MVP.md` | 轻形态（云上唯一一份公司 vault + 一行接入）的 MVP 设计 |
| `RESEARCH.md` | 立项调研：gateway 选型、品类对照、组织框架 gap 矩阵、IM 与 harness 实测 |

**同步纪律**：口径只在 `../SPEC.md` 改；这四份是上游原文的副本，**不在这里手改**。上游有更新时整份重新同步，并在本文件与提交信息里记下新的上游 commit。

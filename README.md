# ai-native-company

![ANC 封面：一份真相源（SPEC.md），三个消费面（人读 / Agent 读 / 机器跑）](./assets/cover.jpg)

> **把组织协调能力从人的职位上解绑、挂到系统上。** 0 中层是结果，提效是副产品。

判断它成不成，硬指标只有一句：

> **新人或 Agent 能不能不靠问人，拿到全部上下文、权限、工具，把活做完，并且有人能验证。**

完整口径见 [`SPEC.md`](./SPEC.md)（唯一真相源）。

---

## 先看什么

| 你想知道 | 打开 |
| --- | --- |
| 这套组织模型凭什么成立 | [`SPEC.md`](./SPEC.md) |
| 运行层怎么落地、哪些已拍板 | [`runtime/DESIGN.md`](./runtime/DESIGN.md) |
| 怎么装、怎么跑、有哪些命令 | [`runtime/README.md`](./runtime/README.md) |
| 看板为什么长这样、还差什么 | [`runtime/docs/PLAN-v3.md`](./runtime/docs/PLAN-v3.md) · [`runtime/docs/UI-BRIEF.md`](./runtime/docs/UI-BRIEF.md) |
| 下一步走哪、哪些等你拍板 | [`runtime/docs/PLAN-v4.md`](./runtime/docs/PLAN-v4.md) · [`runtime/docs/DECISIONS.md`](./runtime/docs/DECISIONS.md) |

## 三个消费面

| 面 | 是什么 | 在哪 |
| --- | --- | --- |
| **真相源** | 一家公司的组织事实：谁在做什么、数据在哪、谁能碰什么。全是 Markdown。 | `SPEC.md` + 客户 vault（`org init` 生成） |
| **接入面** | 一个单二进制 `anc`：初始化、装配、探活、任务队列、审批、审计、作业。 | `runtime/`（Go 1.24，**零第三方依赖**） |
| **观测面** | 只读看板：一屏一问题，每个数字都能点开看它怎么算的、从哪来。 | `anc board serve`（前端已嵌进二进制） |

**真相源只有一份。** 看板与接入面都是它的派生视图 —— 改口径改 SPEC，不在各处各改一遍。

## 它今天能跑什么

| 能力 | 怎么验 |
| --- | --- |
| 单二进制，六个目标交叉编译 | `dev/dev.sh cross`（windows / darwin / linux × amd64 / arm64） |
| 组织真相源的校验与导出 | `anc org check <vault>` · `anc org export <vault>` |
| 接入外部材料（提案、日报） | `anc ingest`（先校验再整份替换）· `anc import reports`（默认 dry-run） |
| 拉取式任务队列 + 审批留痕 | `anc task …` · `anc approvals …`（裁决落 timeline + audit 两本流水） |
| 定时作业（HTTP / 已审批应用） | `anc job run|tick|ls` · 看板「运行 · 接入」里组装 spec |
| 运行态探针与告警 | `anc probe` · `anc notify`（默认 dry-run） |
| 审计与成本归集 | `anc trail` · `anc audit collect|config` |
| 只读看板 | `anc board serve <vault>`（默认只绑 127.0.0.1） |
| 端到端自检 | `dev/dev.sh all`（fmt / vet / test / 交叉编译 / 烟测） |

当前规模：**262 次提交 · 38 个内部包 · 877 个测试函数 · 13.5 MB 单二进制**（darwin/arm64）。

## 快速开始

```bash
cd runtime
bash dev/dev.sh all                # 宿主机不需要装 Go：全部在容器里跑（fmt / vet / test / 交叉编译 / 烟测）
bash dev/dev.sh board              # 起一个演示看板（含 redis 热层），地址打在终端上
```

看板起来后打开终端里打印的地址。想从零建一份真相源：

```bash
anc init <目录> --client <客户名> --tier 1
anc org init <目录> --client <客户名> --id <ascii-id>
anc org check <目录>
```

## 结构

```
ai-native-company/
├─ SPEC.md          真相源：组织模型 · 四层两纵切 · 七连接件 · 三档可逆 · 判据
├─ assets/          封面（cover.svg 为源，cover.jpg 为渲染成品）与作者头像
├─ docs/            给实现读：上游四份实现视图的副本
├─ skill/           给 Agent 读：可单独安装的能力件（资产沉淀的预存件）
└─ runtime/         给机器跑：单二进制 + 只读看板
   ├─ DESIGN.md     运行层落地设计（决策记录在 §0，实测记录在 §7）
   ├─ docs/         计划、规格、决策台账（PLAN-v3 / PLAN-v4 / METRICS / DECISIONS …）
   ├─ dev/          容器里的验证腿：fmt / vet / test / cross / smoke
   └─ testdata/     测试样本一处收着（vault 样本 · 卡片事件体 · golden 快照）
```

**测试代码挨着代码住，测试样本一处收着。** Go 的 `*_test.go` 必须与它测的包同目录（语言约束），
所以**测试代码跟包走**；而**一切测试样本**收到 `runtime/testdata/` 一处，不进正式文件的目录。

**开放问题不在仓库里。** 未定事项全部记在 [GitHub Issues](https://github.com/Jivan-sky/ai-native-company/issues)，
每条带「要定什么 / 为什么没定 / 选项与建议 / 验收 / 卡住谁」。

## 现状（2026-11-14）

**已落并验过**

- 单二进制、零依赖，六个目标交叉编译全过；`dev/dev.sh all` 是每次交付前的门。
- 作业内核：契约 + 真跑 + 定时（launchd / systemd），执行结果同时进 timeline 与 audit。
- 看板 v3：9 页、14 条指标合同；**汇总数字与明细行都能回指原文**（`文件:行号`）。
- 能力制地基：`<vault>/capabilities.md`（角色 × 模块 × 动作 × 范围）+ 路由级判定，
  `--enforce-authz` 打开时真拦（默认关：开发机上没有网关）。
- 接入页的**写盘事务**：预演不写、diff 逐字段、读回验证、备份、原子落盘、停用不删 spec。
- 运行层不变量：任务结账（A1）与审批裁决（A2）的唯一性/幂等已修，并用并发测试证明过。

**还没做完（都在计划里）**

- **真身份**：等飞书自建应用凭据；能力表与判定器已就绪，接上只换身份来源一处。
- **跨域委派链**：A3/A4（撤权、到期、任职）需要先定根授权的权威来源（C1/C2）。
- **看板收敛后的截图**：旧的交付截图已过期并移除，等几轮迭代后统一补。

## 它**不**做什么

- **不做第二套写口**：装 / 审 / 接这些动作最终落回 `anc` 的命令行语义，看板只在白名单上开三条写口。
- **不把客户的库搬过来**：数据在客户库里，ANC 只连不搬；代码升级不触碰那些数据库地址。
- **不假装有权限**：没有真身份时，页面级可见性只是演示，能力判定默认关闭 —— 页面上写着这句话。
- **不造数字**：查不到来源与公式的数字不上屏；口径写不出来就不画图。

## 与 FDE 48h sprint 的关系

**FDE 是前置条件，ANC 是核心。**

- [`fde-48h-sprint`](https://github.com/Jivan-sky/fde-48h-sprint) 负责接需求、判真伪、打穿一个点、换授权和现场；它的**路线 B 指向本仓库**。
- 本仓库负责打穿之后：把那一刀接回树干，长期跑下去。
- 两者是同一根链条的两段，不是两个竞品。

## 与上游的关系

`SPEC.md` v0.3 是**合并稿**：工程实现层（三层架构 / harness 双后端 / persona 七段式 / vault 规范 /
onboarding 五阶段 / 运维）以**上游 [`HA7CH/ai-native-company`](https://github.com/HA7CH/ai-native-company)**
（MIT，含 Climax Racing reference deployment 生产经验）为骨架；组织模型（中层五类工作 → 拆给谁、
两类 Agent、只存岗位）、七连接件、可逆性三档与单一出口、三个阶段与双节奏、判据，来自本仓库 v0.2。

## 边界

- 凭证一律不进本仓库。
- 客户名 / 金额 / 内部人物不进对外材料。
- 知识性沉淀回知识库；本仓库只留**能力与用法**，不写私货。

---

## 作者

<table>
<tr>
<td align="center" width="50%">
  <a href="https://github.com/Jivan-sky"><img src="https://github.com/Jivan-sky.png?size=160" width="96" alt="Jivan-sky"></a><br>
  <b><a href="https://github.com/Jivan-sky">@Jivan-sky</a></b><br>
  <sub>产品判断、口径拍板、现场验证</sub>
</td>
<td align="center" width="50%">
  <img src="./assets/dsh-avatar.jpg" width="96" height="96" style="border-radius:50%" alt="DeepSeek Harness"><br>
  <b>DeepSeek Harness</b><br>
  <sub>架构落地、运行层与看板实现、不变量审计后的修复</sub>
</td>
</tr>
</table>

口径由第一作者拍：哪条不变量必须成立、什么算"做完了"、哪些先不做。
第二作者负责把它落成代码与测试，并在每次改动后跑门禁与回归。

## License

MIT（见 [`LICENSE`](./LICENSE)）。上游 SPEC 的工程实现层同样以 MIT 发布。

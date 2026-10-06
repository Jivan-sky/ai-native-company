# ANC 运行层装配器（runtime）

> **一句话**：把一台空机器（或一个空目录）在几分钟内变成「客户的 ANC 运行层」——树干目录 + 路由表 + 判层卡 + 计划书骨架 + 七个连接件 + 操作日志与可逆性分级。
> 目的：**驻场时不再从零开始**。用已经做好的东西（FDE skill、技能工厂、知识库 ANC 三件套）快速装配，面对不同客户只换模板与分层，不换骨架。

## 技术选型交代

- **语言：Go 单 exe**。理由：装配器要在客户的 Mac mini / Windows / Linux 上跑，同一份源码三平台；交付物不许自带运行时（判据：跨平台 + 一次性起进程 → Go）。
- **代价**：现场改**逻辑**要重编（约 10 秒）。所以**模板全部外置成纯文件**（`templates/`），现场改模板不用重编译。
- 只用标准库，无第三方依赖 → 交叉编译零障碍。

## 用法

```bash
# 1. 到现场先自检（网络能不能连 IM、git 在不在、模板全不全、有没有全局代理）
anc doctor

# 2. 生成客户运行层
anc init ./climax --client 某车队 --tier 1 --form F1 --git

# 3. 看我们已有的可复用资产（别重造）
anc assets
```

`--tier` 就是九宫格判层：`1` 降本增效型（约 30 人，要省钱）/ `2` 进步型（约 200 人，要进步）/ `3` 战略型（国央企，要战略变革）。**答错层＝后面全错。**
`--form` 是这次交什么：`F1` 48h 薄切片 / `F2` 一页纸真伪结论 / `F3` 现场工具 / `F4` 报告。

## 生成出来的客户库长什么样

```
<客户>/
├─ README.md            这是什么、目录说明、怎么用、三条红线
├─ CONTRIBUTING.md      入库规范（frontmatter schema、命名、目录约定）
├─ CLAUDE.md / AGENTS.md  Agent 路由表 + 五条铁律（双 harness）
├─ 00-inbox/            待整理；_originals/ 放原件，只追加不改写
├─ 10-knowledge/        已整理的客户知识（一条易变事实只在一处维护）
├─ 20-ops/              00-七个连接件.md · 01-操作日志与可逆性分级.md
├─ 30-delivery/         00-判层卡.md · 01-计划书.md
├─ 40-roles/            角色 persona
├─ 50-skills/           客户专属 skill
└─ company/             公司自描述（onboarding 产物）
```

## 构建

```powershell
pwsh -File build.ps1              # 六个目标（win/darwin/linux × amd64/arm64）
pwsh -File build.ps1 -Only local  # 只编本机 windows/amd64
```

产出 `dist/anc_<version>_<os>_<arch>.zip`，每个 zip **自包含**（exe + `templates/` + `inventory/` + `README.md`）。实测体积：单平台 exe **2.38–2.58 MB**、zip **1.01–1.16 MB**（`CGO_ENABLED=0` + `-trimpath -ldflags "-s -w"`，纯静态无 libc 依赖）。exe 优先从**自身同级** `templates/` 找模板，所以 zip 解到哪都能直接跑。

## 缺省目标平台（对应客户形态）

| 目标 | 对应 |
|---|---|
| `darwin/arm64` | **Mac mini M 系列 —— 主力交付形态**（客户自购，脚本交付） |
| `darwin/amd64` | 老 Intel Mac |
| `windows/amd64` | 常见客户 PC / 开发机 |
| `windows/arm64` | Windows on ARM |
| `linux/amd64` | 租的云主机 / 信创 x86 |
| `linux/arm64` | ARM 服务器 / 麒麟 ARM |

## 48 小时 → 48 天怎么走

- **48h（薄切片）**：判层 → 只做一条工作流 → 输出接到客户已经用的界面（IM / 工单）→ 一线当场验证。**48h 做出来的东西应该是可以丢的**，它的任务是换取 48 天的授权和现场。
- **48 天（沉淀）**：把长在人脑里的判断写成 Context / Skill / Evals；这时才谈知识库、检索层、观测面板。
- **不做什么**：不重构 ERP。ERP 只读当权威数据源；动作写在旁边这一层（有审计），要改数据走接口回写 + 审批。

## 目录

| 路径 | 说明 |
|---|---|
| `DESIGN.md` | 运行层落地设计 v0.1（草案，未实现；含 4 条待拍板） |
| `main.go` | 装配器（`init` / `doctor` / `assets` / `version`） |
| `templates/` | 客户库模板，**纯文件，现场可直接改** |
| `inventory/assets.md` | 已有资产台账 |
| `build.ps1` | 本机编译 + 交叉编译 + 打包 |
| `dist/` | 产出（见 `build.ps1`） |

## 边界

- 装配器**只写目标目录**，不动系统、不改全局配置（`--git` 用的是局部身份 `anc/anc@local`，不碰你的 git 全局配置）。
- 凭证一律不进仓库。
- 本模块是**工程实现**；ANC 的完整理论口径见 `../SPEC.md`。

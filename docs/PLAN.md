# 实施计划(PLAN)

版本:0.1(2026-07-24)。里程碑按「每步都有真实用户在用」排序:reference deployment(Climax Racing)始终是第一个用户,每个里程碑先在它身上落地,再泛化。

---

## M0 — 立项(本仓库现状)✅

- [x] 六路调研:内部解剖 × 2(仓库 + 生产机实勘)、外部生态 × 4(gateway 选型 / OpenClaw 品类 / 组织框架 gap 分析 / IM 平台与 harness 实测)
- [x] 关键裁决:cc-connect 之上建约定层(不 fork 不自研);官方 CLI 双 harness;一人一 bot;每 bot 一 cwd 枢轴
- [x] README + SPEC + PLAN + RESEARCH

## M1 — 抽取:`anc init` 拉起最小公司(飞书 × Claude Code)

目标:在一台干净 Mac mini 上,`anc init` + 引导 checklist 走完,得到一个能回话的 N-bot 公司。详细技术设计见 [M1-DESIGN.md](./M1-DESIGN.md)。

> **真 gateway 门前的未知数(本仓库自加,非上游原文)—— 2026-10-07 已完成。**
>
> 1. [x] 取 `github.com/chenhg5/cc-connect` **pin v1.3.4**(MIT,Go 单二进制)。不取最新:#1562(同实例多飞书 app 共享 WS 丢第二个 bot 的消息)在 1.4.x 仍 open,1.3.4 是唯一有多 bot 生产实证的版本。二进制落 `~/.anc/bin/`,不挂全局 PATH;`host.json` 记 pin。**实测**:二进制自报 `v1.3.4 / commit 27c1de8f / built 2026-06-16T07:52:29Z`,`--version` exit 0,SHA256 与上游 `checksums.txt` 一致。
> 2. [x] 用 `anc org init` 生成的最小 vault 跑 `anc render --apply`,拿渲染产物**真起一次** gateway。**实测**:三项目(alice/bob `dontAsk`、devbot `bypassPermissions`)全部 `platform ready` + `engine started`,末行 `INFO msg="cc-connect is running" projects=3` —— 配置形态被接受;随后 kill(无残留进程)。
> 3. [x] 三处回填完成:`docs/M1-DESIGN.md` §10.1 的 **W1 实测项 ①-⑤**(全部成立)、`runtime/DESIGN.md` §0-**D4**(新增证据:上游自带 daemon,`serve` 范围缩小;另暴露 `--no-capture-secrets` 这一明文风险)、以及同文 §7.1「gateway 是否接受这份配置」(+ 两条新增的字段白名单测试)。
>
> **① ② 都成立**,未触发 §10 表里的回退列。三处回填全部是实测结论(带命令与实际输出)。
>
> **新开台账**:ISSUES **N7**(`anc init` 裸跑缺模板目录即失败 —— 影响发布形态)、**N8**(cc-connect flag 文档与实现不一致)、**N9**(上游 daemon 默认把明文捕获进服务文件 —— 与「全程无明文」冲突)、**N10**(上游对未知配置键静默忽略 —— 需要字段白名单测试)。
>
> **硬卡点不变**:D4(=`serve` 谁写)/ N1 未拍板前,不往下做阶段 C。D4 的代价本轮已重估((c) 从「写一个网关」降到「写一层编排 + 一个探针」)。

- [ ] 从 reference deployment 抽出 generic 部分,形成 **vault 模板仓库**(目录骨架、CONTRIBUTING、路由 CLAUDE.md/AGENTS.md 双文件、templates/)
- [x] **org 真相源 schema**(members/ + roles/,markdown + frontmatter)与 **config 渲染器**(org → cc-connect config.toml,原子写 + 三重校验 + dry-run)——已落地在 `runtime/`(阶段 A/B;gofmt / vet / `go test -count=1 ./...` 四包实测全绿)
- [ ] **launchd installer**:gateway / vault-sync / watchdog 三件套 plist 生成(完整 PATH、无 SessionCreate、GUI 会话),`anc doctor` 环境诊断
- [ ] 内置角色模板库第一批(通用:经理/运营/技术/对外),persona 七段式渲染
- [ ] `anc deploy personas|skills`、`anc status`
- [ ] 验收:Climax 生产从手工脚本切到 `anc` 命令面,行为不回退;一台测试 mini 从零拉起 demo 公司 ≤ 1 小时人工时间

## M2 — 对话式 onboarding + 自动入库

- [ ] **onboarding skill**(SPEC §6 五阶段):访谈(语音友好)→ 名单 → 初始资料 → 装机 checklist(断点续走)→ 验证 + 自我介绍
- [ ] **ingest 管线产品化**:inbox 目录约定、watcher sidecar、结构化入库(schema 校验 + diff 复核门,不 bypass)、publish、摘要通知
- [ ] `anc audit [--fix]` 第一版(默认值、密钥权限、白名单、bypass 检查)
- [ ] 验收:一个非 Climax 的真实小团队(找一家设计室/贸易行/俱乐部)完整走通 onboarding 并留存使用

## M3 — 第二平台与第二 harness

- [ ] 钉钉(Stream Mode)、企业微信(智能机器人长连接)接入:主要是渲染器支持 + 逐平台建应用 checklist + 卡片降级策略
- [ ] **Codex CLI 第二后端**:Harness 接口落地(runTurn/resume/事件流),AGENTS.md persona 注入,回归两 harness 行为一致性
- [ ] per-member credential 支持(每人绑自己的订阅)
- [ ] 验收:同一 org 定义,切平台/切 harness 只改配置一行

## M4 — 运维与自我迭代闭环产品化

- [ ] watchdog / 防假绿探针 / token & 使用日报 通用化(去 Climax 专有逻辑)
- [ ] **triage 引擎**:聊天问题挖掘 → 聚类去重 → 频率信号 → 自动开 issue → 人只把关(治「反馈闭环后半段全手工」的生产头号痛点)
- [ ] skill merge 后自动部署 + 漂移检测(merged = live)
- [ ] 《整机重建 runbook》+ `anc rebuild`(config 可 git 重建,单机风险对策)
- [ ] 心跳/主动性评估(HEARTBEAT.md 模式,多 bot 错峰与预算闸)

## M5 — skill 生态

- [ ] 公司内 skill registry 约定 + eval 回归框架(周期性跑,防 skill 退化)
- [ ] role 模板库社区化(贡献指南、更多行业角色包)
- [ ] 跨公司 skill 分发的安全设计(签名 + 扫描 + pin,见 SPEC §5)——仅设计,是否实施看需求

---

## 运营节奏

- 开发流程沿用 ha7ch 惯例:PR → Codex review → squash merge;每里程碑收口时同步 reference deployment。
- 竞争时钟(RESEARCH 结论):Claude Cowork 正向企业扩张、OpenClaw 社区随时可能有人把团队化用法产品化——M1/M2 是窗口期,目标 4-6 周内交付。

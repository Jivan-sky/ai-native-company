# ANC 运行层落地设计（v0.1 草案）

> 本文是 `../SPEC.md` 的**工程派生视图**：SPEC 管口径，本文管「口径怎么落到一台客户机器上」。冲突一律以 SPEC 为准；实现过程中若被迫改口径，先改 SPEC，再回来改本文。
>
> **验证状态：设计稿，未在任何客户现场跑过。** 已实测的是 `anc init / doctor / assets / version`，§2 阶段 A 的 `anc render / anc org check`，以及 **阶段 A 的外部验证：真 cc-connect 已接受这份配置，且 Windows 与 Linux 两腿结果一致**（2026-10-07 烟测，v1.3.4，三项目全部 `platform ready` / `engine started`；细节见 §7.1 与 `../docs/M1-DESIGN.md` §10.1。Linux 腿跑在 WSL2 Ubuntu，`anc` 由 HEAD 交叉编译）。**尚未实测**：**macOS 腿（零覆盖）**、服务单元（systemd / launchd / schtasks 一个都没装过）、探针、会话与 agent 真交互（只验到「配置被接受、引擎起来了」，没验到「bot 真回话」）。§7 列出必须实测的项、怎么验、通过判据 —— 在跑通之前，本文任何一条都不许当「已实现」讲。
>
> **语言交代：Go**（复用 `anc` 单 exe，新增 `bootstrap` / `render` / `service` / `serve`）。理由：同一份源码跨 macOS / Windows / Linux，常驻进程不许自带运行时，装配器已经是 Go。代价：现场改**逻辑**要重编（约 10 秒）；对应缓解是 persona、模板、业务规则全部外置成纯文件，改这些不用重编译。

---

## 0. 待拍板（先拍这四条，再往下实现）

| # | 决策 | 选项 | 代价 / 影响 |
|---|---|---|---|
| **D1** | **成员 Bot 的隔离档** | (a) 严格一人一 OS 账号（SPEC §5.9 原义）<br>(b) 分档：成员 Bot 共享账号 + 独立进程 / 独立 HOME / 独立凭据 + harness 白名单；业务 Bot（碰 C 档）才独立账号<br>(c) 独立账号只给「需要碰不可逆动作」的 bot | (a) 隔离最强，但 30~200 人 = 30~200 个 macOS 账号，装机与运维成本随人数线性涨，这是本设计里最大的一处规模冲突<br>(b) 装机量降到个位数，但成员 Bot 的隔离从 OS 级降到「进程 + 文件权限 + harness 规则」级<br>(c) 折中，但"谁算高风险"要每次现场判断 |
| **D2** | **无人值守恢复** | (a) 不开 FileVault + 自动登录<br>(b) 开 FileVault + 重启后人工登录<br>(c) 开 FileVault + 计划内重启 + 掉线外部告警催人 | macOS 上 **FileVault 与自动登录互斥**，只能二选一：(a) 断电重启后 bot 自己回来，但磁盘明文，客户资料有被搬走的面；(b) 数据加密，但每次重启都要人到场，否则 bot 不回话 |
| **D3** | **会话连续性** | (a) 单轮：一次问答一个进程，跨消息状态只靠 vault 落盘<br>(b) 多轮：按会话落 jsonl，保留上下文<br>(c) 混合：IM 线程内多轮，换题或超时即断 | (a) 最简、最可审计、无内存态，但用户要重复交代背景；(b) 体验好，但上下文成本与"读了过期上下文答错"的风险升高 |
| **D4** | **与上游 `anc` CLI 的关系**（= SPEC Q6） | (a) 全部自建到能用<br>(b) 只自建 `bootstrap / init / render`，`serve` 等上游<br>(c) 最小 `serve` 先只服务成员 Bot（只读），业务 Bot 等上游 | 上游 CLI **未发布**，等它就是等一个不确定的时间点；全自建则 `serve` 这一块要自己承担 IM 长连接、会话调度、出口审计。<br>**2026-10-07 新证据（把这个问题变清楚了，但还没答完）**：我们依赖的运行时 `cc-connect` 自己已经带 **daemon 管理** —— `daemon install / uninstall / start / stop / restart / status / logs`，Windows 走 `schtasks`（实测 `daemon status` 返回 `Status: Not installed / Platform: schtasks`）。也就是说 **IM 长连接 + 会话调度 + 常驻这件事，上游运行时已经做完了**，`serve` 真正要自己写的只剩「org → 渲染 → 装载 → 探针」这一层编排 —— 这直接压缩了 (a) 的规模。另有一处安全相关：`daemon install` 默认会把 config.toml 里的 `${ENV}` 占位**捕获成实际值**写进服务文件，须显式用 `--no-capture-secrets` 或 `CC_DAEMON_NO_CAPTURE_SECRETS=1` 关掉 —— 与我们「config / 备份 / diff 全程无明文」的承诺直接冲突，若走 (b)/(c) 必须带上这个开关 |
| **D5** | **两套骨架的关系** | (a) 合成一棵树：客户库（`anc init`：inbox / ops / delivery）与 org 真相源（`anc org init`：company / roles / members）放到同一个仓库根<br>(b) 保持两棵：客户库是交付物，org 真相源是运行层，各自独立<br>(c) 客户库作为 org 真相源的一个数据目录挂进去 | 现在两个命令生成的是两棵独立树，且**都有「角色」这个概念**（`40-roles/` vs `roles/`）—— 撞上 SPEC §3.5「同一事实只在一处维护」，双源必然漂移。合成一棵树要先定谁是根、谁挂谁；不合并就得在两边写清楚各自的权威范围。**未拍板**，暂不合并 |
| **D6** | **bot 的家目录布局** | (a) 按 §2：账号 HOME 下 `bot/`（cwd）与 `state/`（会话、日志）<br>(b) 按当前实现：`<homes>/<成员>/` 作 cwd，`<homes>/<成员>/state/` 放日志 | §2 写的是「一个账号一个 HOME」，而 `render` / `service` 现在用的是「一个 homes 根 + 每个成员一个子目录」。D1 若选「严格一人一 OS 账号」，两者要对齐（homes 根就得落到各账号 HOME 里）；若选共享账号，当前实现就是对的。**等服务单元与 D1 一起定** |

---

## 1. 进程模型（每个 bot 一套）

| 进程 | 生命周期 | 职责 | 不做什么 |
|---|---|---|---|
| `anc serve --bot <id>` | **常驻**（服务管理器拉起） | IM 长连接、消息路由、会话调度、健康探针、**单一出口** | **不跑 agent loop**、不解析业务规则（规则在数据里，见 §3） |
| harness 进程（官方 CLI） | **按需拉起**，一次问答一个进程 | 真正的推理与工具调用 | 不持有跨会话状态；不直接对外发消息（回给 serve） |
| `ingest`（服务 Bot） | 常驻 | 把 IM 附件 / 工单 / 邮件里的原件落 `_originals/`（只追加） | 不改写原件、不外发 |
| `watchdog`（服务 Bot） | 常驻 | 跨 bot 健康交叉 + 告警外发 | 不碰业务数据 |
| `devbot` | 按需 | 唯一的全权限 bot，cwd = 仓库本体 | 改动必须走 git，可回滚 |

**为什么 harness 不常驻**：官方 CLI 是交互式程序，常驻吃内存、占订阅并发额度；按需拉起才能做到「一次问答 = 一个可审计的进程」。这同时是 §4 会话边界的基础。

**拉起方式（一个实现，三套模板）**：`anc service install/status/uninstall` 生成并向当前账号装载服务定义 —— macOS `launchd` LaunchAgent、Linux `systemd --user`、Windows 计划任务（登录时触发）。这是**用户级**服务，不写系统级 daemon。

> **已实现（阶段 B）**：`anc service render / status / install / uninstall`，三平台单元由纯函数生成（`internal/service/`）。
> 单元里**不写账号** —— 用户级服务「谁装就是谁」，所以 D1 没拍板也能先跑；Windows 计划任务的 `UserId` 由 `--account` 给。
> 装卸两类动作默认只打印，`--apply` 才真做；单元同样带 `anc:generated` 指纹，他源文件拒绝覆盖。
> **仍未实现**：`anc serve` 本身（阶段 C/D），所以现在装载只会得到反复退出的服务 —— 命令会自己把这句话打出来。

---

## 2. 账号与目录布局

```
/opt/anc/vault/          root 拥有、bot 只读；git ff-only 副本（客户库真相源在这里）
/opt/anc/harness/        官方 CLI 本体，root 拥有、只读；升级一次全 bot 生效
/opt/anc/bin/anc         装配器 + serve（单 exe）
/Users/anc-<bot>/        bot 账号 HOME（0700）
  .anc/creds/            该账号专属凭据（0600）；不进 git、不进日志
  bot/                   cwd：AGENTS.md / CLAUDE.md（渲染产物，带指纹头）、skills/、inbox/
  state/                 会话记录、探针游标、审计暂存（不进 git）
```

- **共享 CLI 本体 + 独立 HOME = 独立凭据**：各官方 CLI 的凭据落在 `$HOME` 或该账号的登录钥匙串里，按账号天然隔离；共享本体只省磁盘与升级次数，**不承担隔离职责**。
- 隔离靠 **OS 账号 + 独立进程 + 独立 HOME + 独立凭据**（SPEC §5.9）。cwd 只是挂载点，**不是授权边界**。
- `anc bootstrap` 是**唯一需要管理员权限**的动作，且只做上面列出的这几类目录与账号。**必须先 `--dry-run` 打印将要做的每一步**，人核对后再真跑（危险操作，见守则）。

---

## 3. org 真相源 → 渲染 → 发布

- **真相源** = git 仓库里的 markdown + frontmatter（公司 / 角色 / 成员 / 岗位），**只存岗位与职责，不存个人标识**（SPEC §2.3）。
- `anc render` **全量生成、不打补丁**：产出每个 bot 的 `AGENTS.md` / `CLAUDE.md`、目录路由表、persona、skill 挂载清单、权限声明文件。
- **persona 三层叠加**：公共层 × 角色层 × 成员层 → 渲染进上述产物；公共段**只存在于公共层**，避免手抄漂移。
- 生成物带**指纹头**；`anc render --check` 报告漂移。**手改生成物 = 事故**（检测到就报错退出，不静默覆盖）。
- **校验规则住在数据里，不埋在实现里**：出厂规则表在 `internal/org/rules.go`（每条有 id、默认档、理由），
  客户在 `company.md` 的 `policy:` 段逐条覆盖（`fatal` / `warn` / `off`）。分档判据只有一条 ——
  **拦不可逆的**（bot 下线、对外答错、覆盖人的手写文件），可逆的小事只告警；SPEC 的无例外红线（§6-5 / §171）
  标 `[红线]` 不许降级。规则 id 拼错 = 你以为关了其实没关，所以策略段自己也要被校验。
  非红档发现必须回显，门禁不许静默。全表：`anc org check <vault> --rules`。
- **两道差分门**：`--adopt`（目标配置没有 anc 指纹 = 别人的文件，不覆盖）、`--allow-scale`
  （新增 / 删除 project 会静默上下线 bot）。两者独立，接管他源配置时都要显式过。
- **`--check` 的口径要说全**：它比的是「现在这份 == anc 上一轮生成的」，抓得到人的手改与 org 变化，
  **抓不到渲染器自己的 bug**。渲染器的外部验证只有真拉起 + 探针（阶段 B）。
  **2026-10-07 已补上半步**：真 cc-connect v1.3.4 吃下了渲染产物（三项目 `platform ready` + `engine started`），
  即「gateway 接受这份配置」成立；但**上游对未知键是静默忽略的**（实测），所以这份产物「被接受」不等于
  「字段都对」—— 字段正确性只能由我们自己的白名单测试兜（见 §7）。
- **发布**：运维机 `render` → commit → 生产机 `anc pull`（ff-only）→ 重渲染 → `anc serve reload`（只对新会话生效，不打断在跑的会话）。
- 硬指标不变：**重装一次 ≤ 10 分钟**（§6）。

---

## 4. 执行契约（一次会话长什么样）

1. `serve` 校验发送者：这个 bot 只服务它对应的那个人（＋ 被授权的群 / 流程）。
2. 组装输入：消息正文 + persona 指针 + 路由表 + 附件落点（**原件指针，不是原件副本**）。
3. 拉起 harness：cwd = `$HOME/bot`，环境变量注入该账号凭据，流式读输出。
4. 回 IM：按段 / 卡片输出，**只走 serve 的单一出口**。
5. 落审计行：谁问的、读了哪些源、做了什么分档动作、消耗多少 token、耗时多久。

**状态不留在内存里**：换进程不丢事实，跨消息上下文按 §0-D3 拍板结果处理。

---

## 5. 权限落点（三层，位置比写法重要）

| 层 | 放什么 | 为什么放这层 |
|---|---|---|
| **harness 权限规则** | bypass 关闭、工具白名单、目录白名单、出站域名白名单 | 声明式、可审计、改一行生效、不用发版 |
| **`anc serve` 代码兜底** | 发送者鉴权、输入校验、路径白名单、密钥注入、**C 档人确认卡（IM 按钮）** | 硬约束至少一层代码兜底：只有提示词的硬约束等于没有约束 |
| **OS 层** | 账号隔离、文件权限、只读挂载、独立凭据 | 破一个 = 破一个 |

- 角色 bot：只读 vault，只写自己的 `$HOME/bot` 与 `state`；`_originals/` 只追加。
- devbot：全权限，cwd = 仓库本体，改坏靠 git 回滚。
- **单一出口**：所有对外动作只经 serve 的 egress，出口处统一做四件事 —— 幂等键、C 档人确认、审计记录、失败可见。检验：**「客户收到的那条消息是从哪来的」必须只有一个答案。**

---

## 6. 装配、重建与观测

**装配链（现场按序执行）**

```
anc bootstrap --dry-run      # 打印将要建什么，人核对
anc bootstrap                # 建账号 / 目录 / 只读挂载 / 本体（需管理员）
anc init <客户库> ...         # 已实现并实测通过
anc render                   # 生成 persona / 路由表 / 权限声明
anc service install          # 装载用户级服务
anc doctor                   # 环境与网络自检（已实现）
→  给 bot 发第一条私聊，等回复  # 这才是「装好了」
```

**重建演练是交付物**：在新机器上跑同一条链，记录「到第一条回复」的墙钟时长；压不到 10 分钟就按缺陷立项（SPEC §3.5）。

**观测三源交叉（防假绿）**：服务在册（launchd / systemd / 计划任务状态）＋ 进程存在 ＋ **功能级探针**（给 bot 发一条私聊 ping，等它回）。「没消息 = 没事」是反模式。

**计量**：token 按 bot / provider 落账；业务流只量「一件事从发生到处理完要多久」与「卡住时间占比」。

---

## 7. 必须实测的清单（未验证，不许当已实现说）

| 要验的 | 怎么验 | 通过判据 |
|---|---|---|
| 官方 CLI 的非交互入口与流式输出 | 在某个 bot 账号下跑一次 headless 问答 | 能拿到流式事件与结束码；能中途取消并留下记录 |
| 每账号凭据隔离 | 两个账号各自登录同一 CLI | A 账号的凭据不能被 B 账号读取或复用 |
| 无人值守启动（含钥匙串能否解锁） | 按 §0-D2 拍板的档位重启机器，不人工登录 GUI | 服务被拉起，且凭据可用（能回话） |
| `bootstrap` 的动作清单 | `--dry-run` 逐条核对后再真跑 | 实际动作与清单完全一致，没有清单外的系统改动 |
| 会话回收与订阅额度 | 并发 N 个会话，观察额度、内存与回收 | 不触发上游限流；空闲进程被回收 |
| Windows 计划任务装载 | 在目标机上 `anc service install --apply` | `schtasks /XML` 接受我们写的 UTF-8 XML；任务出现在计划任务库里 | 
| systemd 无人值守常驻 | 目标机上开 linger 后退出登录 | 服务继续跑（未开 linger 时退出登录即停） |
| 服务单元被真正拉起 | 目标机上 `anc service status` | 服务在跑；日志文件有内容；`serve` 已实现（阶段 C/D 之后才可能） |

### 7.1 已实测（2026-10-07，Windows + Linux 两腿，cc-connect v1.3.4 / commit 27c1de8f）

**「gateway 是否接受这份配置」—— 已通过。** `anc org init` + `anc render --apply` 产出的三项目 config
（alice / bob = `dontAsk`，devbot = `bypassPermissions`，`append_system_prompt` 为七段 persona 的 TOML
literal block，平台段用 `[[projects.platforms]]` + `[projects.platforms.options]`）交给真 gateway 拉起：

```
INFO msg="platform ready" project=smoke-alice platform=feishu
INFO msg="engine started" project=smoke-alice agent=claudecode platforms=1
…（smoke-bob / smoke-devbot 同形）
INFO msg="api server started" socket=%TEMP%\anc-smoke\data\run\api.sock
INFO msg="cc-connect is running" projects=3
```

派生的三条设计结论：

1. **`data_dir` 是有效键** —— 会话数据落在我们指定的目录，没回流默认 `~/.cc-connect`。
2. **长 persona 不撞命令行长** —— cc-connect 把合入内容写临时文件、用 `--append-system-prompt-file`
   传给 claude（规避 Windows 8192 字节上限），所以 persona 可以继续写长。
3. **上游不校验未知键**（实测：塞 `bogus_key_xyz` 后 `config loaded` 照常、零告警）—— 所以
   「渲染产物被 gateway 接受」**不构成**「字段名都对」的证据。表里下面两行因此升格为必须做：

| 要验的 | 怎么验 | 通过判据 |
|---|---|---|
| 渲染产物的字段名白名单 | 拿 1.3.4 的 `config.example.toml` 当 schema 语料，对我们产出的每个键断言「在样例里出现过」 | 键名全部命中；命中不了的要么补样例、要么删字段 |
| 每次升级后的形态回归 | 升级 cc-connect 时先跑字段白名单，再跑一次烟测拉起 | 两项都过才允许换 pin |

#### 7.1.1 跨平台对照（Linux 腿，WSL2 Ubuntu x86_64）

同一份 `anc` 源码交叉编译出 linux/amd64（`GOOS=linux GOARCH=amd64 go build -trimpath`），
配 cc-connect 的 `cc-connect-v1.3.4-linux-amd64` 发布件（SHA256 `86a8c00d…4c997`，
`sha256sum -c` 通过），在 WSL2 Ubuntu 上重跑同一条链路。**结论与 Windows 一致：**

```
INFO msg="platform ready" project=smoke-alice platform=feishu
INFO msg="engine started" project=smoke-alice agent=claudecode platforms=1
…（smoke-bob / smoke-devbot 同形）
INFO msg="api server started" socket=/home/<user>/anc-linux-smoke/data/run/api.sock
INFO msg="cc-connect is running" projects=3
```

`org init` + `render --apply` 在 Linux 上直接产出 POSIX 路径（`work_dir = /home/<user>/…`、
`data_dir = /home/<user>/…`），**没有 Windows 路径泄漏**；未定义 `${ENV}` 的行为与 Windows 逐字一致
（三条 `WARN` → `config loaded` → `failed to create platform … app_id and app_secret are required`、exit 1）。
`data_dir` 下同样长出 `agent-prompts/`（长 prompt 落文件那条路径在 Linux 也走通）、`crons/`、`timers/`、`run/`。

三处必须记住的平台差异：

1. **socket 形态不同** —— Windows 是文件路径，Linux 是**Unix domain socket**。探针/看门狗不许假定其中一种。
2. **渲染路径是本机原生的** —— 我们只验过「在哪个 OS 渲染就给哪个 OS 的路径」；
   **跨 OS 渲染（Windows 上渲染给 Linux 用）未验，也没设计**。发布流程（§3「运维机 render → 生产机 pull → 重渲染」）
   靠的是生产机本地重渲染，所以这条路是安全的，但别想着把渲染产物跨 OS 搬运。
3. **服务单元这一段仍是零覆盖** —— WSL2 默认 PID 1 是 `init(Ubuntu)`、`systemctl --user` 返回 `offline`，
   要验 systemd 用户单元得先开 systemd（改本机 WSL 全局配置，属需授权动作），
   `launchd` 更需要真 mac。所以 §7 表里「服务单元被真正拉起」那行**依然没动过**。

---

## 8. 与 SPEC 的映射

| 本文 | SPEC |
|---|---|
| §1 进程模型 | §3.2 L2 身份 / 执行 |
| §2 账号与目录 | §3.2 身份、§5.9 隔离靠 OS 身份 |
| §3 渲染与发布 | §3.5 纵切 A |
| §4 执行契约 | §3.2 执行、§3.3 上下文 |
| §5 权限落点 | §5 默认值、§6 可逆性三档与单一出口 |
| §6 观测 | §3.6 纵切 B |

**本文不含任何新口径。** 本文里凡出现「应该怎样」的分歧，回到 SPEC 定，不在这里各改一遍。


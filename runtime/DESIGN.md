# ANC 运行层落地设计（v0.1 草案）

> 本文是 `../SPEC.md` 的**工程派生视图**：SPEC 管口径，本文管「口径怎么落到一台客户机器上」。冲突一律以 SPEC 为准；实现过程中若被迫改口径，先改 SPEC，再回来改本文。
>
> **验证状态：设计稿，未在任何客户现场跑过。** 已实测的是 `anc init / doctor / assets / version`，§2 阶段 A 的 `anc render / anc org check`，以及 **阶段 A 的外部验证：真 cc-connect 已接受这份配置，且 Windows 与 Linux 两腿结果一致**（2026-10-07 烟测，v1.3.4，三项目全部 `platform ready` / `engine started`；细节见 §7.1 与 `../docs/M1-DESIGN.md` §10.1。Linux 腿跑在 WSL2 Ubuntu，`anc` 由 HEAD 交叉编译）。**会话与 agent 真交互已打通**（2026-10-07：飞书真人发消息 → agent 真回话，`turn complete tools=3`；同日两次「引擎全绿但回不了话」的故障已定位并修复 —— `work_dir` 从未创建、空白名单在 `dontAsk` 下等于全拒，见 `../docs/M1-DESIGN.md` 与议题 #37）。**`anc probe` 只读两源已落地**（socket 真拨 + 会话事实），并已抓过残留 socket 与「agent 从没起来过」两类假绿；**尚未实测**：**macOS 腿（零覆盖）**、服务单元（systemd / launchd / schtasks 一个都没装过）、探针的**功能级 ping**（真发一条消息那条，烧 token，未做）、看板运行态页的接入。§7 列出必须实测的项、怎么验、通过判据 —— 在跑通之前，本文任何一条都不许当「已实现」讲。
>
> **语言交代：Go**（复用 `anc` 单 exe，新增 `bootstrap` / `render` / `service` / `serve`）。理由：同一份源码跨 macOS / Windows / Linux，常驻进程不许自带运行时，装配器已经是 Go。代价：现场改**逻辑**要重编（约 10 秒）；对应缓解是 persona、模板、业务规则全部外置成纯文件，改这些不用重编译。

---

## 0. 待拍板（先拍这四条，再往下实现）

| # | 决策 | 选项 | 代价 / 影响 |
|---|---|---|---|
| **D1** | **成员 Bot 的隔离档** | (a) 严格一人一 OS 账号（SPEC §6-9 原义）<br>(b) 分档：成员 Bot 共享账号 + 独立进程 / 独立 HOME / 独立凭据 + harness 白名单；业务 Bot（碰 C 档）才独立账号<br>(c) 独立账号只给「需要碰不可逆动作」的 bot | (a) 隔离最强，但 30~200 人 = 30~200 个 macOS 账号，装机与运维成本随人数线性涨，这是本设计里最大的一处规模冲突<br>(b) 装机量降到个位数，但成员 Bot 的隔离从 OS 级降到「进程 + 文件权限 + harness 规则」级<br>(c) 折中，但"谁算高风险"要每次现场判断 |
| **D2** | **无人值守恢复** | (a) 不开 FileVault + 自动登录<br>(b) 开 FileVault + 重启后人工登录<br>(c) 开 FileVault + 计划内重启 + 掉线外部告警催人 | macOS 上 **FileVault 与自动登录互斥**，只能二选一：(a) 断电重启后 bot 自己回来，但磁盘明文，客户资料有被搬走的面；(b) 数据加密，但每次重启都要人到场，否则 bot 不回话 |
| **D3** | **会话连续性** | (a) 单轮：一次问答一个进程，跨消息状态只靠 vault 落盘<br>(b) 多轮：按会话落 jsonl，保留上下文<br>(c) 混合：IM 线程内多轮，换题或超时即断 | (a) 最简、最可审计、无内存态，但用户要重复交代背景；(b) 体验好，但上下文成本与"读了过期上下文答错"的风险升高 |
| **D4** | **与上游 `anc` CLI 的关系**（= SPEC Q6） | (a) 全部自建到能用<br>(b) 只自建 `bootstrap / init / render`，`serve` 等上游<br>(c) 最小 `serve` 先只服务成员 Bot（只读），业务 Bot 等上游 | 上游 CLI **未发布**，等它就是等一个不确定的时间点；全自建则 `serve` 这一块要自己承担 IM 长连接、会话调度、出口审计。<br>**2026-10-07 新证据（已据此拍板，结论见下方「拍板记录」）**：我们依赖的运行时 `cc-connect` 自己已经带 **daemon 管理** —— `daemon install / uninstall / start / stop / restart / status / logs`，Windows 走 `schtasks`（实测 `daemon status` 返回 `Status: Not installed / Platform: schtasks`）。也就是说 **IM 长连接 + 会话调度 + 常驻这件事，上游运行时已经做完了**，`serve` 真正要自己写的只剩「org → 渲染 → 装载 → 探针」这一层编排 —— 这直接压缩了 (a) 的规模。另有一处安全相关：`daemon install` 默认会把 config.toml 里的 `${ENV}` 占位**捕获成实际值**写进服务文件，须显式用 `--no-capture-secrets` 或 `CC_DAEMON_NO_CAPTURE_SECRETS=1` 关掉 —— 与我们「config / 备份 / diff 全程无明文」的承诺直接冲突，若走 (b)/(c) 必须带上这个开关 |
| **D5** | **两套骨架的关系** | (a) 合成一棵树：客户库（`anc init`：inbox / ops / delivery）与 org 真相源（`anc org init`：company / roles / members）放到同一个仓库根<br>(b) 保持两棵：客户库是交付物，org 真相源是运行层，各自独立<br>(c) 客户库作为 org 真相源的一个数据目录挂进去 | 现在两个命令生成的是两棵独立树，且**都有「角色」这个概念**（`40-roles/` vs `roles/`）—— 撞上 SPEC §3.5「同一事实只在一处维护」，双源必然漂移。合成一棵树要先定谁是根、谁挂谁；不合并就得在两边写清楚各自的权威范围。**未拍板**，暂不合并 |
| **D6** | **bot 的家目录布局** | (a) 按 §2：账号 HOME 下 `bot/`（cwd）与 `state/`（会话、日志）<br>(b) 按当前实现：`<homes>/<成员>/` 作 cwd，`<homes>/<成员>/state/` 放日志 | §2 写的是「一个账号一个 HOME」，而 `render` / `service` 现在用的是「一个 homes 根 + 每个成员一个子目录」。D1 若选「严格一人一 OS 账号」，两者要对齐（homes 根就得落到各账号 HOME 里）；若选共享账号，当前实现就是对的。**等服务单元与 D1 一起定** |

**拍板记录（2026-10-07）**

- **D4**：采纳 **(c′)** —— `serve` 降级为 `apply`。上游 `cc-connect daemon` 已承担长连接 / 会话 / 常驻
  （已实测 `daemon install / start / status` 全在），所以 v1 的 `anc serve` 其实是 `anc apply`：
  **render → 校验 → 装载上游 daemon（必须带 `--no-capture-secrets`）→ 装只读探针**，**不自建常驻进程**。
  阶段 C 的启动条件（先拍本条）由此满足。
- **D5**：采纳 **(a)** —— 合成一棵树，**以客户库为根**；`anc org init` 降级为「只生成 org 子树」的子集命令。
  加一条更根本的：**一个仓库根 = 一个公司**（「一机一公司」是它的推论，不是权宜）。
- **D6**：**不等 D1** —— 用一层间接解耦：布局固定为 `<homes_root>/<成员>/`，
  D1 只决定 `<homes_root>` 是共享账号下的 `~/anc/homes`，还是各账号的 `$HOME`。**模板只有一份**。
- **D1 / D2 / D3**：**仍未拍板**。
- **域表（罗盘）落点**（2026-10-07 拍板）：一条业务域 = `domains.md` 里一行；
  `slug`（ASCII）/ `name`（中文）分列，`who` **填岗位**、人名渲染时从 `members/` 现算，
  `data` 是**指针**（顶层目录名）不是说明，列序按表头认、多出来的列忽略。
  **「怎么做」不进表**（活的，由 agent 自己沉淀）；**任务进度也待定**（见 SPEC §13 Q15，
  倾向「表里只留指针、进度落沉淀层」）。粒度判据（防繁琐）：① 会改变 agent 行为吗
  ② 能自动维护吗（人手维护必腐烂）③ 是不是已在别处（在则引用不复制）。
  拒掉的列：成员名单（与 org 双源）、KPI、状态 / 成熟度、审批流程、子域嵌套、常见任务清单。

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
- 隔离靠 **OS 账号 + 独立进程 + 独立 HOME + 独立凭据**（SPEC §6-9）。cwd 只是挂载点，**不是授权边界**。
- **布局与隔离档解耦（2026-10-07 拍板，D6）**：布局固定为 `<homes_root>/<成员>/`；
  D1 只决定 `<homes_root>` 是共享账号下的 `~/anc/homes`，还是各账号的 `$HOME`。**模板只有一份**，不再等 D1。
- `anc bootstrap` 是**唯一需要管理员权限**的动作，且只做上面列出的这几类目录与账号。**必须先 `--dry-run` 打印将要做的每一步**，人核对后再真跑（危险操作，见守则）。

---

## 3. org 真相源 → 渲染 → 发布

- **真相源** = git 仓库里的 markdown + frontmatter（公司 / 角色 / 成员 / 岗位），**只存岗位与职责，不存个人标识**（SPEC §2.3）。
- `anc render` **全量生成、不打补丁**：产出每个 bot 的 `AGENTS.md` / `CLAUDE.md`、目录路由表、persona、skill 挂载清单、权限声明文件。
- **persona 三层叠加**：公共层 × 角色层 × 成员层 → 渲染进上述产物；公共段**只存在于公共层**，避免手抄漂移。
- **段 8「业务域」由 `domains.md` 驱动**：表在才出这一段。**自己所属的域整行**（含 `data`），
  **别人的域只给目录行**（域 / 名称 / 是什么 / 找谁），不给它的 `data` 与 `terms`。
  `who` 存岗位，人名渲染时现算。**没有这张表的 vault，persona 逐字不变**（golden 未动即为证）。
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
- **安全相关段完全显式（2026-10-07 已实现）**：产物恒定显式输出 `[relay] timeout_secs = 0`（v1 零绑定）。
  上游对这个段自带默认值且默认是「开着」（120），不写就等于静默放行 bot 间通道（SPEC §6 不变量 3）。
  两道拦：`Build` 自校验（产物缺段直接失败，无覆盖开关）、`anc render --check` 把「段缺失」与「值非 0」
  都当安全缺口报出来并以退出码 1 结束 —— 指纹比对只看 persona / inputs，**手改 `timeout_secs` 不会动指纹**，
  只有这条能看见它。

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
| **授权表（真相源）** | grant：谁 / 什么动作 / 什么客体 / 期限 / 署名（SPEC §6 授权模型） | 声明式、落 git 可 diff 可回滚；**运行时每次行使重读真相源**，撤回即时生效 |

- 角色 bot：只读 vault，只写自己的 `$HOME/bot` 与 `state`；`_originals/` 只追加。
- **渲染期过滤（2026-10-07）**：agent **不判权**，只工作在**已被过滤好的世界**里；
  **授权表不进 bot 可读的 vault**，bot 的 cwd 是渲染产物、不是仓库根（SPEC §3 / §6）。
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

#### 7.1.2 服务单元：Linux 腿的离线校验（2026-10-07）—— 抓到一个硬 bug

真装载还没做，但**单元文件能不能被 systemd 接受**这一层可以离线判：`systemd-analyze verify`
（WSL2 Ubuntu 里的 systemd 259）。**不需要 systemd 当 PID 1**，只要以 root 跑一次
—— WSL 默认没有 `/run/systemd/`，非 root 会 `Permission denied`。

**当场抓到**：`anc service render --goos linux` 的产出一处**前缀重复**，单元根本装不上。

```
StandardOutput=append:append:<homes>/alice/state/serve.log
StandardError=append:append:<homes>/alice/state/serve.err.log
```

systemd 的判定（修复前，原文）：

```
anc-alice.service:12: StandardOutput= path is not absolute: append:/home/<user>/.../state/serve.log
anc-alice.service:13: StandardError= path is not absolute: append:/home/<user>/.../state/serve.err.log
```

成因：模板里写的是 `StandardOutput=append:%s`，传参时又拼了一次 `"append:"+path`。
systemd 剥掉一个 `append:` 后，剩下的 `append:/...` 不是绝对路径 → 拒收。
修复：模板去掉 `append:`（改为 `StandardOutput=%s`，前缀只由参数带）。
**修复后 `systemd-analyze verify` 无输出（通过）。**

这个 bug 从阶段 B 首次提交（`1a5d03e`）就在，`internal/service` 的测试**没覆盖这两行**，
而且它从没在任何真 systemd 上跑过 —— 正是 N5「装上了 ≠ 跑起来了」的实例。

**同批做掉的**：darwin 的 plist 与 windows 的 task XML 用 XML 解析器验证**良构**
（Windows 侧，两份都 OK）。**注意这只证明「结构合法」，不证明「launchd / schtasks 接受」**
—— 后者要真机。

**仍未覆盖**：`systemctl --user enable --now` 真装载、linger、`launchctl load`、
`schtasks /Create /XML`（要往本机任务库写东西，属需授权动作）、以及 `anc serve` 真跑（尚未实现）。

#### 7.1.3 端到端真腿：真飞书 app 走通一轮对话（2026-10-07）

前两节验的都是「gateway 起得来」。这一段验的是**消息真的能进去、agent 真的会答、答复真的发得回来**。
用的是一条**专用测试腿**（运维者个人飞书应用，**未绑公司账号**），不碰生产。

现场：`anc org init`（单成员 alice）→ 填真实 `feishu.app_id` / `feishu.open_id` → `anc render --apply`
→ `cc-connect --config …`，密钥只走**进程环境变量**（`ANC_FEISHU_SECRET_ALICE`），不落盘。

```
INFO msg="feishu: bot identified" open_id=ou_05e1…4de1
INFO msg="platform ready" project=live-alice platform=feishu
INFO msg="engine started" project=live-alice agent=claudecode platforms=1
INFO msg="cc-connect is running" projects=1
[Info] [connected to wss://msg-frontier.feishu.cn/ws/v2 …]
```

用户发「你好」之后：

```
level=INFO msg="message received" platform=feishu session=feishu:oc_1837…:ou_8759… content_len=6
level=INFO msg="session spawned" agent_session="" is_resume=false elapsed=61.9052ms
level=INFO msg="turn complete" session=s1 tools=0 response_len=166 turn_duration=6.9600825s
  input_tokens=42923 output_tokens=223 silent=false
```

**四个结论**：

1. **渲染产物能驱动一次真实对话** —— persona（七段）确实进了 claude 的上下文：bot 回的是
   「冒烟在。日程、消息、入库，说吧。」，与我们在 `members/alice/persona.md` 里写的服务对象逐字对应。
   「gateway 接受配置」由此从「进程起得来」升级为「**能干活**」。
2. **`allow_from` 用渲染进 config 的 open_id 生效** —— 用 app owner 的 open_id 投递即达，
   说明渲染器把 `member.feishu.open_id` → `allow_from` 这条链路是通的。
3. **长连接（websocket）形态可行** —— 飞书 `callback_type=websocket`，cc-connect 直连
   `wss://msg-frontier.feishu.cn/ws/v2`，**不需要公网回调地址**。这对「一台 Mac mini 在内网」的部署形态是关键前提。
4. **逐轮 usage 是现成的**（详见 §4.6 / GitHub #12 的评论）：`cc-connect` 的 `turn complete`
   自带 `input_tokens` / `output_tokens` / `turn_duration` / `tools`。**但**它只是 stdout 日志行，
   会话落盘 JSON（`data/sessions/<project>_<hash>.json`）里**没有** usage 字段 —— 长期归集的结构化出口**未定**。

**同时抓到一个隔离缺口**：bot 家目录 `homes/alice` 是**空的**，但 bot 的回复里冒出了运维者个人
`~/.claude` 的内容（caveman skill / `settings.json`）。**persona 不是 bot 上下文的全集。**
详见 GitHub **#28**。

**仍未覆盖**：多 bot 并发（本次只一个 project）；订阅制下 usage 的真伪（`input_tokens` 的来源未核）；
bot 家目录与个人配置的隔离（#28）。

#### 7.1.4 usage 的取数路径：读 harness 原生记录（2026-10-07）—— 三方对拍

§4.6 承诺了 token 计量 / 成本分账，但一直没定「数从哪来」。查清了，结论是**读 harness 原生记录**。

**先排掉三条不行的路**：内部 HTTP API（`data_dir/run/api.sock`）的 `GET /sessions` 只回
`project` / `session_key` / `platform`，**没有 usage**；会话落盘 JSON（`data/sessions/*.json`）只有 `history`；
cc-connect 的 `turn complete` 日志行**只打 `input_tokens` / `output_tokens`**，
而 `core.Event` 上明明有 `CacheCreationInputTokens` / `CacheReadInputTokens`（`engine.go:5101` 没打）。
回复页脚有全量（`<model> · out N · in N cw N cr N · ctx N%`），但它在 interactive card 里，解析脆弱。

**日志这条可以用**：`CC_LOG_FILE` 把 slog 重定向到可轮转文件（实测落地）。但**轮转只留 `.1` 一个备份**，
最旧的丢 —— 归集必须**持续消费**，不能事后翻。且格式是 TextHandler，**没有 JSON 选项**。

**真正可用的是原生记录。** cc-connect 的 `agent_session` 就是 claude 的 session id，用它直接定位
`~/.claude/projects/<slug>/<session>.jsonl`，逐轮抽 `message.usage`：

| 轮 | 原生 `in` / `out` | 原生 `cache_read` | cc-connect 日志 |
|---|---|---|---|
| 1 | 42923 / 223 | 0 | `input_tokens=42923 output_tokens=223` **逐字一致** |
| 2 | 203 / 2 | 43008 | `input_tokens=203 output_tokens=2`（cr 缺） |
| 3 | 23194 / 88 | 27008 | 无 `turn complete` 行 |

全天汇总（Agent=Claude）：原生逐轮相加 = `ccusage daily` = `66320 / 313 / 70016`，
而 cc-connect 日志累加只有 `66320 / 313 / **0**`（字段不存在）。

**三个结论**：

1. **cc-connect 的 in/out 是转述不是估算** —— 与原生记录逐字一致。
2. **原生记录字段最全**（in/out/cr/cw），连 `costUSD` 都有（见下）。
3. **不必为 cw/cr 去 fork cc-connect，也不必抓页脚。**

**代价（必须记住）**：这条路**绑死 claude 的存储格式**
（`<HOME>/.claude/projects/<slug>/<session>.jsonl`、行式 JSON、`message.usage`），**不是稳定契约**
—— 同一次就撞见 `cost-state` / `atis-latch` / `last-prompt` 这些未公开行类型。换 harness 就要换一套解析。

**意外收获**：原生记录里有 `type: "cost-state"` 行，claude **自己算成本并按模型分账**：
`totalCostUSD` + `modelUsage{ "<model>[<window>]": { inputTokens, outputTokens, cacheReadInputTokens,
cacheCreationInputTokens, thinkingTokens, costUSD } }`，且**未知模型会标 `hasUnknownModelCost: true`**。
「未知模型显式报无价格、不猜」这个口径**上游已经实现了**，照抄即可。
**但**：本文件里 `cost-state` 只出现 1 条且只含第 1 轮，是**快照不是账本**，写盘时机**未验**。

**参照实现**：本机装了 `ccusage`，一次认出 Claude / Codex / Hermes / OpenClaw / Qwen，
覆盖 14 种 harness，把 cache create / cache read 单列，并对缺价格的模型打 WARN。
**说明「每种 harness 写一个 adapter」不是天量工作**；用它当参照（或依赖）比自己从零写更划算。

---

## 8. 与 SPEC 的映射

| 本文 | SPEC |
|---|---|
| §1 进程模型 | §4.2 L2 运行层 |
| §2 账号与目录 | §3 核心概念（每 bot 一个 cwd）、§6-9 隔离靠 OS 身份 |
| §3 渲染与发布 | §4.5 纵切 A |
| §4 执行契约 | §4.1 L1 / §4.2 L2 / §4.3 L3 |
| §5 权限落点 | §6 十二条默认值 + 授权模型、§7 可逆性三档与单一出口 |
| §6 观测 | §4.6 纵切 B |

**本文不含任何新口径。** 本文里凡出现「应该怎样」的分歧，回到 SPEC 定，不在这里各改一遍。


# ANC 运行层落地设计（v0.1 草案）

> 本文是 `../SPEC.md` 的**工程派生视图**：SPEC 管口径，本文管「口径怎么落到一台客户机器上」。冲突一律以 SPEC 为准；实现过程中若被迫改口径，先改 SPEC，再回来改本文。
>
> **验证状态：设计稿，未在任何客户现场跑过。** 已实测的是 `anc init / doctor / assets / version`，§2 阶段 A 的 `anc render / anc org check`，以及 **阶段 A 的外部验证：真 cc-connect 已接受这份配置，且 Windows 与 Linux 两腿结果一致**（2026-10-07 烟测，v1.3.4，三项目全部 `platform ready` / `engine started`；细节见 §7.1 与 `../docs/M1-DESIGN.md` §10.1。Linux 腿跑在 WSL2 Ubuntu，`anc` 由 HEAD 交叉编译）。**会话与 agent 真交互已打通**（2026-10-07：飞书真人发消息 → agent 真回话，`turn complete tools=3`；同日两次「引擎全绿但回不了话」的故障已定位并修复 —— `work_dir` 从未创建、空白名单在 `dontAsk` 下等于全拒，见 `../docs/M1-DESIGN.md` 与议题 #37）。**`anc probe` 只读两源已落地**（socket 真拨 + 会话事实），并已抓过残留 socket 与「agent 从没起来过」两类假绿；**看板运行态页已接入**（2026-10-07：看板与 CLI 共用 internal/probe 同一份判据，走独立端点 /api/runtime，anc board serve --data 起；活体经 HTTP 与真实浏览器引擎各验一次，响应里逐条摘掉本机路径）。**尚未实测**：**macOS 腿（零覆盖）**、**我们自己那套**服务单元（systemd / launchd / schtasks 一个都没装过 —— D4 之后常驻交给上游 daemon，装载走 `anc apply`；上游那份 Windows 计划任务已实测装载成功）、探针的**功能级 ping**（那要真发一条消息、烧 token —— 已拍板**不做**，裁判权留给人工，见 ../SPEC.md §13 Q17）。**`anc notify` 已落地**（2026-10-07：判据做成纯函数 + 18 条档位用例，活体 dry-run 与「无 socket」负例均已实测，**真发到飞书并跑完整条闭环**，见 §7.1.5）。**`anc trail` 已落地**（2026-10-07：留痕的事实层 —— 只读聚合 cc-connect 会话与 harness 原生记录；三条口径与 claude 自己的 cost-state **对拍到逐字段相等**；活体 + 负例 + JSON 均已实测，见 §7.1.6）。**`anc trail` 的判断层已落地**（2026-10-07：判据是**数据**不是代码，`--rules` 可整份替换、不用重编译；只出结论不设门禁，每条结论都带来源与证据，见 §7.1.7）。**`anc apply` 已落地（Windows 腿 2026-10-07；**Linux 腿 2026-10-08**）**：一条命令走完七步 —— 校验 org → 渲染（与 `anc render` 同一套门禁：同一份 `commitConfig`）→ 凭据体检 → `cc-connect daemon install --no-capture-secrets --force` → 凭据桥 → `restart --force` → 回读（platform ready + 只读探针）；沙箱真跑通（`platform ready 1/1`、探针绿、任务定义里既无键名也无密钥值、重跑幂等），dry-run 与真装载各一次，见 §7.1.8。**仍未实测**：**macOS 腿的凭据注入**（零实现）；Windows 腿的**登录自启**（上游任务写的是 `-AtLogOn`，要注销 / 重启才验得到 —— 归 #42）。Linux 腿的凭据注入与「重启 / 断电后自己起来」都已实测（§7.1.10 / §7.1.11）。**`anc doctor` 多了一组「断电兜底」只读自检**（Linux 三条前提 / Windows 触发器 + 电源条件；真机实测，见 §7.1.11）。**看板三分区接齐了**（2026-10-08：运行态 = 防假绿探针、数据流 = 策略面 + 执行面事实、原料 = 数据目录清单；**事件面与沉淀层仍未实现**，页内如实列缺 —— 见 `README.md` 看板一节）。**授权表（`grants/`）已有落点与结构校验**（2026-10-08：一个 grant 一个文件、十条规则整组 `warn`、看板投影全量；**执行层与按人过滤视图未做** —— 见 §7.1.15）。**凭据键名不再回读文本，改为渲染时记账**（2026-10-08：成员名非 ASCII 或带 `-` 时，曾经发出一个要不到凭据、静默起不来的 bot；现在体检点名那个键并拦下，真二进制 dry-run 三种形态各复现一次，见 §7.1.16）。§7 列出必须实测的项、怎么验、通过判据 —— 在跑通之前，本文任何一条都不许当「已实现」讲。
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
- **D3（部分，2026-10-07 拍板）**：**空闲重置关掉** —— `reset_on_idle_mins` 出厂值 = `0`，
  换新会话由人**显式发 `/new`**，不靠计时器替人猜。它现在来自数据
  （`company.md` 的 `defaults.reset_on_idle_mins`），**不再是代码里的字面量**
  —— 这一行原本写死成 30、没有任何拍板依据，而且客户改不了（见议题 #41）。
  D3 的其余部分（单轮 / 多轮 / 混合）**仍未拍板**。
- **`company.devbot.count` 出厂档（2026-10-07 拍板）**：**保持 fatal、不改代码**。
  理由：SPEC §1 那条是红线，而「首跑只服务一个 bot」这个现场问题**已经有现成的数据出口** ——
  `company.md` 的 `policy:` 段一行（`company.devbot.count = warn`）就能降档。沙箱实测：`anc org check`
  打印「规则覆盖 company.devbot.count」+「⚠️ 非红档发现 1 条」，退出码 0。
  不给它开实现里的特例：门禁要留一条能一键降级的数据出口，而不是逐个在代码里放行。
- **阶段 C 的装载（2026-10-07 落地）**：`anc serve` **不写** —— 装载 = 上游 daemon（见 D4）。
  一条 `anc apply`：渲染 → 校验 → `cc-connect daemon install --no-capture-secrets --force`
  → 凭据桥（把 secrets.env 读进 daemon 进程环境）→ `restart --force` → 回读。实测见 §7.1.8。
- **D1 / D2 与 D3 的其余部分**：**仍未拍板**。
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
| `cc-connect daemon`（上游，**常驻**） | **常驻**（Windows 计划任务 / systemd --user / launchd） | IM 长连接、消息路由、会话调度、**单一出口** | **不跑 agent loop**、不解析业务规则（规则在数据里，见 §3） |
| `anc apply`（我们，**一次**） | 按需执行，装完即退 | 渲染 + 校验 + 装载 `cc-connect daemon` + 凭据桥 + 重启 + 回读 | 不做常驻、不跑 agent loop、不自己实现长连接（D4） |
| harness 进程（官方 CLI） | **按需拉起**，一次问答一个进程 | 真正的推理与工具调用 | 不持有跨会话状态；不直接对外发消息（回给 serve） |
| `ingest`（服务 Bot） | 常驻 | 把 IM 附件 / 工单 / 邮件里的原件落 `_originals/`（只追加） | 不改写原件、不外发 |
| `watchdog`（服务 Bot） | 常驻 | 跨 bot 健康交叉 + 告警外发 | 不碰业务数据 |
| `devbot` | 按需 | 唯一的全权限 bot，cwd = 仓库本体 | 改动必须走 git，可回滚 |

**为什么 harness 不常驻**：官方 CLI 是交互式程序，常驻吃内存、占订阅并发额度；按需拉起才能做到「一次问答 = 一个可审计的进程」。这同时是 §4 会话边界的基础。

**拉起方式**：装载 = `anc apply` —— 它调上游 `cc-connect daemon install --no-capture-secrets --force`，单元形态（Windows 计划任务 / Linux `systemd` / macOS `launchd`）由上游按平台决定；凭据由**凭据桥**在拉起 gateway 之前读进进程环境（§7.1.8）。这是**用户级**服务，不写系统级 daemon。

> **装载已实现（阶段 C，2026-10-07）**：`anc apply` —— 常驻交给上游 daemon（D4），我们**不写** `serve`。
> 上面表里 `cc-connect daemon` 那一行就是它；实测证据见 §7.1.8。
> **阶段 B 的 `anc service` 已退役（2026-10-07）**：它生成的单元跑的是 `anc serve --bot`，而 serve 永不存在 ——
> 装上去只会得到一个反复退出的服务。退役记录与教训见 §7.1.9。

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
| **授权表（真相源）** | grant：谁 / 什么动作 / 什么客体 / 期限 / 署名（SPEC §6 授权模型） | 声明式、落 git 可 diff 可回滚；**运行时每次行使重读真相源**，撤回即时生效。落点与结构校验见 **§7.1.15**（执行层未落） |

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
anc org init <客户库> ...     # 已实现并实测通过
anc render --apply           # org → gateway config（落盘；persona / 路由表 / 权限声明都在这一份里）
anc apply                    # 渲染 + 校验 + 装载上游 daemon + 凭据桥 + 重启 + 回读（一条命令，幂等）
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
| Windows 计划任务装载 | 在目标机上 `anc apply --apply`（= 上游 `daemon install --no-capture-secrets --force`） | 任务出现在计划任务库里；`platform ready` 到齐；凭据不进任务定义 —— **已实测，见 §7.1.8** | 
| Linux / macOS 腿的装载 | 在目标机上跑 `anc apply` | 凭据注入按平台落地（systemd `EnvironmentFile=` / launchd 包装脚本）—— **Linux 已实测，见 §7.1.10；macOS 未实现，命令直接报错** |
| 登录自启（`-AtLogOn` / linger / launchd） | 注销或重启一次，不人工登录 GUI | 服务被拉起且凭据可用 —— **Linux 已实测，含硬断电，见 §7.1.11；Windows 只有登录触发器，未验**；`anc doctor` 现在会把这三条前提（Windows 是触发器 + 电源条件）自检一遍，只读 |
| `anc trail` 在别的 harness 上 | 用 codex / hermes 各跑一轮，再 `anc trail` | 账能跟那个 harness 自己的成本记录对上（现在是 claude 专属解析） |

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
3. **我们自己那套服务单元从没在真 systemd / launchd 上跑过** —— WSL2 默认 PID 1 是 `init(Ubuntu)`、
   `systemctl --user` 返回 `offline`，要验 systemd 用户单元得先开 systemd（改本机 WSL 全局配置，
   属需授权动作），`launchd` 更需要真 mac。**D4 之后这件事不再由我们承担**（常驻交给上游 daemon）——
   这套代码已于 2026-10-07 退役，教训见 §7.1.9。

#### 7.1.2 服务单元：Linux 腿的离线校验（2026-10-07）—— 抓到一个硬 bug

> **这一节讲的是已退役的 `internal/service`（2026-10-07 退役，见 §7.1.9）。代码删了，教训留着。**

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
**这些都不再由我们承担** —— D4 之后装载走上游 daemon（§7.1.8），代码在 §7.1.9 退役。

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

> **修正（2026-10-07，见 §7.1.6）**：本节那句「原生逐轮相加 = `ccusage daily`」是按**行**相加得出的。
> 在同一现场复算：按行相加 = `172549 / 17184 / 3392000`，按 `message.id` 合并 = `85726 / 8517 / 1659520`
> —— **正好一倍**。所以「按行相加」不能当口径用，正确做法是**按 `message.id` 合并**（一条消息会写多行）。
> 本节那次为什么能对上没查清（可能那份样本里没有重复行）；**以 §7.1.6 的口径为准**。

### 7.1.5 报红送人：`anc notify`（2026-10-07）

§6 的观测只做到「看得见」。用户拍板：**报红要由 bot 主动送到负责人面前**，
不能躺在 CLI / 看板里等人来看。落地成 `anc notify`，与 `probe` 分成两条命令 ——
probe 只读、永远无副作用；notify 有副作用（往人的聊天里推消息）。合在一起会毁掉
probe 的立身之本（只读命令被随手跑），所以判据仍是同一份 `internal/probe`，不写两套。

**判据（纯函数 `notify.Decide`，无时钟、无盘、无网，18 条档位用例覆盖）**：

- **只看红。** 黄 = 运行中 / 这一窗口没观测到，不要人动手；把黄也推给人 → 人静音 → 红的告警一起死。
- **边沿触发 + 冷却**（默认 30m，同 probe 的 `--stale` / `--stall` 一样是参数）：
  没喊过 + 红 → `alert`；红着且过冷却期 → `reminder`（否则就成了「没消息 = 没事」）；
  红转绿 → `recovery` 并清账；冷却期内 → 只落 `Plan.Silent`（**让「没喊」说得出来**）。
- **网关挂了只喊 `gateway` 一条**，且此时不评估 bot —— 底下每个 bot 判红是同一根因，逐个喊就是刷屏。
- 没喊过的账转绿 → **不欠谁一声**（不发 recovery）。

**通道**：走 cc-connect 的 unix socket `POST /send`（`{"project","message"}`，`core/api.go`）。
与探针判活**同一条通道** —— 不直连飞书 API，换平台（钉钉 / 企业微信）是 cc-connect 那侧的事。

**游标**：`<data_dir>/state/notify.json`（不进 git）。**dry-run 不写**（写了会吞掉下一次真发的告警）；
推送失败（一个都没送到）也不写 —— 不把失败当成「喊过了」；坏 JSON 报错，不当空游标静默吞。

**推给谁**：`company.admins` 各自的 bot，**不是出故障那个 bot**（它可能连话都回不了）。
不是成员 / 已停用的收进 `unresolved` 明说，不静默少喊一个人。

**实测**：`gofmt` / `go vet` / `go test ./...` 全绿（notify 包 18 条，含真 unix socket 上验请求体与错误路径）；
活体 dry-run 沙箱 🟢 →「没有要喊的」exit 0；负例（临时现场无 socket）→ `[新告警] gateway`、exit 1、
**且未落游标**；无动作时 `--send` 写空游标成功。

**真发已实测（2026-10-07，用户在场，沙箱）** —— 整条闭环都走过真通道，没有一步是 mock：

| 步 | 做了什么 | 结果 |
|---|---|---|
| 造红 | 沙箱里放一个「有消息、但 `agent_session_id` 为空」的假会话 | `anc probe` 判 🔴（`Judge` 合并同项目所有会话文件，这条签名就是「引擎绿但回不了话」） |
| 喊人 | `notify --send` | `[新告警] demo-alice` 推到 `demo-alice`，gateway 回 200，**负责人聊天里真收到**；退出码 0，游标落盘 |
| 不唠叨 | 红档还在时**再跑一次** | 「没有要喊的（红 1 / 黄 0）」，1 条落 `Silent` —— 冷却真压住了，没重复喊 |
| 报平安 | 删掉假会话，再 `--send` | `[已恢复] demo-alice` 推到人面前，退出码 0，游标清回 `{}` |

沙箱已复原（🟢）。**仍未覆盖**：真网关真故障（不造假会自己变红的那种）下的端到端 —— 
那要等真出事，或者专门搭一个坏掉的 gateway。

### 7.1.6 留痕的事实层：`anc trail`（2026-10-07）

§4 第 5 条要「落审计行：谁问的、读了哪些源、做了什么分档动作、消耗多少 token、耗时多久」，
§5 要「客户收到的那条消息从哪来只有一个答案」。**写入端 harness 自己已经有了**（记录就是账本），
缺的是把账翻成人看得懂、并能按 bot / 按轮对上的那一层。这就是 `anc trail`：**只读聚合，零新增写入**。

两份现成记录，缺一份都不完整：

| 源 | 位置 | 它回答什么 |
|---|---|---|
| cc-connect 会话落盘 | `<data>/sessions/<project>_<hash>.json` | 哪个 bot 的哪个会话槽对应哪个 harness 会话 id —— **两份记录之间唯一的桥** |
| harness 原生记录 | `<claude-home>/projects/<slug(work_dir)>/<id>.jsonl` ＋ `<id>/subagents/*.jsonl` | 逐轮的 token / 工具 / 被拒 / 耗时 / 成本 |

**三条口径是实测出来的，不是看着像**（把聚合结果与 claude 自己的 `cost-state` 对拍，**逐字段相等**）：

1. **不按行累加。** 同一条 assistant 消息写成多行（一行一个 content block，流式中间态
   `output_tokens=0`）。按行相加 = 正确值的**两倍**（本现场 `172549` vs `85726`）。
   正确做法：按 `message.id` 合并 —— 用量取各次出现的最大值，工具名取并集。
2. **子 agent 的账算在这个 bot 头上。** `<id>/subagents/*.jsonl` 是 Agent 工具拉起的旁路会话
   （本现场 1 个 Explore，`in 60279 / out 7969`）。不并进来会少报一大截；`meta.json` 的 `toolUseId`
   能对上主记录里的 `tool_use.id`，所以还能**精确归到拉起它的那一轮**（本现场 = 第 6 轮）。
3. **成本用 harness 自己算的**（`cost-state.totalCostUSD`），它连「未知模型没价格」都标了
   （`hasUnknownModelCost`）。自己不维护价格表。

对拍结果（同一段会话）：

```
trail 聚合（主 + 子 agent，按 message.id 合并）  in 146005  out 16486  cr 2071680  cw 0
claude cost-state（harness 自己的账本）          in 146005  out 16486  cr 2071680  cw 0
```

**坑（都写进代码注释了）**：一行可能很大（整段工具输出塞在行里），`bufio.Scanner` 默认 64KB
会直接报错 —— 那是**读不到**，不是「没有」；解析不了的行要**数出来报**；`work_dir` 住在
`[projects.agent.options]` 里（项目名之后还有段头），按「见 `[` 就重置」扫会把整段丢掉。

**读不到就明说**：原生记录找不到 / 解析不了时，`trail` **报「归集不上」并以退出码 1 退出**，
合计行也标注「其中 N 段读不到，没算进来」—— 一个光秃秃的 `0` 和「真的一分没花」长得一模一样，
那正是我们一直在防的假绿。

**实测**：`gofmt` / `go vet` / `go test ./...` 全绿（trail 包 12 条 + render 2 条）；活体在沙箱跑出
3 段会话、6 轮时间线、子任务归属与逐轮耗时（不含空闲）；负例（临时现场 work_dir 指向不存在的地方）
→ 3 段全报「归集不上」、Exit 1、合计注明「3 段读不到」；`--json` 结构与字段核对过。

**未覆盖**：**只认 claude 的 jsonl 格式** —— 换 harness（codex / hermes / openclaw…）要另写 adapter，
这条路是绑死在存储格式上的（同 §7.1.4 的代价）。**判断层没做**：「决定是什么」「失败根因」不在此列，
`trail` 只出事实，不替 agent 编理由。

### 7.1.7 留痕的判断层：`anc trail` 的判据（2026-10-07）

事实层只回答「发生了什么」。这一层回答「这算什么」，**且判断与事实在输出里分开摆** ——
混在一起，读的人就分不清哪句能当证据用。

**判据是数据，不是代码。** 引擎里没有任何一个 `if` 认识某条具体判据：

- 判据 = 一张表（`id / scope / level / title / say / when`），`when` 是对**指标**的比较
  （`denied >= 2`），`say` 是带占位符的模板（`{denied_tools}`）；
- 出厂 4 条（`internal/judge` 的 `Builtin`），`--rules <文件>` 可以**整份替换**；
- **加一条判据 = 加一行数据**，不用改引擎、不用重编译；换一套完全不同的判法，换的是那份文件。

**这里没有门禁。** judge 只出结论：不拦任何事、不改退出码、不自动动手。
判错了的代价应该只是「多了一行话」，不是「一件本该发生的事没发生」。

**三条自我约束**（都写进代码了）：

1. **来源与证据缺一不可。** 每条结论带 `source`（现在是 `rule`，将来模型路线会写 `model` ——
   这条字段就是「判据」与「猜测」的分界线）与 `evidence`（点回记录原话）；**没有证据不发**。
2. **证据只列相关的那一类。** 一条「被权限挡下」的结论，证据里混进「文件不存在」，
   读的人就没法判断这条结论成不成立。相关性从判据自己写的指标推出来，不在引擎里写死。
3. **写错的判据要被拒。** 指标名、比较符、占位符都校验；不认识的直接报错 ——
   一条**永远不会命中**的判据，比一条写错的更难发现。新增**事实**才需要动事实层与指标表，
   判据可以随便长，事实必须来自记录。

**事实层为此补的一格**：`Failure{kind, tool, why}` —— `denied`（被权限规则挡下）与
`failed`（真执行失败）**分开记**。把「权限没给」说成「故障」，人会去查一个不存在的问题；
把真失败说成「权限不够」，人会去加权限、问题还在。`why` **照抄记录原话**（截掉给模型看的
`IMPORTANT:` 提示尾巴），不转述。工具名靠 `tool_use_id` 回查主记录，查不到就留空、**不猜**。

**实测**：`gofmt` / `go vet` / `go test ./...` 全绿（judge 10 条 + trail 13 条）；
沙箱活体跑出 6 条结论（`session-denied-heavy` ×1、`turn-denied-repeat` ×2、`turn-tool-failed` ×3），
证据各自只列相关那一类；**换一份判据文件**（自定义阈值与措辞）行为随之改变，**没有重编译**；
`--no-judge` 只出事实；`--json` 结构为 `sessions: [{session, findings}]`，字段核对过；
负例（读不到）只命中 `session-unreadable` 并以退出码 1 退出。

**未覆盖**：出厂只有 4 条判据（**覆盖窄是有意的** —— 它是起点不是标准答案，现场该改的是那张表）；
「失败根因」的**模型路线**没做，将来也只允许作为「提案」出现，不能当结论。

---

### 7.1.8 装载：`anc apply`（2026-10-07，Windows 腿，沙箱真跑）

**一条命令七步**：校验 org → 渲染（与 `anc render` 共用同一份落盘实现与门禁）→ 凭据体检 →
`cc-connect daemon install --no-capture-secrets --force` → 凭据桥 → `daemon restart --force` → 回读。

**为什么「凭据桥」要单独一步**：上游 daemon 的装载体**没有 dotenv**（实测 v1.3.4 二进制里搜不到），
`${ENV}` 只从**进程环境**解析；而 `daemon install` 默认会把 `${ENV}` **捕获成明文**写进服务文件。
所以两头都必须在：装载带 `--no-capture-secrets`，拉起 gateway 之前把 `secrets.env` 读进进程环境。
注入的是**上游生成的** `cc-connect-daemon.ps1`，因此托管区带边界标记与指纹，幂等可重入、被冲掉看得出来。

**实测（沙箱 `anc-demo`，真跑）**：

| 验的 | 怎么验 | 结果 |
|---|---|---|
| dry-run 不碰任何东西 | `anc apply <vault>` | 退出 0；`config.toml` 不存在、没跑任何 daemon 命令 |
| 真装载 | `anc apply <vault> --apply` | `platform ready 1/1`、探针 🟢；装载体顶部出现托管区（`fp 720cbfde3d11`），上游原文逐字保留在下面 |
| **凭据不进任务定义** | `Export-ScheduledTask -TaskName cc-connect` 的 XML 里找键名与密钥值 | 键名 → False，32 位密钥值 → False；任务动作只是一句 `powershell.exe -File <家目录>\.cc-connect\cc-connect-daemon.ps1` |
| 幂等 | 重跑 `--apply`，比对装载体 SHA256 | 哈希不变、`platform ready 1/1` |
| 缺键 = 停 | 凭据文件留空跑一次 | 退出 1，**在装载之前**就停（不装载一个必然起不来的 daemon，否则这台机器上原来在跑的 bot 会全部下线） |

**一处如实记**：上游 `install --force` 每次都会把装载体整份重写回它自己那份，所以重跑在**文件层面**
是「重写回原样 → 再放回托管区」。命令不把它说成「没动过」，而是说「重注（内容与上次一致）」。

**踩到并修掉的一处**：`render.EnvRefs` 第一次跑就把产物头部注释里的 `${ENV}`（讲口径用的那句话）
当成了真实的键名，于是体检报「缺键 ENV」。修法：**整行注释不算引用**。这是「先跑 dry-run」的价值。

**没做（明确标出）**：

- **macOS 腿**：launchd 包装脚本未实现（议题 #23）。**未实现即报错**：`anc apply` 在 macOS 上直接拒绝，
  不退化成「前四步做完、留个读不到凭据的 daemon」。Linux 腿见 §7.1.10。
- **登录自启**：上游写的是 `-AtLogOn`，要注销 / 重启才验得到；本轮只验了「现在这一次拉起」。
- **ACL**：`~/.cc-connect` 整棵树与 `~/.anc` 其余部分仍是默认继承（归议题 #4；`secrets.env` 已收紧）。
### 7.1.9 退役记录：`anc service`（2026-10-07）

**为什么退**：D4 把 `serve` 降级成 `apply`、且 serve 永不存在，而 `anc service render/install`
生成的单元跑的就是 `anc serve --bot` —— 装载它只会得到一个反复退出的服务。
留着等于留一条「看起来能用、装了就坏」的路径，**比没有更糟**。

**删了什么**：`internal/service/`（三平台单元生成 + 指纹 + `~/` 展开）、`runtime/service.go`（CLI）、
`runtime/service_cli_test.go`（4 条 CLI 测试）。装载的职责归 `anc apply`（§7.1.8）。
删除前先在机器上查过：没有我们自己装过的计划任务（`ANC\*`）、也没有 `~/anc/tasks/` 残留，
所以**没有任何东西需要卸载**。

**留下的教训**（代码删了，这三条不许忘）：

1. **模板 + 参数拼接是 bug 温床**：模板写 `StandardOutput=append:%s`、传参又拼一次 `"append:"+path`，
   产出 `append:append:/…`，systemd 直接拒收（§7.1.2 抓到的那处）。
2. **「结构合法」不等于「对方接受」**：XML 良构、`systemd-analyze verify` 通过，都只是离线判据。
3. **单测没覆盖的行等于没写**：那个 bug 从阶段 B 首次提交就在，测试恰好绕过了它。

**顺带得到的判据**：常驻这件事上游已经做完了（`cc-connect daemon`），我们自研的那一套
除了多一个失败面没有任何增量 —— **能不自研就不自研**；同理，「一个 bot 一套进程」在
上游「一个 daemon 管全部 project」的现实下本来就不成立。
### 7.1.10 装载：Linux 腿（2026-10-08，Ubuntu 24.04 / systemd 255，真 VM 真跑）

**沙箱**：VMware Workstation 里的 Ubuntu 24.04 cloud image —— `pid1=systemd`、`systemctl is-system-running=running`、
用户级 `systemctl --user` 也是 running、linger 已开。WSL 不行：没有 systemd 就没有 `systemctl --user`，
而 `daemon install` 在 Linux 上只走 systemd，没有用户会话它会直接拒绝。

**机制：我们自己的 drop-in，不碰上游的单元文件。**
Windows 腿接管的是**上游生成**的 `cc-connect-daemon.ps1`（所以要维护边界 + 指纹 + 「被重装冲掉看得出来」）。
Linux 腿反过来：上游按 euid 落单元（root → `/etc/systemd/system/cc-connect.service`，
非 root → `~/.config/systemd/user/cc-connect.service`），`daemon install --force` 每次把它整份重写回自己那份；
而 systemd 的 drop-in（`<单元名>.d/anc-secrets.conf`）是**我们完全拥有**的文件，上游不碰它 ——
天然幂等，也不用去解析别人的文件。装载时只去这两个位置里**找**真落点（不猜），找不到就明说试过哪些。

**两个真 bug（都是实测抓出来的，别再踩）**：

1. **systemd 的 `EnvironmentFile=` 不给去引号。** 写成 `EnvironmentFile="…"` → systemd 报
   `EnvironmentFile= path is not absolute, ignoring: "/home/anc/.anc/secrets.env"`，整条被忽略，
   daemon 起来后报「引用了没定义的环境变量」。修：**不加引号**，且路径不能带空白（测试里加了「不许有引号」的断言）。
2. **上游 `daemon restart` 不认新写的 drop-in，必须我们自己 reload。** 实测：写完 drop-in 后
   `systemctl --user show cc-connect -p EnvironmentFiles` 是空的，跑一次 `systemctl --user daemon-reload`
   之后才出现 `/home/anc/.anc/secrets.env (ignore_errors=no)`。修：`bridgeLinux` 写完文件自己 reload 并回显。

**这两条正好解释「引擎绿但起不来」**：不 reload 或带引号 → daemon 起来了却报
`config: env var placeholder references unset variable`，而表面上 `platform ready` 还在。

**实测（真跑，逐条可复现）**：

| 验的 | 怎么验 | 结果 |
|---|---|---|
| 装得上 | `anc apply <vault> --apply` | 第 4 步 `cc-connect daemon installed and started. Platform: systemd (user)` |
| **systemd 真认这份 drop-in** | `systemctl --user show cc-connect -p EnvironmentFiles` | `EnvironmentFiles=/home/anc/.anc/secrets.env (ignore_errors=no)`（reload 之前为空） |
| **凭据真进了进程环境** | `/proc/<MainPID>/environ` 里数键名 | `ANC_FEISHU_SECRET_{ALICE,BOB,DEVBOT}` 三个都在（只报键名，不打印值） |
| **凭据不进单元文件** | 在 `~/.config/systemd/user/` 下 grep 键名与密钥值 | 空 —— 单元与 drop-in 里没有任何明文密钥 |
| 幂等 | 重跑 `--apply`，比对 drop-in SHA256 | `0970b11a…` 不变，`fp 1104018a058f` 不变 |
| 端到端 | 日志里数 `platform ready` | `platform ready 3/3` + `engine started … agent=claudecode` |
| 探针 | `anc probe <vault>` | 三个 project 都是 🟡「没观测到任何会话」—— 没发消息就不许报绿 |
 探针的退出码口径：**不是全绿就 `exit 1`**（`probe.go:108` 的 `rep.AllGreen()`）—— 于是刚装好、还没人
发过消息的机器上 `anc probe` **必然返回 1**（三个 🟡）。这是「不许报绿」的直接后果，**别把它当
健康门禁串进 `&&`**：`anc apply` 第 7 步就是因此只回显、不采信它的退出码。

**一处已更正（2026-10-08 复测）**：曾记「VM 的 NAT 出不去飞书（`open.feishu.cn` 连接被 reset）」——
那是**当时主机侧透明代理那一分钟的状态**，不是这台沙箱的固有属性。取证：日志里 `connection reset by peer`
只出现在 2026-10-07T17:40Z 前后（对端是 fake-IP `198.18.0.75:443`，Clash 类代理的特征段）；此后同一条
出网路径拿到的是**真飞书响应** —— `curl -X POST https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal`
→ `{"code":10003,"msg":"invalid param"}`（HTTP 200，真服务端 JSON），cc-connect 自己的日志也从传输错误
变成服务端错误（`1000040346: app_id is invalid`、`msg:invalid param,code:10003`）。结论：**Linux 腿「真回话」
只缺一个真 app_id，不缺出网。** `platform ready` 判的仍是 gateway 自己起没起来。

**没做（明确标出）**：

- **macOS 腿**：零覆盖（议题 #23）。
- **登录自启**：本条当时只验了「现在这一次拉起」；「重启 / 断电后自己起来」后来补验了，见 §7.1.11。
- **真回话**：探针停在 🟡，缺的是**一个真实飞书应用**（出网已复测通过，见上）。

### 7.1.11 断电 / 重启之后，谁把它拉回来（2026-10-08，Linux 腿三层实测）

**问题**：`anc apply` 只保证「现在这一次拉起来」。服务器不断电是常态，但断电是**必须兜住**的那一次 ——
机器起来之后，没有人在旁边敲命令。

**三层保险，逐层实测（Ubuntu 24.04.5 / systemd 255 真 VM，全程无人登录）**：

| 层 | 怎么造出来的 | 结果 |
|---|---|---|
| 进程暴死 | `kill -9 <MainPID>` | 上游单元带 `Restart=on-failure` / `RestartSec=10`，systemd 10 秒后拉回（`NRestarts=1`、新 PID） |
| 机器重启 | `systemctl reboot` | 开机 **12 秒**后服务自己起来（01:51:01 开机 → 01:51:13 active），此时没有任何人登录 |
| **硬断电** | `vmrun stop <vmx> hard` 再上电 | 仍是 12 秒自己起来（01:52:18 → 01:52:30） |

**硬断电那一条才是真的**：dmesg 留下了非正常关机的痕迹 ——
`EXT4-fs (sda1): INFO: recovery required on readonly filesystem` → `recovery complete`，
以及 `systemd-journald: File …/system.journal corrupted or uncleanly shut down, renaming and replacing`。
也就是说：**日志回放过一轮之后，`anc apply` 装的那份东西照样自己站起来了** ——
残留的 `api.sock` 没挡住它，`cc-connect is running projects=3`。

**为什么它起得来**：上游单元是 `WantedBy=default.target` 且已 `enable`，加上 `loginctl` 的 `Linger=yes`
—— 用户级 systemd 管理器在开机时就起，不等人登录。**三条缺一条都不成立**，
所以这三条都得在（该由 `anc doctor` 查，归 #17）。

**2026-10-08 复验（换了 HEAD 的装载体再走一遍）**：把 HEAD 的 `anc_linux_amd64` 落到客机后，
`anc apply --apply` 全 7 步真做成功（退出码 0、`platform ready 3/3`、探针三个 🟡）。**关键是第 4 步
`daemon install --force` 会把单元整份重写成上游自己那份** —— 重写之后 `is-enabled=enabled`、
`Linger=yes` 都还在，`systemctl reboot` 后服务再自己起来（新 `MainPID`、`NRestarts=0`、无人登录）。
幂等：紧接着第二次 `--apply` 同样 exit 0、`fp 1104018a058f` 不变、`MainPID` 又换了一个、ready 仍 3/3。

**一处如实记（客机时钟）**：这台 VM 的 RTC 存的是**本地时间**，而系统声明 `RTC in local TZ: no`，
于是每次开机内核先把系统时钟读快 8 小时，几十秒后才被 NTP 拉回（实测：daemon 就在那个偏窗里启动，
它头几条日志的时间戳因此是错的）。真实服务器 RTC 走 UTC 不会这样，但值得记一句：
**断电后有一小段时间「时钟是错的」**，任何依赖时间的校验（TLS / token / TTL）都跑在那个窗口里。

**Windows 腿现在是缺口（明确标出）**：上游那份任务的触发器只有 `<LogonTrigger>`。
也就是说，**断电重启后如果没人登录这台机器，这个 daemon 就不会起来**；再叠上 §7.1.8 记的电源条件
（`DisallowStartIfOnBatteries` / `StopIfGoingOnBatteries`），笔记本上一拔电就直接停。
要补「开机就起」得给任务加启动触发器 —— 那要么以 SYSTEM 跑、要么存下账号口令，
是一条**替客户改上游默认**的决定，归议题 #42 拍。

**「这次兜底到底成没成」现在有人说话了（2026-10-08 实测）**：`anc doctor` 多了一组**只读**自检 ——
Linux 查那三条前提（单元在不在 / `is-enabled` / `Linger`），Windows 查触发器里有没有 `BootTrigger`
与两条电源设置。**没装机就明确跳过**（不给刚 `init` 的机器平添红字），**不替客户改上游默认**，
也不设门禁（doctor 的结论行本来就写着「⚠️ 不一定是阻断项」）。实测：Linux 真 VM 三条全绿
（`单元 /home/anc/.config/systemd/user/cc-connect.service`、`enabled`、`Linger=yes`）；
未装机那条路（`HOME=/tmp/nohome`）如实报「单元不在（还没 apply）—— 跳过」；
Windows 真机如实报出「触发器只有 `LogonTrigger`」+「电池供电不启动 / 拔电就停」两条。
⚠️ **如实记**：`Linger=no` 与「单元 disabled」这两条**没真造出来跑过** —— 判据的解析有用例
（`parseLinger`），绿档那条也已经把「exec → 解析 → 分岔」整条路走通了，但要把「兜底坏掉」
那一刻真造出来，得先停 linger / 停单元，没动。系统单元（root 那份）同理没跑过。

**顺带改掉的一处误导**：`anc apply` 第 7 步原来在 `platform ready 0/N` 时一律把 FIX 指向凭据桥。
现在它先拨一次 socket 分岔（`startupHint`）：**进程根本没起**（拨不通）与**起来了但没到 ready**
是两种病，要查的地方完全不同。上面那台 Windows 机器今天就正好撞在第一种上：
同一台机器、同一时刻，输出从「凭据桥没生效」变成了「gateway 没在跑 + 看计划任务（含电源条件）」。

### 7.1.12 留痕的「决策与执行」层：`anc timeline`（2026-10-08，真沙箱 + 看板第七页）

**问题**：§7.1.6 记的是**机器做过什么**（事实层）、§7.1.7 记的是**该不该做**（判断层）。
还缺一层：**这件事卡在哪、谁拍的板、agent 怎么执行的、成没成、根因是什么** ——
人和 agent **自己写下来**的那条流水。它**不是审计**：谁**尝试**做了什么（含越权尝试）属于事件面，
还没实现（页内如实列缺，不假装答了）。

**为什么是 append-only JSONL，不是数据库**：vault 归 git 管，留存记录最需要的恰恰是
diff / review / 回滚 —— 二进制库这三样全丢。代价如实记：过滤与分页是**扫描**而非索引，
按「一条任务几条记录」的量级可忽略（`limit` 上限 200 只是防 `?limit=100000` 把一次请求变成全库扫描，
不是门禁）。

**三条口径**（都刻意留了「以后不用重构」的余地）：

| 口径 | 落法 |
|---|---|
| 真相源 = `timeline/<YYYY-MM>.<作者>.jsonl` | 按**作者**分片 —— 两个 agent 同时写也碰不到同一个文件，并发就地消掉，不靠锁 |
| 词表不锁死 | 配色只认 `done`(绿) / `running`(黄) / `blocked`·`failed`(红)；**认不出的词原样保留、画灰**（`unknown` 档）—— 以后加词只改数据 |
| 一个 case 多行 = 一次推进 | 折叠取最后一条当当前态，`recent` 带最近 5 条；**历史一行都不删** |

**不设门禁**：`add` 不校验「谁能写」（那是授权层 #32–#35 的事）；`kind` 是约定、不是白名单，
写别的也收。目录不存在 = 还没开始记 = 空时间线，**不是错**（刚 `init` 的机器不该因此报红）。

**实跑过什么（照实记）**：

- Go 侧：`go test -count=1 ./...` 十一包全绿（新增 `internal/timeline` 12 条、`internal/board` 时间线 6 条）。
  用例盯的是真踩过的坑 —— 分页游标里的 `+` 在 query 串里被解成空格（照抄 `next_before` 必踩）、
  空折叠结果必须是 `[]` 而不是 `null`（前端 `.length` / `.map` 会崩）、坏行只报 `文件名:行号`（不带本机路径）、
  参数错不当 500。
- CLI：临时 vault 里加 4 条 → 折成 3 个 case → 按作者落到两份文件 → 三档计数与
  `--json` / `--limit`（给出 `--before` 游标）/ `--band red` 全对。
- 看板第七页：新装 `dist/anc.exe` 起在 `127.0.0.1:8787`，在真沙箱 vault 里记 4 条（含一个**认不出的 status 词**），
  `--dump-dom "#/timeline"` 断言真数据真渲出来（`feishu-reply` / `waiting-upstream` / `灰 · 认不出的词`），
  并与 `/api/timeline` 的 `counts`（1 黄 / 1 红 / 1 灰 case）对了账。
- **没验的**：真机观感（字体 / 毛玻璃 / 屏宽折行）；控制台错误数（要 CDP 才测得到）；
  导航切换 / 浏览器后退 / 零控制台错误那三项仍是**六页时**的结论，加了第七页之后**没有重跑**。

**顺带改掉的一处自相矛盾**：总览页原来手抄了一份「两块还没接数据源」（数据流 / 原料），
而那两页早就是「已接入」了。现在从 `PAGES` 现算 —— 导航与总览只有一处口径。
### 7.1.13 接入面的信封：`anc envelope`（2026-10-08，单元 17 条 + 活体实测）

#44 的第一块落地：**与 harness 无关的那一层**。网关可替换（#30 已盘 CC / Codex / DSH / Hermes /
OpenClaw 五家怎么接），可换的是网关，**信封不变** —— 所以先把信封立住，再谈阶梯与入口。

**形状**（`internal/envelope`）：`id` / `ts` / `who` / `on_behalf_of` / `scope{domain,project}` /
`kind` / `body` / `refs` / `needs`。**只有 `id` / `ts` / `who` 必需** —— 其余留空不拦。

**两段报，刻意分开**：

| 段 | 判什么 | 怎么报 |
|---|---|---|
| 结构（`Parse`） | 读不读得懂 | 缺 `id` / `ts` 非 RFC3339 / 缺 `who` → **拒收**，退出码 1 |
| 绑定（`Bind`） | 指向的东西在真相源里**存不存在** | 一条发现，档位走生效规则表 |

分开的理由：结构错是「你格式写错了」，绑定错是「我们公司没有这个人」——
混在一起报，人分不清该改 JSON 还是该查人事。

**七条发现，整组默认 warn**：`envelope.who.unknown` / `on_behalf_of.missing` /
`on_behalf_of.unknown` / `scope.domain.unknown` / `scope.project.unknown` / `kind.missing` /
`kind.unknown`。这个档不是随口定的 —— **域表 / 项目表立的就是这个先例**：新能力先「看见」、
不先「拦」，一条红线都不设。要提红写 `company.md` 的 `policy:` 段（`= fatal`），要关掉写 `= off`。
七条全部登记在 `org.DefaultRules` 里，`anc org check --rules` 能看见；
**有测试盯着这件事** —— 规则 id 一旦拼错没登记，`Policy.Level` 的兜底是 `fatal`，等于偷偷上了门禁。

**身份不许自报**：`who` 必须能在 `members/` 里解出来；`on_behalf_of` 支持 `member:名字` /
`role:岗位` / `domain:slug` 三种前缀，也接受**裸名字**（按 成员 → 岗位 → 域 的固定顺序解，
不猜）。这是 #44 的验收判据之一。

**表为空就不报**：没有 `domains.md` / `projects.md` 的 vault，`scope` 一条都不出 ——
同 `LoadDomains` / `LoadProjects` 的谦让：**门禁不许长在别人的文档上**。

**判据摊开**：`check` 顺带把六问打出来（谁 / 代谁 / 哪块业务 / 要什么 / 证据在哪 / 要不要人拍）。
后两问**只列原文**：要不要人拍由信封自己说（`needs`），我们不从字符串里猜意图。

**实测**（2026-10-08，本机 `dist/anc.exe`，对着 `testdata/orgs/domains` 与真沙箱 vault）：

| 用例 | 结果 |
|---|---|
| 干净信封（`who=alice`、`on_behalf_of=member:alice`、`scope=trade/trade-q3`、`kind=ask`） | 六问全答出，**0 项发现**，exit 0 |
| 到处不对（`who=nobody`、`bot:alice`、`nope/nope-q9`、`escalate`） | **5 项 warn**，一条不落，exit 0（不拦） |
| 缺 `who` | 拒收：「缺 who —— 判据要求只靠信封就能答出谁」，exit 1 |
| 空域表 / 空项目表的 vault | `scope` 两条**不报**，其余 3 条照报 |
| `policy: envelope.who.unknown = fatal`（实测：把影响域拷进临时 vault 改 `company.md`） | 变 `🔴`，**exit 1** |
| `policy: envelope.kind.unknown = off` | 那条**整条不出** |
| `anc org check --rules` | 七条 `envelope.*` 都在表里，覆盖标记 `★` 也对 |

`gofmt` / `go vet` / `go test -count=1 ./...` **十一包全绿**（envelope 包 17 条）。
路上真踩到并修掉一处：`Bind` 一开始直接 append 裸切片，绕过了 `Report.Add` 的丢弃逻辑，
**`off` 档关不掉** —— 改成走 `*org.Report` 后才真的能关（有测试锁住）。

**仍未落**（#44 的其余三项，别当做了）：入口的抽象边界（收 / 发 / 会话归属 / usage / 拒答 五件事）、
能力降级阶梯（MCP → 插件 → skills → CLI → 只读 HTTP）在客户端侧怎么发、**入口唯一性**
（一机一网关 = 一份日志 / 计量 / 审计）怎么落代码。信封本身也**还没冻结** ——
等一个真实接入（#46）来回压一遍再定。
### 7.1.14 接入面真接入：MCP（2026-10-08，CC 2.1.291 + Codex 0.160.1 真调）

#44 的验收里有一条是「网关从 A 换成 B：信封与议题都不变，只有 adapter 换」。
这一条**不能靠纸面**，所以真接了两个 harness。

**服务端**：`anc envelope serve`（`internal/mcp` + `internal/envelope`）。
极小 MCP（streamable HTTP），只做 initialize / notifications / tools/list / tools/call；
对外**只暴露一个工具** `anc_send_envelope` —— 能力面收在一处，是「入口唯一性」的第一步。

**无状态**：不发 `Mcp-Session-Id`、不记会话、GET / DELETE 一律 405（不提供 SSE 流）。
这是 #45 那条待验主张（「新的 MCP 协议是无状态的，可能更适合」）在服务端的第一次践行：
一封封自带路由信息的信，**收到就能办**，本来就不需要先握手养出一段会话。

**实测（本机真调，不是模拟）**：

| harness | 客户端报的协议版本 | 链路 | 结果 |
|---|---|---|---|
| Claude Code 2.1.291 | `2025-11-25` | initialize → notifications/initialized → tools/list → tools/call | ✅ 信封落盘，六问六答回到 harness |
| Codex 0.160.1 | `2025-06-18` | initialize → tools/list → tools/call | ✅ 同上 |
| 裸 JSON-RPC（探针） | `2025-06-18` | 同上 | ✅ 同上 |

**两家的协议版本号不一样** —— 服务端**原样回客户端报的那个**才都通。写死一个版本，
报另一个版本的客户端就走了。这是实测教出来的，不是设计出来的。

**没动全局配置**：CC 走 `--mcp-config` + `--strict-mcp-config`（只在一次运行内接线）；
Codex 走 `-c mcp_servers.anc.url=...`。两个都是「用完就走」。

**Codex 侧的一个坑**：默认审批策略 `never` 会把 MCP 工具调用拦下
（`MCP tool call requires approval, but approval policy is never`）——
要 `-c 'mcp_servers.anc.tools.<工具>.approval_mode="approve"'`。这是 harness 侧的策略，
不是 ANC 侧的问题，但接的时候必须知道。

**身份对不上的那封也真跑了一遍**（经 CC）：`who=nobody` / `on_behalf_of=member:ghost` /
`kind=escalate` → 三项 🟡 **照收**（落盘、并把发现回给递信的人），与「默认 warn、不先拦」一致。
真沙箱的 `domains.md` / `projects.md` 都是空表，所以 `scope.domain=nope` **一条都不报** ——
「门禁不许长在别人的文档上」在真接入里也被验证了。

**日志落哪**：`<data>/envelope/<YYYY-MM>.<who>.jsonl`（按 who 分片、append-only）。
放 data 目录而不是 vault（git），两个理由：① 这是**流量**不是真相，每封信进 git 就是每天刷 diff
（同 §13 Q15 的理由）；② vault 顶层目录会被 `scanRouting` 当成「数据来源」扫进 persona 路由表 ——
一封信都不该改变谁的数据来源。

**门禁与「拒收」**：默认 warn = 照收；被 `company.md` 的 `policy:` 提成 `fatal` 的 = **拒收**
（不写日志、回 `isError=true`）。依据是 `fatal` 在这库里的定义就是「阻止落盘」（`org/rules.go`）。
`TestIngressFatalRejectsAndDoesNotLog` 锁住这条，也锁住「拒收的信不落盘」。

**实测覆盖**：`gofmt` / `go vet` / `go test -count=1 ./...` **十二包全绿**
（`internal/mcp` 8 条、`internal/envelope` 22 条、CLI 侧 8 条）。

**仍未落**：入口的抽象边界（收 / 发 / 会话归属 / usage / 拒答 五件事，现在只落了「收」）、
能力降级阶梯（MCP 已是主路；插件 / skills / CLI 未接）、入口唯一性**只做到「一个入口一个工具」**，
还**没有**做「一机一网关 = 一份日志 / 计量 / 审计」的强制。信封仍未冻结。
### 7.1.15 授权表：`grants/`（2026-10-08，单元 12 条 + 沙箱真跑）

SPEC §6 的授权模型（谁 / 什么动作 / 什么客体 / 期限 / 署名）落到 `grants/`。
**到这一刻为止只有落点与结构校验** —— 执行层（怎么行使、到点怎么失效）一行代码都没有。

**形状：一个 grant 一个文件**（`internal/org/grants.go`）。为什么不是「一张表一行」：

| 选 | 好处 | 代价 |
|---|---|---|
| **一个文件一条**（采纳） | 落点天然可分层（给人快速定位）；改一条只碰一个文件，谁提的谁批的能在 git 里逐条追 | 条数一多文件就多（扫目录比扫表慢，现在这个量级无所谓） |
| 一张表一行 | 一眼看全 | 表会长成一坨，`slug` 得另起一列当主键；两个人同时改同一张表 = 天天冲突 |

**扫描递归、分层不管**：目录怎么分层是给人看的（按部门 / 按项目随你），换分层不用改代码；
顺序按路径定，快照 / diff 才有确定字节。

**判据：有 frontmatter 才算条目**。落点里的说明文件（`CLAUDE.md` / `README.md`）按名字跳过；
名字不在忽略名单、又没有 frontmatter 的**报出来**（`grant.file.unparsable`）—— 本包一贯的
「宁可拒绝，不做静默猜测」。同一理由多一条 `grant.file.multiple`：正文里还有第二段 `grant.`
frontmatter 就报（第二段压根不会被读，不报 = 那条授权静默消失）。判据收得很紧
（一行正好 `---` + 紧跟的第一行非空内容以 `grant.` 开头），正文里的分隔线不会误报 —— 有测试锁住。

**只做结构校验，语义只到 `from`**：五个必填字段一个不缺 + `from` 解得出成员或岗位。
`to` / `object` 的客体词表（信道 #33、看板分区 #7）**还没有定义处** —— 现在校验它们只能是猜，
所以**故意留白**。「这条权该不该给」也不归校验器：那是提案 + 人确认的事。
域不能当 `from`（`domain:trade` → `grant.from.unknown`）：域是**客体**不是主体。

**十条规则整组默认 `warn`**，一条红线都不设 —— 域表 / 项目表 / 接入面立的就是这个先例，
理由一样：**授权是新能力，先「看见」不先「拦」**。
⚠️ 这是**加了十个可关的开关**，不是加了十个门禁：默认全 `warn`、全部允许 `off`，
要提红得自己在 `company.md` 的 `policy:` 段写（`grant.ttl.missing = fatal`）。
`anc org check` 另外加了一行 `授权 N 条` —— 「先看见」得先看得见。

**看板投影全量、bot 侧零条**：投影（`anc.board/v1`）带 `grants`，十条字段原样搬、不筛选不脱敏；
bot 侧那个「只出与我相关的几条」的视图**还没做**。两个消费者、两个视图，**不共用一份产物** ——
共用迟早把看板的全量漏给 bot。

**期限必带**（SPEC §6，无默认）。出厂口径 **30 天**（2026-10-08 拍板：15 天太短、一个月够用）；
表里写多少执行层就按多少算。授权表里**不放 token / 不放密钥** —— 它记的是「谁被允许做什么」，
凭据怎么发是执行层的事（这也是「token 明文进上下文」那个担心的正面回答：表里根本没有 token）。

**实测**（2026-10-08，本机 `dist/anc.exe`，对着沙箱 vault 的临时副本 —— 不动沙箱本体）：

| 用例 | 结果 |
|---|---|
| 两条干净 grant（`member:alice → role:manager`、`role:manager → member:alice`） | `授权 2 条`，0 项 grant 发现，exit 0 |
| 没有 `grants/` 目录 | `授权 0 条`，其余输出**逐字不变**，exit 0 |
| 缺 `ttl` | `grant.ttl.missing`，exit 0（不拦） |
| `from: domain:trade`（域当授权者） | `grant.from.unknown`，exit 0 |
| `action: bless` + 非 ASCII 文件名 + 没 frontmatter 的 md | 三条一起报（`action.unknown` / `slug.format` / `file.unparsable`），exit 0 |
| 一个文件里塞两段 `grant.` frontmatter | `grant.file.multiple`，exit 0 |
| `policy: grant.ttl.missing = fatal` | 变 🔴，**exit 1**（不落盘） |
| `policy: grant.action.unknown = off` | 那条**整条不出** |
| `anc org check --rules` | 十条 `grant.*` 全在表里，覆盖标记 `★` 也对 |
| `anc org export` | `grants` 段 2 条、十条字段齐全；`grants/` **不在** `routing` 里 |

`gofmt` / `go vet` / `go test -count=1 ./...` **十二包全绿**（`internal/org` 新增 12 条用例，
其中 `from` 那条跑 7 组子例）。

**仍未落**（别当做了）：执行层（**期限到点怎么失效 / 怎么级联 / 能不能续期**）、
bot 侧的按人过滤视图、看板上「谁能做什么」那个页（投影里已经有数据，页上还没画）。
顺带记一条同日拍板：**信道的默认值是「不通」**（跨域默认不通，要通就写一条 grant）——
但信道本身是 #33，还没实现。

### 7.1.16 凭据键名的账：`Plan.SecretKeys`（2026-10-08，单元用例 + 真二进制 dry-run 复现）

**症状**（审计报告 P0-4 的现场，比报告写的更严重）：成员名不是 ASCII 时 —— 比如 `张三` —— 渲染器
照常写出 `app_secret = "${ANC_FEISHU_SECRET_张三}"`，但体检是**拿正则回读产物文本**算「要哪些键」，
那把正则只认 `[A-Za-z_][A-Za-z0-9_]*`，中文键它根本看不见。于是体检拿着「少数了一个键」的清单
报 **✅ 齐**，现场等来的是一个没有凭据、起不来的 bot。带 `-` 的名（`alice-2`）同样中招。

**修法（治本）**：键名在**写 `app_secret` 那一行当场记账**，记进 `Plan.SecretKeys`；体检吃这份账，
不再回读文本。账与回读在 ASCII 名这一档必须一致（用例断言），在非 ASCII 这一档**必须不一致** ——
回读看不见它，这正是账存在的理由。键名只有一个出处：`render.FeishuSecretKey`。

**顺手补的两处「说出来」**：

- 新规则 `member.name.format`（**warn，不是红线**）：成员名拼不出合法键名时报警并点名那个键。
  为什么 warn 不 fatal —— 存量 vault 不该因为一条新规则突然渲染不出来；拦的那一处本来就在装载
  （缺键 = 停），这条只负责让人看见。想变红：`policy: member.name.format = fatal`。
- 体检报缺键时，把「**只是没写**」与「**根本写不出来**」分开说。后者照着「补键」那条 FIX 去补也
  永远补不上（解析器与装载用同一把尺子 `apply.LegalKey`），所以直接给改名的 FIX。

| 场景 | 命令 | 结果 |
|---|---|---|
| 全 ASCII 名（基线） | `anc apply <vault>` | `引用 2 个：…_ALICE, …_DEVBOT` + ✅ 齐，exit 0 |
| 成员名 `张三`（中文） | 同上 | `引用 2 个：…_张三, …_DEVBOT` + 🔴「不是合法的环境变量名」，exit 1 |
| 成员名 `alice-2`（带 `-`） | 同上 | 同上（`-` 同样拼不出合法键名），exit 1 |
| 干净 fixture 四份（one / six / domains / disabled） | `internal/org` 用例 | `member.name.format` 一条都不报 —— 规则不变成噪音 |

`gofmt` / `go vet` / `go test -count=1 ./...` **十二包全绿**（`internal/render` 2 条、`internal/org` 2 条、
根包 1 条新用例）。

**未做**（别当做了）：成员名与 `display_name` 的彻底分离（成员 id 用 ASCII slug、中文只住
`display_name`）—— 那是 schema 变更，要人拍板；本轮只把「静默」变成「出声」。

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


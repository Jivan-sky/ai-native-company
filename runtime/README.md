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

# 4. 生成 org 真相源骨架（vault）—— 公司 / 角色 / 成员 / 数据目录路由
anc org init ./climax-vault --client 某车队 --id climax --members alice,bob

# 5. 校验骨架（--rules 打印生效规则表），然后渲染 gateway 配置
anc org check ./climax-vault --rules
anc render ./climax-vault --apply

# 6. 服务单元：默认只打印，--apply 才真落盘；install 再加 --apply 才真装载
anc service render --org ./climax-vault --bot alice --goos darwin
anc service status --org ./climax-vault --bot alice
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

## org 真相源 → gateway config（`anc render`）

真相源是 git 里的 markdown + frontmatter，**只存岗位与职责**。`anc render <vault>` 把它编译成
cc-connect 的 `config.toml`；**默认 dry-run**，落盘必须显式 `--apply`（时间戳备份 + 原子写）。

```
vault/
	├─ company/company.md         公司自描述 + defaults + policy（规则覆盖）
	├─ roles/<role>/persona.md    角色层：职责 / 风格 / 术语表 + model / mode / allowed_tools
	├─ members/<name>/persona.md  成员层：display_name / role / model / feishu.*
	├─ domains.md                 业务域表（罗盘）：slug / name / what / who / data / sources / terms
	└─ <数据目录>/CLAUDE.md        顶层数据目录 → persona 段 3 的路由表（首句即说明）
```

两道差分门，防的都是「不可逆」而不是「你不听话」：

- `--adopt`：目标配置没有 anc 指纹 → 拒绝覆盖（那是别人的文件，不是我们的产物）。
- `--allow-scale`：本次会新增 / 删除 project → 拒绝落盘（防一次误操作静默把 bot 上下线）。

两道门互相独立：接管他源配置时 project 集合必然全变，所以 `--adopt` 与 `--allow-scale` 都要显式过。

### 规则表：校验住在数据里，不埋在实现里

`company.md` 的 `policy:` 段可以逐条改规则的处置档：

```yaml
policy:
  member.feishu.open_id.format: off      # 平台是可变层，格式不该拦人
  role.allowed_tools.empty: fatal        # 这个客户要求更严
```

- 三档：`fatal` 拦落盘 / `warn` 照走但回显 / `off` 不报。默认档只有一条判据 ——
  **拦不可逆的**（bot 下线、对外答错、覆盖人的手写文件）；可逆的小事只告警。
- 标 `[红线]` 的不许降级（SPEC §6-5 无例外红线、§171 生产事故）。
- 规则 id 拼错、级别值非法 → 直接拦：拼错等于「你以为关了其实没关」。
- 全表随时可查：`anc org check <vault> --rules`。
- 非红档发现**必须回显**（`anc render` / `anc org check` 都会打），门禁不许静默。

### 业务域表：桥上的罗盘（`domains.md`）

一块业务一行。它是**「域标签」客体的唯一定义处** —— persona 上下文、未来授权的「域」作用域、
看板的分区都从这一张表长出来，所以只有一处维护。

```
| slug | name | what | who | data | sources | terms |
|---|---|---|---|---|---|---|
| trade | 大宗贸易 | 进出口合同的签订、执行与结算 | manager | projects | 邮件、合同扫描件 | 保留英文：pipeline |
```

- `slug` 机器用（进文件名与代码），必须小写 ASCII；`name` 给人看。
- `who` 填**岗位**：渲染时从 `members/` 现算成人名（`经理（Alice、Bob）`），
  所以人事变动不用改表，也不会「表里张三、实际李四」。同一张表既给管理层看「谁在做哪块」，
  也给业务人员查「这件事该找谁」。
- `data` 是**指针**（vault 顶层目录名），不是说明 —— 目录的说明归该目录的 `CLAUDE.md`。
- **列可以多，认不得的列忽略**；列序随便排，按表头认，不按位置猜。
- 表可以空着（只留表头）= 还没划域，不算错。

渲染边界：**自己所属的域整行进 persona（段 8）**；别人的域只给一行目录
（域 / 名称 / 是什么 / 找谁），**不给它的 `data` 与 `terms`**。要跨域就按「找谁」接头、走授权。
**没有 `domains.md` 的 vault，persona 一个字节都不变**（存量零影响）。

域表里的字同样要过 persona 的体检（三引号 / `${}` / 绝对路径 …）—— 它也是要进上下文的文本。

规则默认整组 `warn`：域表是新能力，先观察；想卡死在 `policy:` 段写 `domain.slug.format: fatal`。

### 只读投影出口（`anc org export`）

```
anc org export <vault>            # stdout 出一份 JSON，消费方自己重定向
```

给**看板 / 前端**吃的只读视图：公司 / 角色 / 成员 / 业务域 / 数据路由，
带 `schema`（`anc.board/v1`）—— 字段增删一律改版本号，前端据此判断能不能吃。

- **纯函数**：同一份真相源 + 同一时刻 → 逐字节相同，可以按字节 diff。
- **只读**：不写真相源、不碰机器、不生成任何能给 gateway 吃的东西。
  这也是它和 `internal/render` **分两个包**的原因 —— 混在一起，迟早有人拿「生成看板」的路径去生成配置。
- **刻意不导出** `feishu.app_id` / `open_id` / `extra_allow_from` / `allow_chat`：
  看板是**观测面，不是凭据面**（SPEC §2.3 / §6-3）。「这人绑没绑好飞书」属于阶段 C 的探针。
- `who` 是岗位，投影里派生成 `who_label`（`经理（Alice Wang）`），
  与 persona 用的是**同一处口径**（`org.WhoLabel`）—— 免得看板和 persona 各存一份名单。
- 非红档发现走 **stderr**，stdout 恒为纯 JSON（有测试盯着这条）。

### 验证到了哪一步（别把「一致」当成「已验证」）

- `--check` 比的是「现在这份 == anc 上一轮生成的」。它能抓人的手改、能抓 org 的变化，
  **抓不到渲染器自己的 bug**，也**不代表 gateway 已经吃下这份配置**。
- 真实拉起 + 功能探针（kickstart + 90s 窗口）在阶段 B 的服务单元里落地。
- 测试三层（`go test ./...`）：性质测试（确定性 / 往返读回 / 时间只影响指纹头）、
  负面断言（**只断规则 id，不断文案**）、一个结构快照 `testdata/golden/one.snapshot`（`-update` 重写）。
  不存全文 golden —— 它的失败模式是「改一个字就红」，然后人就会习惯性 `-update`，测试就死了。

## 服务单元（`anc service`）

一个 bot 一套**用户级**服务定义 —— macOS LaunchAgent / systemd --user / Windows 登录时计划任务。
不写系统级 daemon、不需要管理员权限；**要哪个账号跑，就以哪个账号执行 install**，
所以单元里不写账号（D1 隔离档没拍板也不影响这份单元）。

| 平台 | 落点 | 装载 |
|---|---|---|
| darwin | `~/Library/LaunchAgents/com.anc.<公司>.<成员>.plist` | `launchctl load -w` |
| linux | `~/.config/systemd/user/anc-<公司>-<成员>.service` | `systemctl --user enable --now` |
| windows | `~/anc/tasks/<公司>-<成员>.task.xml` | `schtasks /Create /XML` |

- `render` 默认只打印，`--apply` 落盘；`install` / `uninstall` 没有 `--apply` 就只打印将执行的命令。
- 单元同样带 `anc:generated` 指纹：没有指纹的现有文件（手写或他源）拒绝覆盖，`--adopt` 才接管。
- `status` 是只读的：看单元在不在、指纹对不对、服务管理器认不认它。
- **注意**：`anc serve` 尚未实现（阶段 C/D），现在 install 只会得到一个反复退出的服务 ——
  命令会自己把这句话打出来，别把「装上了」当成「跑起来了」。

### `anc org init` 生成的骨架

```
<vault>/
├─ company/company.md         公司自描述 + defaults + policy（规则覆盖）
├─ roles/<role>/persona.md    角色层：职责 / 风格 / 术语表 + allowed_tools 等
├─ members/<name>/persona.md  成员层：display_name / role / feishu.app_id / feishu.open_id
└─ <数据目录>/CLAUDE.md        首句即 persona 路由表里的说明
```

**骨架不是成品**：`app_id` / `open_id` 是占位符，`allowed_tools` 故意留空（会一直告警顶着），
直到人把它们填成真的。生成完会自动跑一次校验并把非红档发现打出来。

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
| `DESIGN.md` | 运行层落地设计 v0.1（草案；含 4 条待拍板） |
| `main.go` | 装配器（`init` / `doctor` / `assets` / `render` / `org check` / `version`） |
| `render.go` | `anc render` / `anc org check` 的 CLI（dry-run 默认、原子写、两道差分门） |
| `board.go` | `anc org export` 的 CLI（只读投影，无写操作） |
| `internal/board/` | org 真相源 → 看板消费的只读 JSON（纯函数，不含凭据面字段） |
| `service.go` | `anc service render/status/install/uninstall` 的 CLI（用户级服务单元） |
| `orginit.go` | `anc org init`：生成 org 真相源骨架（vault 模板） |
| `internal/org/` | org 真相源的解析与校验：frontmatter、org 模型、规则表（`rules.go`） |
| `internal/render/` | 纯函数渲染：persona 七段叠加 + lint、config 全量生成 + 往返回读 |
| `internal/service/` | 纯函数生成三平台服务单元 + 指纹 + `~/` 展开 |
| `templates/org/` | vault 骨架模板（纯文件，现场可直接改） |
| `testdata/orgs/` | 校验正/负例 vault（one / six / disabled / broken-validate / broken-section） |
| `testdata/golden/` | 结构快照（只锁语义） |
| `templates/` | 客户库模板，**纯文件，现场可直接改** |
| `inventory/assets.md` | 已有资产台账 |
| `build.ps1` | 本机编译 + 交叉编译 + 打包 |
| `dist/` | 产出（见 `build.ps1`） |

## 边界

- 装配器**只写目标目录**，不动系统、不改全局配置（`--git` 用的是局部身份 `anc/anc@local`，不碰你的 git 全局配置）。
- 凭证一律不进仓库。
- 本模块是**工程实现**；ANC 的完整理论口径见 `../SPEC.md`。

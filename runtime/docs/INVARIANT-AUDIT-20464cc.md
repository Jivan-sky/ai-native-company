# 任务结账与审批裁决：不变量审计与修复提案

评审基线为 [`20464cc`](https://github.com/Jivan-sky/ai-native-company/tree/20464cc29a20852f8107b1af6a17f029b7360257)。本文基于 SPEC、运行层源码和已有测试的静态核对。反例给出源码允许的事件顺序，验收表列出修复后的预期；本次没有运行项目或验证故障恢复。方案供讨论，以下设计选择尚未写入 SPEC。

## A1　任务结账

**高严重度。** Redis 保存进行中的唯一状态，结账应在对应任务实例的终态持久化后清理热层。当前 `settledEntry` 遍历时间线，普通记录只要与任务同 case 就算“已归档”。调用者在检查任务是否仍在进行之前就删除热层并返回 `Already`。另一条路径先读任务，再写终态，最后按 ID 无条件删除，中间成功的交接可能被抹掉。[权威边界](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L304-L320) · [结账顺序](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/tasksettle/tasksettle.go#L70-L108) · [归档行及匹配谓词](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/tasksettle/tasksettle.go#L119-L158) · [删除操作](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/hot/redis.go#L606-L615)

### 合法转移

| 起点 | 动作 | 结果 |
|---|---|---|
| `queued / running / review / blocked` | 普通结账 | 返回仍在办理，保留版本、持有人、有效租约和现场。 |
| 进行中 | 对眼前版本显式取消或判失败 | 记录操作者、决定时间和实际终态。显式传入 `running` 等非终态仍不能结账。 |
| `done / cancelled` | 普通结账 | 将该实例的终态归档。业务 `failed` 映射到协议阶段 `done`，归档和统计须保留失败结果。 |
| 已有该实例的正式结账回执 | 重送原请求或补清理 | 回原决定，只补缺项。提出相反终态时返回冲突。 |
| 老数据缺 `stage` | 尝试归档 | 按现有映射从已知 `status` 还原；无法从自定义词判定终态时保留现场、返回原因。 |

`ack` 或 `verify` 负责改变业务状态，`settle` 负责显式归档。普通任务可按现有规则回执完成；进入验收的任务仍支持驳回重提。[阶段及版本契约](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/README.md#L569-L590) · [旧状态映射](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/hot/hot.go#L275-L312) · [结账设计原意](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/DESIGN.md#L3565-L3582)

### 版本、唯一键与写入顺序

- **一个实例只结一次。** 业务唯一键为 `(vault, task_instance_id, settle)`。可复用的业务编号须配不可复用的实例号。同一 case 可含多个工作项。作者、月份、业务结果和版本均不能使同一实例获得第二个结账位置。
- **版本决定请求是否仍合法。** 结账带操作者看到的 `expected_version`。取得与其他任务写者共享的串行保护后，重新读取该实例的版本和阶段，再决定能否提交。新旧客户端都要保护本次读写窗口；旧客户端未传版本时，只能保证服务端本次读到的版本。租约围栏仍用于持有人操作。
- **请求号决定是否重放。** 同一 `request_id`、目标版本与业务参数返回原回执；请求号复用但参数冲突时拒绝。服务端处理重试应先核对已有回执，避免把已成功的同一请求误报为版本陈旧。
- **提交点是 vault 中不可覆盖的结账回执。** 它包含实例号、原版本、终态、操作者、原决定时间、稳定的时间线事件 ID、验收口径、评审人及提交时的证据引用。结账补充的引用与原引用合并。原任务的 `refs` 目前只在热层积累，归档入口单独取本次参数。[验收材料的写入](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/hot/task.go#L374-L391) · [当前归档字段](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/tasksettle/tasksettle.go#L115-L142)

写入顺序为：串行核对实例与版本 → 持久创建唯一回执 → 按稳定事件 ID 写终态时间线 → 条件清除对应实例、版本或结账占位的状态、租约和围栏。回执已提交后，该实例在业务上已经结束。所有能改同一对象的入口，包括交接、回执、人工热层操作和新建同名任务，都要能看见该决定。只在最后删 Redis 时比版本，会留下与已成功交接冲突的旧版终态。[交接的 CAS](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/hot/task.go#L323-L345) · [结账的存储接口](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/tasksettle/tasksettle.go#L23-L43) · [看板、CLI 与 MCP 的共用入口](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/board/tasks.go#L16-L34)

### A1 验收

| 场景 | 最小事件顺序 | 应看到的结果 |
|---|---|---|
| A1-1 普通备注 | T 正在运行 → 写同案进度备注 → 普通结账 | 返回仍在办理。原备注 1 条，正式结账回执 0 份，终态行 0 条。版本、持有人和租约保持原值，可继续工作。 |
| A1-2 交接先赢 | 取消方读 v2 → 持有人交接成功，T 成为 v3 → 提交 v2 的取消 | 返回版本冲突，并给出当前 v3。下一手仍能领取；取消回执和取消时间线各 0 条。 |
| A1-3 结账先赢 | 两方都读 v2 → 取消回执持久化 → 清 Redis 前发起交接 | 交接返回已取消和原回执号。恢复后只有 1 条取消终态。 |
| A1-4 中断与重放 | 分别在回执前、回执后、时间线完成后、清理后但响应前中断，重启并重送原请求 | 回执前失败时热层保留；回执后按原决定补缺；有完整时间线只补清理；响应丢失回原回执。最终 1 份回执、1 条终态、0 条旧实例热状态。同名新实例仍在。 |

Redis 不可达、持久回执存在时，返回“已提交，清理未确认”。若成功查得热层为空且无正式凭证，返回“没有结账证据”。旧 `task-<id>` 要连同实例与终态核对；`hot done` 还允许独立补史。对无法归属到确定实例的记录保留原件，不据此清任务。已有测试分别覆盖正常重试与空时间线中的运行任务，须增加两者组合。[现有测试](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/tasksettle/tasksettle_test.go#L87-L135) · [历史补记入口](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/hot.go#L369-L416)

## A2　审批裁决

**高严重度。** 看板、CLI 和卡片走同一裁决入口。当前顺序为读待批 → 追加时间线 → 追加审计 → 删除待批。两位操作者都可能先读到待批、各自成功写下相反决定。即使只有一位操作者，第二笔写失败后重试也会重复追加第一笔。当前顺序重复点击测试发生在第一次已成功清队之后。[规范](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L718-L731) · [两笔留痕与幂等预期](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/DESIGN.md#L1935-L1962) · [裁决顺序](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/approvals/approvals.go#L419-L445) · [现有重试测试](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/approvals/approvals_test.go#L262-L302)

### 合法转移

| 起点 | 动作 | 结果 |
|---|---|---|
| 本轮待批 | `approve / reject / hold` | 第一个合法且成功持久提交的裁决结束本轮，固定信号、操作者、理由和原时间。 |
| 已裁决、流水或清队待同步 | 重送原请求 | 返回原裁决号和同步进度；后台继续补缺。 |
| 本轮已裁决 | 另一请求提出相反意见 | 返回冲突和调用者有权看到的既有裁决；不新增第二个终局。 |
| 本轮 `hold` 后收到补充材料 | 重新提请 | 建议生成关联旧提案的新 ID。如保留原号，则引入显式新轮次，旧卡只指向旧轮。 |

沿用当前语义：三个信号均结束**本轮审批**。`verdict` 区分同意、驳回和暂缓，时间线的 `done` 指裁决已记录，审计的 `ok` 指裁决动作完成。实际授权由管理者 bot 写入。[三个信号的现有测试](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/approvals/card_test.go#L28-L54) · [权限生效边界](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/README.md#L625-L639)

### 版本、幂等键与恢复

1. **唯一裁决键**为 `(vault, proposal_id, round)`。现有一信封一轮可用固定轮次。请求绑定当时的提案版本或材料摘要，防止旧卡对新材料作决定。唯一键不含信号。当前两条流水的 ID 都带信号，因此仅凭流水 ID 无法裁定谁先赢。[提案身份](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/approvals/approvals.go#L106-L141) · [现有流水 ID](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/approvals/approvals.go#L350-L395)
2. **投递身份单独保存。** 同一请求号、同一身份和业务参数可重放；同号改变信号或理由则冲突。飞书事件 ID 标识一次投递，CLI 和看板可用稳定请求号。兼容旧看板无请求号的重复点击，维持 409 并附原裁决号。已能确认是同一请求的重投返回原结果。[旧看板的 409 契约](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/board/approvals_test.go#L266-L277)
3. **持久创建裁决回执后才算裁决成功。** 同一轮所有入口共享一个不可覆盖的提交点。在回执里固定当时的提案、决定、操作者、身份来源、理由、时间和两条投影事件 ID。区分已认证身份与自填署名，恢复过程不能把恢复程序记为审批人。[现有署名语义](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/approvals/approvals.go#L293-L309)
4. **以已选裁决补齐副作用。** 先核对或补写时间线，再核对或补写审计，两者一致后仅清本轮待批项。网关短期缓存不能代替持久回执。卡片同步向原消息回写相同的终态；平台尚未确认时单列“卡片待更新”。返回值分别说明裁决、留痕及卡片状态。[当前网关去重与收口顺序](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/gateway/relay.go#L167-L208)

### A2 验收

| 场景 | 最小事件顺序 | 应看到的结果 |
|---|---|---|
| A2-1 相反意见 | 看板和卡片都看到 P 待批 → 一方同意，另一方驳回 → 同时提交 | 恰好 1 个胜者；另一方得到冲突。两端查得同一裁决号、操作者和结果。最终 1 份裁决、1 条时间线、1 条审计、本轮待批 0 条。 |
| A2-2 审计写失败 | 同意已持久提交 → 时间线成功 → 审计失败 → 重启 | 用户看到“已同意，审计待补”。恢复只新增缺失的审计行，时间线仍为 1 条。 |
| A2-3 清队失败 | 两条流水完成 → Redis 不可达 → 新驳回请求到达 | 已提交的同意仍有效；读端显示同步状态并禁用该轮裁决。恢复只清原轮，流水各 1 条。 |
| A2-4 响应或投递丢失 | 首次投递在提交前失败后重投；另一次在提交后丢响应，重启后重投 | 前者可完成裁决；后者取回原结果。卡片失败只补原消息，决策、审计和时间线条数不变。 |
| A2-5 暂缓后补料 | 本轮信号为 hold → 旧卡重投 → 补料进入下一轮 | 旧轮展示“暂缓，本轮已记录”，按键停用，授权表保持原状。下一轮可以独立决定。 |

### 两项修复的持久性边界

适用前提是一家公司使用一份权威持久卷，各进程共享同一串行裁决机制。权威回执必须完整且不可覆盖，投影操作可重复执行。JSONL 的每个写者应在同一分片保护下按稳定事件 ID 查重，ID 和原始内容一致才认领已有行。恢复进程在“行已写入、进度尚未标记”的窗口只补记进度。发现半行、同 ID 异内容、历史相反裁决时，保留证据并报告待核对。回执的原子创建、数据同步和目录元数据持久化须按交付平台验证。进程退出与机器断电分开验收。[现有 JSONL 追加](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/timeline/timeline.go#L210-L248)

启动时扫描未完成回执并补账；请求重试也可触发同一恢复逻辑。验收同时清点持久回执、两个分片中的逻辑事件、Redis 的实例和版本，以及用户所见回执。已持久裁决但尚未投影时，读端应显示同步中，不能再提供一个可成功的裁决入口。对旧数据，仅凭证据一致性足以唯一归因时自动补齐；缺少可信依据时不给历史决定补造身份或胜者。

## 其余审计结论

A 类为规范和实现的差异，B 类为实现实际作出但缺少业务不变量的裁决，C 类为给定规范前提下不可达的承诺。以下均对应同一基线，作为修复排期的技术依据。

| 编号、严重度 | 触发顺序、影响与建议 | 依据 |
|---|---|---|
| A3 高 · 授权有效期 | 有效委派 → 授权人离职而历史授权行仍在 → 下次正常跨域行使。入口重新读组织，但匹配只看受予方与域，不能证明当前动作仍获有效授权。应由同一判据检查动作、客体、委派链与时点。此结论限于信封准入。 | [规范](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L644-L663) · [范围声明](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/envelope/gate.go#L15-L36) · [匹配](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/envelope/gate.go#L167-L180) |
| A4 中 · 就绪误报 | S 仅在另一受予者的授权上署名 → S 运行基准 → 与 S 相关的行可以被计作 S 已获得权限。展示、就绪评估与执行须区分“被提及”和“有效持权”。岗位授权在校验和跨域匹配中也得到不同解释。 | [规范](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L641-L649) · [上下文投影](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/ctxpack/pack.go#L488-L510) · [基准](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/bench/bench.go#L273-L289) · [岗位校验](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/org/grants.go#L170-L177) · [岗位匹配](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/envelope/gate.go#L194-L212) |
| A5 高 · 已读无痕 | 合法上下文读取成功 → 审计写失败 → 调用方仍收到成功。入站也可能先记 OK、随后收件落盘失败。区分准入、持久收件与响应，给必记事件可恢复的持久写入点。 | [规范](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L676-L685) · [出站失败](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/envelope_read.go#L84-L111) · [入站顺序](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/envelope.go#L973-L982) |
| A6 中 · 技能格式 | 从已完成 case 生成草稿 → 按指引补四段 → 发布前检查通过。正式格式要求中英触发 description，但正向测试的草稿就缺该字段。发布检查应校验实际待发布的能力件。 | [规范](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L325-L329) · [生成](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/distill/draft.go#L64-L70) · [正向用例](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/distill/check_test.go#L10-L41) |
| A7 中 · 知识来源丢失 | 同一正文的签发原件从 A 更正为 B → 仅接入 MCP 的读者重新取文。索引剥掉来源元数据，响应无法指出签发件。保留受权限约束的原件引用；原件不可访问时说明状态。 | [溯源规范](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L263-L279) · [分歧查原件](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L550-L555) · [建索引](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/retrieval/retrieval.go#L375-L388) · [正文出口](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/search.go#L345-L363) |
| B1 高 · 同秒状态顺序 | 两位作者同秒先后写完成、重开 → 分片按文件顺序读，稳定排序后最后一行覆盖状态。调换真实先后可得到同一份存储内容，还会影响技能候选。业务状态应有前序版本或显式冲突。 | [看板语义](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L392-L395) · [时间精度](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/timeline.go#L83-L93) · [排序](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/timeline/timeline.go#L150-L201) · [折叠](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/timeline/timeline.go#L288-L313) · [候选](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/distill/distill.go#L98-L117) |
| B2 中 · 旧草稿溯源误绿 | K 以方法 A 结案并产生草稿 → 重开纠正为 B → 旧 A 草稿再次检查。“来源通过”只查 K 当前是否绿色。建议绑定结案事件或证据修订号；冻结包也应携带实际技能内容版本，以便事后比对。 | [规范](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L325-L341) · [草稿证据](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/distill/draft.go#L90-L105) · [检查谓词](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/distill/check.go#L297-L311) · [技能投影](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/ctxpack/pack.go#L343-L359) |
| C1 高 · 首笔授权 | 某跨域能力的初始持权者为空 → 新人需要该权。任何合规转授都要求授予者先有此权，所以任何数量的转授都不能产生第一名合法持权者。明确客户如何创设、签署和终止根授权。预置可信根授权的客户不属于此反例。 | [初态及签发前提](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L565-L587) · [角色授权模型](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L641-L647) · [实现边界](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/envelope/gate.go#L35-L52) |
| C2 高 · 副本撤权时效 | 本地副本同步 → 上游撤权 → 下次拉取前本地需要作决定。对本地而言，已撤和未撤两种世界的输入相同，无法保证按上游提交瞬间撤权。应定义生效点、延迟窗口和无法确认新鲜度时的行为。 | [发布模型](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L74-L81) · [同步间隔](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L343-L350) · [即时撤权承诺](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L650-L663) · [本地读取](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/envelope.go#L945-L958) |

建议先修 A1、A2 并用上述故障序列验收。授权部分先明确 C1、C2 的权威来源和生效时点，再统一 A3、A4 的执行与测量判据。验收这些修复时，分别对账终态、审计、证据版本和用户可见结果。

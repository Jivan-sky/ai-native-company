# 任务结账、审批裁决与跨域授权：不变量审计与修复提案

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

## A3　跨域授权何时失效

**高严重度。** 一笔委派登记在册，只证明它曾经签发。SPEC 要求每次行使检查授权者的有效权限与在职状态，子委派不能活得比上游授权久。当前 `Grant` 保存相对期限文本，信封准入对跨域行使检查受予者和域，尚未判动作、到期时刻及授权来源链。下文针对跨域**业务动作的准入**；收件、动作执行成功和审批通过分别留各自的事实。[授权字段](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L575-L588) · [角色、级联与期限](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L641-L664) · [现有记录结构](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/org/grants.go#L22-L33) · [准入边界](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/envelope/gate.go#L15-L36) · [当前匹配](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/envelope/gate.go#L167-L179)

### 有效授权的判据

以“采购岗的乙能否读取贸易域”为例，参与判定的是 **谁实际调用、代谁行使、读什么对象、在什么时刻、依据哪版授权事实**。同一动作应得到可解释的结果。以下是建议写回 SPEC 的契约。

1. **保留签发时的来源。** 每笔授权有稳定 ID，记录授予者、受予主体、动作、带类型的客体、签发生效时刻、可选到期时刻，以及此次签发所依据的上游授权 ID 或客户确认的根授权。岗位派生的授权还要能定位这一次任职。旧工号或岗位名复用时，不能把旧任职的授权接给新任职。根授权如何创设属于本报告 C1；验收以一笔已明确的根授权为起点。
2. **逐段验证，再合并独立依据。** 一条链有效，要求每一段在判定时刻都已生效、未到期、未撤回，授予者当时仍持有该项权，当前受予者仍满足在职、任职或代理条件。子项的动作、客体与范围不得超出父项；无法证明可包含时，只接受明确匹配的范围。中间一段失效使该链的全部后代失效。一人持有两条独立链时，另一条完整有效链可以支持本次行使；失效的子授权不能自行改挂到备用链。保留历史行以供回溯。
3. **区分离职、换岗与撤回。** 离职生效后，当事人本人的工作授权及以其授权为来源的后代均失效，代其行使的 Bot 同样失去这项权。换岗只终止依赖旧任职的链；来源明确、与岗位无关且其他条件仍满足的个人直授权继续有效。按 SPEC 的“岗位生成默认授权集”，建议把岗位模板签发为**绑定任职实例的具体授权**。新任职须能找到对应的新授权，调回原岗也不复活旧授权。业务 Bot 代岗位时核对其已登记的岗位代理关系，与员工本人的任职授权分别计算。
4. **统一时间边界。** 按权威服务时钟比较，采用左闭右开区间 `[生效时刻, 到期时刻)`；恰在到期时已经失效。空期限沿用 SPEC 的无固定截止，但仍受上游期限、离职及撤回约束。相对期限从有证据的签发生效时刻换算一次；重读或重新部署不能刷新起算点。续期产生一笔新的授权，已到期记录仍可查询。
5. **选定生效点。** 建议以有序的权威授权版本生效为准。一次判定使用同版的组织任职、代理关系、授权和撤回记录；换岗的旧任职结束与新任职变更按同一已提交版本读取。在权威版本确认撤回后开始的行使，应看到新版本；本地副本不能确认已追平时，结果为“无法判定”，不放行。预演结果只描述其评估时刻和版本。若收件会排队，真正开始受保护动作前再判；跨越边界的已执行动作保留当时的依据。现有 Git 周期同步不足以证明上游即时撤回，生效点选择与 C2 联动。[副本同步](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L343-L350) · [撤回承诺](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L656-L664)

**签发、撤回与重试。** 签发时核对授予者在该版本已拥有可授出的同一权限，把授权与其上游依据一并持久记录，保留产生它的审批或根授权引用。撤回以授权 ID 为目标追加撤回事实，重复投递同一事件不产生第二次生效；历史回放仍可得到撤回前的结论。当前判定沿原链读取最新有效状态，无需等后台逐级更新所有子项。缓存须同时受快照版本和链上最早到期时刻约束。

**存量与失败处理。** 有可信签发记录的旧授权可补齐起算时间与来源；仅凭文件修改时间、当前同名岗位或目录顺序不能补证。旧 `to: role` 记录需要区分模板与已授给某次任职的有效权，先列受影响的授权与成员，再按可证明的任职映射转换。链中出现循环、缺父记录、期限无法解释或权威数据读失败，且无其他可证明的有效链时，两端均返回“无法判定”并给出待修的数据项。已知到期、已撤回、已离职返回“拒绝”及具体原因。建议先对照输出新旧差异，再按同一策略版本切换基准、预检和正式入口。

### A3 验收：每条反例都查两端

以下主体均已认证；除特别说明，乙不属于贸易域，目标统一为 `read / domain:trade`。给两端提供同一权威快照与受控时钟。对“拒绝”和“无法判定”，正式入口均不得启动受保护动作；基准的权限需求均不记为已送达。

| 编号 | 最小事件顺序 | 验收预期 |
|---|---|---|
| A3-1 授予者离职 | 甲持根授权，签发 G1 给乙；乙据 G1 签 G2 给丙 → 甲离职生效 → 乙、丙各请求一次 | 变更前两人均可读。变更后两人均被拒，原因可定位甲的在职条件和断开的链。G1、G2 的历史记录及离职前的判定证据可查。 |
| A3-2 受予人离职、复职 | 乙持 G1，个人 Bot 代表乙 → 乙离职 → 后以同一工号重新入职 | 乙与该 Bot 对 G1 均被拒。复职本身不给予旧 G1；经独立新授权后才可引用新授权放行。 |
| A3-3 换岗与独立来源 | 乙的 G1 来自采购任职，G2 来自与岗位无关的有效直授；丙的 G3 只从 G1 派生 → 乙调离采购 | G1 与 G3 对应的链失效；乙凭 G2 仍可读，判定证据只使用 G2 的有效链。丙被拒。继任者在新任职授权落定前不能凭职位名沿用 G1。 |
| A3-4 到期边界 | 父项截止 10:00 UTC，子项约定 11:00 UTC → 09:59:59、10:00、10:00:01 分别判定 | 三次依次允许、拒绝、拒绝，后两次显示父项已到期。交换父子截止时刻则由子项先失效。重载后截止时刻不变。 |
| A3-5 无期限、撤回与续期 | 无固定期限授权保持在职且链完整 → 显式撤回 → 重新签发另一笔授权 | 撤回前允许；撤回生效版本起拒绝；新授权生效后可按新 ID 放行。仅查旧授权的有效性始终显示已撤回。 |
| A3-6 副本与排队 | 预演在 v10 通过 → 权威 v11 撤回 → 旧副本收到请求或队列开始执行 | 能读 v11 则拒绝；无法确认当前版本则“无法判定”。不能拿 v10 的绿灯启动动作。历史 v10 的预演仍可按原时间解释。 |
| A3-7 旧资料不全 | 唯一候选旧授权只写期限 `30d`、无可信起算点；另分别考察缺失父项和父链循环 | 三种情况均能终止判定并指出缺口，基准不记绿，准入不放行。补齐可信依据后再判；未补齐前不能静默解释为永久或根授权。 |

## A4　让就绪基准与实际准入回答同一个问题

**中严重度。** 当前上下文的“与我相关”视图把 `from`、`to`、`on_behalf_of` 的命中都展示出来，视图本身明确声明不承担访问控制。`bench.hasGrant` 直接用这些行的动作和客体判定送达。另有相反方向的差异：校验器认为岗位授权不会展开到人，而信封匹配按任职岗位接受。应保留审阅关系的展示功能，把权限就绪这一项切换到正式准入的判定。[相关身份槽位](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/org/grants.go#L257-L273) · [展示视图](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/ctxpack/pack.go#L488-L511) · [基准打分](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/bench/bench.go#L273-L289) · [校验器对岗位的解释](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/org/grants.go#L170-L177) · [信封对岗位的解释](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/envelope/gate.go#L194-L212)

### 共用判定与输出

| 环节 | 修复契约 |
|---|---|
| 输入 | 由两个入口把请求归一为同一份事实：组织、已验证调用主体、实际行权主体及代理绑定、动作、客体类型与 ID、必要的范围、判定时刻、组织与授权快照版本、判定规则版本。基准用同一身份及代理事实作只读模拟，正式入口从可信运行上下文绑定身份。单有一处署名不能创设代理关系。 |
| 动作与客体 | 对具体跨域行使，准入从已声明的操作契约取得实际业务动作与目标，并与请求核对。接受一封信的 `invoke` 审计类别不能替代里面业务动作的 `read/write/invoke`。没有确定动作的旧信封、只有动作而未指定对象的旧基准题，保留可解析性，标成“未形成可对照的具体行使”，不能计入具体授权的通过数。对裸域 slug 和显式域引用做同一归一；目录名仅在有明确且唯一的域映射时才可转换。 |
| 核心判定 | `anc bench` 的 grant 项、`anc envelope check` 和 `anc envelope serve` 调用同一无副作用的有效授权判定，使用 A3 的链规则。保留“与我相关”列表供审阅。正式入口区分“提出申请”和“行使权限”；档位只改变展示或影子评估，生产模式不能把缺少授权的业务行使记为允许。 |
| 可观察结果 | 每笔返回允许、拒绝或无法判定，带稳定原因码、判定时刻和版本；允许时附所用授权 ID 及可追溯依据。几条链都有效时按稳定顺序选证据；多项失败使用同一原因归并顺序。对普通调用方只展示其有权查看的详情。基准中只有“允许”令该 grant 项送达，拒绝与未知分别统计；具体需求仍留在通过率分母中，歧义旧题单列数量及覆盖范围。 |
| 留痕 | 基准和预检只生成评估结果，不能伪造一笔实际行使。正式入口分别记录判定结论和收件或执行结果。相同快照、请求和时刻应给出相同的授权结果、原因和证据；真实调用时权威版本或时刻已变，则重新判断，并让调用者看见差异来自哪一项变化。 |

这里只对齐跨域行使这一项。数据检索的可见范围、普通域内操作、完整工作流成功率有各自口径；单看任务总分无法验收授权一致性。出站上下文如何开放跨域数据仍属 SPEC 已列出的独立问题。[出站上下文边界](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L676-L693) · [基准入口](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/bench.go#L70-L88) · [信封预检](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/envelope.go#L110-L123) · [正式入口](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/envelope.go#L949-L981) · [旧题的动作与客体写法](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/bench/bench.go#L49-L53)

### A4 验收：逐项对账，包含正向对照

对每行使用同一批权限数据，依次从基准、信封预检和正式入口读取**授权结论**。除特别说明，时刻与版本固定。清点每项的结果、原因、证据链和实际行使次数；预演与基准不得增加实际行使计数。

| 编号 | 最小事件顺序 | 验收预期 |
|---|---|---|
| A4-1 正确受予者与仅署名者 | G 由甲授予乙；S 只在该授权行署名，S 自身无此权 → 乙、S 分别请求同一动作和域 | 乙三端均允许、证据为 G；S 的相关列表可展示 G，但三端对 S 均拒绝，其 grant 项未送达。甲如有权，应从其自己的上游依据得到允许，不能拿发给乙的 G 证明自己持权。 |
| A4-2 动作和方向 | 乙只有贸易域读权 → 分别读贸易域、写贸易域、读另一域 → 再查反向受予关系 | 仅第一项允许。其余分别报告动作、客体或有效受予主体不匹配；三端逐项一致。不能从某域一项能力推出任意动作或反向授权。 |
| A4-3 岗位与代理 | 旧记录只写岗位模板，无具体任职授权 → 为现任乙生成 G → 由其绑定 Bot 代乙申请行使；再用无有效代理绑定的模拟主体作对照 | 模板阶段三端均不能把乙判为已获权；生成 G 后乙及有效代理均能引用该具体依据。没有有效代理绑定时三端均不通过，不能改按 Bot 自身或另一个同名主体判。乙换岗后按 A3-3 对账。 |
| A4-4 到期和缓存 | 基准在截止前通过 → 到达截止时刻再做预检和正式准入 → 按新时刻重跑基准 | 截止前的历史结论可复查；截止时刻的三端均拒绝，原因及过期授权一致。同一授权数据版本、不同评估时刻也能产生不同结果。 |
| A4-5 旧题、旧信封与别名 | 同一域分别用规范化引用与无歧义裸 slug 表示；再提交只有 `read` 的基准题、无法归域的目录名或不能确定业务动作的跨域信封 | 前两种表示三端结论和证据一致。缺对象或缺动作的输入显示具体缺项，不能以任意一行 read 授权判绿；若统计单列，报告同时显示未覆盖数量。 |
| A4-6 申请和行使 | 无权的在职主体先提交纯申请，再尝试同域同动作的实际行使；另以旧告警策略对照 | 申请可进入审批并标为“请求”，其业务权限仍未就绪；实际行使拒绝。观察模式的告警收件可单报，不能进入“有权行使”的通过数。 |
| A4-7 结论与副作用 | 同一份有效 G 先经基准、预检、正常准入；再令准入后的收件持久化失败 | 正常时三端授权均允许且指向 G；只有实际入口产生行使尝试。后一次授权结论仍为允许，但收件失败可见，不能当作业务执行成功。审计写失败的恢复由 A5 独立处理。 |

验收时应保留两种对照：对确定有效的 G，三端都应引用同一笔或同一条有效链；只改变受予者、动作、时刻或权威版本之一时，三端都应给出表中预定的答案。这样即便三个入口共用了一个仍然写错的函数，错误也能被独立的业务预期识别。

## 其余审计结论

A 类为规范和实现的差异，B 类为实现实际作出但缺少业务不变量的裁决，C 类为给定规范前提下不可达的承诺。以下均对应同一基线，作为修复排期的技术依据。

| 编号、严重度 | 触发顺序、影响与建议 | 依据 |
|---|---|---|
| A5 高 · 已读无痕 | 合法上下文读取成功 → 审计写失败 → 调用方仍收到成功。入站也可能先记 OK、随后收件落盘失败。区分准入、持久收件与响应，给必记事件可恢复的持久写入点。 | [规范](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L676-L685) · [出站失败](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/envelope_read.go#L84-L111) · [入站顺序](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/envelope.go#L973-L982) |
| A6 中 · 技能格式 | 从已完成 case 生成草稿 → 按指引补四段 → 发布前检查通过。正式格式要求中英触发 description，但正向测试的草稿就缺该字段。发布检查应校验实际待发布的能力件。 | [规范](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L325-L329) · [生成](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/distill/draft.go#L64-L70) · [正向用例](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/distill/check_test.go#L10-L41) |
| A7 中 · 知识来源丢失 | 同一正文的签发原件从 A 更正为 B → 仅接入 MCP 的读者重新取文。索引剥掉来源元数据，响应无法指出签发件。保留受权限约束的原件引用；原件不可访问时说明状态。 | [溯源规范](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L263-L279) · [分歧查原件](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L550-L555) · [建索引](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/retrieval/retrieval.go#L375-L388) · [正文出口](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/search.go#L345-L363) |
| B1 高 · 同秒状态顺序 | 两位作者同秒先后写完成、重开 → 分片按文件顺序读，稳定排序后最后一行覆盖状态。调换真实先后可得到同一份存储内容，还会影响技能候选。业务状态应有前序版本或显式冲突。 | [看板语义](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L392-L395) · [时间精度](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/timeline.go#L83-L93) · [排序](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/timeline/timeline.go#L150-L201) · [折叠](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/timeline/timeline.go#L288-L313) · [候选](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/distill/distill.go#L98-L117) |
| B2 中 · 旧草稿溯源误绿 | K 以方法 A 结案并产生草稿 → 重开纠正为 B → 旧 A 草稿再次检查。“来源通过”只查 K 当前是否绿色。建议绑定结案事件或证据修订号；冻结包也应携带实际技能内容版本，以便事后比对。 | [规范](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L325-L341) · [草稿证据](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/distill/draft.go#L90-L105) · [检查谓词](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/distill/check.go#L297-L311) · [技能投影](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/ctxpack/pack.go#L343-L359) |
| C1 高 · 首笔授权 | 某跨域能力的初始持权者为空 → 新人需要该权。任何合规转授都要求授予者先有此权，所以任何数量的转授都不能产生第一名合法持权者。明确客户如何创设、签署和终止根授权。预置可信根授权的客户不属于此反例。 | [初态及签发前提](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L565-L587) · [角色授权模型](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L641-L647) · [实现边界](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/internal/envelope/gate.go#L35-L52) |
| C2 高 · 副本撤权时效 | 本地副本同步 → 上游撤权 → 下次拉取前本地需要作决定。对本地而言，已撤和未撤两种世界的输入相同，无法保证按上游提交瞬间撤权。应定义生效点、延迟窗口和无法确认新鲜度时的行为。 | [发布模型](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L74-L81) · [同步间隔](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L343-L350) · [即时撤权承诺](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/SPEC.md#L650-L663) · [本地读取](https://github.com/Jivan-sky/ai-native-company/blob/20464cc29a20852f8107b1af6a17f029b7360257/runtime/envelope.go#L945-L958) |

建议先修 A1、A2 并用上述故障序列验收。授权部分先明确 C1、C2 的权威来源和生效时点，再统一 A3、A4 的执行与测量判据。验收这些修复时，分别对账终态、审计、证据版本和用户可见结果。

# new-api 的路由能借什么、不能借什么

> 读的是 [`QuantumNous/new-api`](https://github.com/QuantumNous/new-api)（AGPLv3，Go + Gin）的
> `model/ability.go`、`service/channel_select.go`、`model/channel_priority.go`。
> 只借**结构**，不借代码 —— 那套是"一个请求换渠道重发"，我们是"一件活换人办"，语义不同。

## 一、它到底怎么选渠道（三步，都在一次请求内完成）

| 步 | 机制 | 代码位置 |
|---|---|---|
| ① 分档 | `priority` 降序排成阶梯；`retry` 是**第几次尝试**，直接当梯子索引 —— 第 n 次尝试就用第 n 档 | `model/ability.go: getPriority` |
| ② 档内加权随机 | 同一档里按 `weight + 10` 求和，随机落点。**+10 是保底**：`weight=0` 的档也必有机会 | `model/ability.go: getChannelQuery` |
| ③ 跨组降级 | `group=auto` 时按组顺序试；组内重试用完（`groupRetry >= RetryTimes`）才换下一组，**组内计数与组间计数分开** | `service/channel_select.go: CacheGetRandomSatisfiedChannel` |

两条外挂：

- **渠道亲和**（affinity）：同一会话粘同一个渠道，失败重试时有 `affinityRetryOffset` 抵消，避免"亲和的渠道一直失败却一直重试它"。
- **优先级改动的乐观锁**：`revision = sha256(优先级列表)`，改档时带 revision，对不上就报 `ErrChannelPriorityConflict`。

## 二、能借的三条

### 1. 分档 + 档内加权（直接借）

我们现在的派单是**纯拉取**：`anc task pull` 按入队顺序取，谁先拉谁得。没有"这件活更该给谁"。

可以照搬这套形状：

```
task.Type            ≈ model
agent                ≈ channel
task.Requires(技能/域/权限)  ≈ group   ← 已有：现成的域与岗位
agent.Priority       ≈ priority
agent.Weight         ≈ weight
```

**但"降档"的含义要改**：new-api 里 retry 是"同一个请求换个渠道重发"，我们**不能**这样 —— 一件活被两个 agent 各执行一次就是重复副作用。

所以我们的"降档"只能是**换人之前先交接**（`anc task handoff`），而不是静默重试。这条差别是根本的，不是实现细节。

### 2. 加权里的保底项（值得抄的小设计）

`weight + 10` 而不是 `weight`：weight 全为 0 时仍然均匀随机，不会除零、也不会退化成"永远选第一个"。

我们的 agent 权重目前没有定义处。抄这一条的成本是零，收益是**默认真空不会变成固定偏好**。

### 3. revision 做乐观锁（我们已经在用同构的）

它用 `sha256(优先级列表)` 当 revision；我们的 `ExpectedVersion` / 裁决回执 / 权威源 `version` 是同一种东西。

值得抄的一点：**revision 是整份数据的摘要**，不是每行一个号。这样"有人动过任何一行"都能被同一把尺子发现。

## 三、不能借的两条

| 它的做法 | 为什么我们不能 |
|---|---|
| 失败自动换渠道重发（最多 N 次） | 我们的"渠道"是**会动手的人或 agent**。重发 = 重复执行。我们一律要求显式交接，并把"谁办到哪一步"留在账上 |
| 跨组降级没有边际成本 | 换上游模型只是换个账单；换执行者要**重新校验权限**（A3 的 `Exercise`）。跨域之后那条授权的链可能根本不通 |

还有一条**容易误抄**的：它的 `group` 是"用户分组 → 可用渠道集合"，本质是配额分组；
我们的 `domain` 是**数据可见范围**。同名不同义，别混。

## 四、落到我们的哪两处

### 派单（`anc task pull` 的排序键）

现在：按入队时间。建议：**在入队时算好一个"候选集 + 分档"，pull 时只做档内加权随机**。

三个约束（都是我们已经有的，不用新造）：

1. 候选集必须过 `Permission`（谁能接这件活）—— 已有 `capabilities.md` 与 `agents.md` 的域。
2. **不许自动降档到"没权限的人"**：档位只表达"更合适"，不表达"可以越过权限"。
3. 被跳过要留痕：为什么这一档没人接（权重为 0 不等于不接，是"少接"）。

### token 统计（`anc trail` / `anc audit` 的用量归集）

new-api 的 usage log 结构值得照形状抄：

| 字段 | 我们的对应 |
|---|---|
| prompt / completion / cache tokens | 原样（三档分开，cache 单独算价是它的关键设计） |
| 模型 + 渠道 + 分组 | 模型 + agent + 域 |
| 单价按表达式（分档计价） | 我们的价格表也应支持**按档**（不同模型不同价，不要写死倍率） |
| 按 key / 用户 / 组聚合 | 按成员 / 岗位 / 域聚合 |

我们比它多一维：**成本要能挂到 case 上**（这件事花了多少钱），它只有请求维度。
这一维正是「口径回源」的价值 —— 一个项目的成本不是一个数，是可下钻的账。

## 四之二、已经落地的第一版（`anc task route`）

只做**算法、不派人**：`anc task route <vault> --domain <d> [--role <r>] [--pick]`。

- 默认只输出候选与分档（`--pick` 才在最高档里抽一个，`--seed` 可复现）
- 四档：域+岗位都对得上 / 只对得上域 / 泛用且岗位对 / 泛用
- **域不匹配的 agent 不进候选**（列在 `excluded` 里并写明原因），不是排在最后
- 档内均匀（借 `+10` 保底），**不跨档** —— 最高档没人就返回空，不悄悄降级

实测（demo vault，6 个业务 agent）：

```
$ anc task route testdata/demo-public/vault --domain delivery-ops
第 2 档（域对得上）：shopinitbot / workorderbot
excluded: receptionbot（挂在 cs-ops）、ancopsbot（挂在 anc-platform）…

$ … --pick --seed 100..112
shopinitbot ×5 · workorderbot ×8     ← 档内均匀，且只在这两人里选
```

为什么先只做 dry-run：派单一旦自动写进 `to`，改的就是「谁会被叫起来干活」。
那要先让人看着档位跑几轮、确认划分合理，再打开。

## 五、一句话

它的路由解决的是「**同一个请求，发给哪家上游最合适**」；
我们的派单要解决的是「**同一件活，交给谁办，且他有权办**」。

前者可以随机、可以重试、可以静默降级；后者不行 —— 这三个"可以"在我们的语境里都会变成**重复执行**或**越权执行**。

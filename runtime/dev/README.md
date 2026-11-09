# dev —— ANC 开发验证环境（Docker）

宿主只需要 Docker，不需要装 Go。设计依据与实测记录见 `../DESIGN.md` §7.1.70。

## 一条命令

```bash
./dev/dev.sh all      # fmt + vet + test + cross + smoke
```

## 分条

| 命令 | 做什么 |
|---|---|
| `./dev/dev.sh fmt` | `gofmt -l`，有未格式化的文件就红 |
| `./dev/dev.sh vet` | `go vet ./...` |
| `./dev/dev.sh test` | `go test -count=1 ./...`（根包 + 24 个内部包） |
| `./dev/dev.sh build` | 编到 `.devbox/bin/anc`（**Linux 二进制，宿主不执行**） |
| `./dev/dev.sh cross` | 六目标交叉编译：linux/darwin/windows × amd64/arm64 |
| `./dev/dev.sh smoke` | 端到端：装配 → 真相源 → 渲染 → 投影 → 热层 → 看板 |
| `./dev/dev.sh board` | 起常驻看板（redis + board），地址 `http://127.0.0.1:8787/` |
| `./dev/dev.sh down` | 停掉 redis / board |
| `./dev/dev.sh clean` | 删 `.devbox/`（二进制 / 沙箱 vault / 日志） |
| `./dev/dev.sh sh` | 进容器交互 shell |

## 三个服务

- `dev` —— 一次性命令执行器。**源码只读挂载**（`..:/src:ro`），产出全落 `.devbox/`，
  一次构建不会把二进制或沙箱 vault 写回仓库。
- `redis` —— 热层（`internal/hot`）。不落盘：热层按定义不是真相源。
- `board` —— 看板常驻。容器内绑 `0.0.0.0` 是容器私有网络，宿主机只发到 `127.0.0.1`。

## 产出在哪

```
runtime/.devbox/        （已 gitignore，可整个删掉重建）
├─ bin/anc              烟测编译出来的 Linux 二进制
├─ sandbox/acme/        org 真相源样本（smoke 生成）
├─ sandbox/gateway/     render 出来的 config.toml
├─ cross/               六目标产物
└─ log/smoke/           每一步的原始输出（红了先看这里）
```

## 这个环境验不出来的东西（别拿它当证据）

- **macOS 腿**：容器是 `linux/arm64`。daemon 装载（launchd / FileVault / 自动登录）一件没验，议题 #23 只关掉一半。
- **真 cc-connect 装载**：`anc apply` 要有上游 daemon，容器里没有。以 §7.1.8 / §7.1.10 的实测为准。
- **真飞书收发**：`anc notify` / 卡片回程要有真 app 与凭据。

`anc doctor` 会连 `open.feishu.cn` 等外网端点；离线环境下这一步会打 ⚠️ 但**不拦**后续步骤
（它是环境自检，不是产品判据）。

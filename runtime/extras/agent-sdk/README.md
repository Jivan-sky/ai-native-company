# ANC · Python 适配层（可选件）

把 ANC 面向动作的接口翻译成 Python agent 生态习惯的调用。**不实现业务规则。**

## 四条纪律

1. **不碰凭据** —— 令牌从 `ANC_TOKEN` 读，只在转发时出现，不落盘、不打印
2. **不实现业务规则** —— 权限、审计、幂等、写盘事务一律回 Go；这一层只转发
3. **不进默认安装** —— 基础交付是单二进制（`anc`）；这里是 `extras/` 下的可选件
4. **必须能被删掉** —— 删了它，产品功能一条不少

## 跑

```bash
pip install fastapi uvicorn httpx
ANC_BASE=http://127.0.0.1:8710 ANC_TOKEN=<可选> uvicorn app:app --port 8790
```

只暴露四条面向动作的接口：`/agent/board`、`/agent/approvals`、`/agent/timeline`、`/agent/context`。
**不把整个看板 API 透出去** —— 那些字段是为渲染组织的，agent 依赖它们等于依赖界面细节。

## 不做什么

- 不做写操作（签、结账、接入、签发授权）—— 写必须回 Go 的入口，那里才有事务与留痕
- 不做重试 —— 重试写操作等于重复执行
- 不做权限判断 —— 它拿到的令牌能做什么，由 Go 那边判

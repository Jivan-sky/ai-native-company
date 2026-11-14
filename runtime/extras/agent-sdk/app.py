"""ANC 的 Python 适配层 —— **可选件，不是核心**。

它只做一件事：把 ANC 面向动作的接口，翻译成 Python agent 生态习惯的调用。
它**不实现任何业务规则**：权限、审计、幂等、写盘事务全部回 Go 那边。

四条纪律（写在这里，也写在 README 里）：
  1 不碰凭据：令牌从环境变量读，用完即弃，不落盘、不打印
  2 不实现业务规则：只转发，不判权限、不写审计、不重试写操作
  3 不进默认安装：基础交付是单二进制；这个是 extras/ 里的可选件
  4 必须能被删掉：删了它，产品功能一条不少

跑法：
    pip install fastapi uvicorn httpx
    ANC_BASE=http://127.0.0.1:8710 ANC_TOKEN=<可选，机器令牌> uvicorn app:app --port 8790
"""

from __future__ import annotations

import os
from typing import Any

try:
    import httpx
    from fastapi import FastAPI, HTTPException
    from fastapi.responses import JSONResponse
except ModuleNotFoundError as e:  # 没装依赖时给一句人话，而不是一屏 traceback
    raise SystemExit(
        "这个适配层需要两个依赖：pip install fastapi uvicorn httpx。\n"
        "（它是可选件 —— 不装也不影响 ANC 主程序。）\n"
        f"缺的是：{e.name}"
    )

BASE = os.environ.get("ANC_BASE", "").rstrip("/")
TOKEN = os.environ.get("ANC_TOKEN", "")

app = FastAPI(title="ANC agent adapter", version="0.1.0", docs_url="/docs")

# 只暴露**面向动作**的几条，不把整个看板 API 透出去。
# 透出去等于把"给眼睛看的字段"也给了 agent，它会开始依赖渲染细节。
ROUTES = {
    "board": "/api/board",          # 这家公司长什么样
    "approvals": "/api/approvals",  # 有哪些在等人点头
    "timeline": "/api/timeline",    # 一件事的来龙去脉
    "context": "/api/context",      # 某个 agent 拿到什么上下文
}


def _headers() -> dict[str, str]:
    # 令牌只在这一次转发里出现；不写日志、不缓存。
    return {"Authorization": f"Bearer {TOKEN}"} if TOKEN else {}


async def _get(path: str, params: dict[str, Any] | None = None) -> JSONResponse:
    if not BASE:
        raise HTTPException(status_code=503, detail="没有配 ANC_BASE —— 这个适配层不知道该连谁")
    url = BASE + path
    try:
        async with httpx.AsyncClient(timeout=10.0) as cli:
            r = await cli.get(url, params=params, headers=_headers())
    except httpx.RequestError as e:
        # 连不上就如实说连不上，不假装是空数据。
        raise HTTPException(status_code=502, detail=f"连不上 ANC：{e.__class__.__name__}")
    if r.status_code >= 400:
        # 透传状态码与后端原话：错误形状是契约的一部分，不在这一层改写。
        return JSONResponse(status_code=r.status_code, content=_safe_json(r))
    return JSONResponse(content=_safe_json(r))


def _safe_json(r: httpx.Response) -> Any:
    try:
        return r.json()
    except Exception:
        return {"error": "ANC 返回的不是 JSON", "status": r.status_code}


@app.get("/healthz")
async def healthz() -> dict[str, Any]:
    return {"ok": True, "base": BASE or None, "token_set": bool(TOKEN)}


@app.get("/agent/board")
async def board() -> JSONResponse:
    return await _get(ROUTES["board"])


@app.get("/agent/approvals")
async def approvals(domain: str | None = None) -> JSONResponse:
    return await _get(ROUTES["approvals"], {"who": domain} if domain else None)


@app.get("/agent/timeline")
async def timeline(limit: int = 50) -> JSONResponse:
    # 上限自己夹一下：这一层不该让 agent 一次拉 10 万条回来。
    return await _get(ROUTES["timeline"], {"limit": max(1, min(500, limit))})


@app.get("/agent/context")
async def context(who: str, domain: str | None = None) -> JSONResponse:
    params: dict[str, Any] = {"who": who}
    if domain:
        params["domain"] = domain
    return await _get(ROUTES["context"], params)

#!/usr/bin/env python3
"""独立神谕 · token 账：只认字节，按每家**记录自己的**口径把四类 token 数出来。

三家口径（都是记录自己写的，不是我们的读取器说的）：
  dsh    每条 assistant/message 的 usage 求和
  claude 按 message.id **合并**（同一条消息会写多行，按行累加会翻倍）后求和
  codex  每个 turn 的 turn_token_usage 是累计值：按 turn 取最后一个，再把各 turn 相加
         （不用 thread_token_usage —— fork 出来的会话把前史也带进来了）

输出：{"in":..,"out":..,"cache_read":..,"cache_write":..,"files":N}
"""
import json, os, subprocess, sys


def files(root):
    if not os.path.isdir(root):
        return [root]
    out = []
    for base, _d, names in os.walk(root):
        for n in names:
            if n.endswith((".jsonl", ".zstd")):
                out.append(os.path.join(base, n))
    return sorted(out)


def lines(p):
    if p.endswith(".zstd"):
        raw = subprocess.run(["zstd", "-dc", "--", p], capture_output=True, check=True).stdout
        for ln in raw.decode("utf-8", "replace").splitlines():
            ln = ln.strip()
            if ln:
                try:
                    yield json.loads(ln)
                except Exception:
                    continue
    else:
        for ln in open(p, encoding="utf-8", errors="replace"):
            ln = ln.strip()
            if ln:
                try:
                    yield json.loads(ln)
                except Exception:
                    continue


def dsh(stream):
    tot = {"in": 0, "out": 0, "cache_read": 0, "cache_write": 0}
    for o in stream:
        if o.get("type") != "assistant/message":
            continue
        u = (o.get("data") or {}).get("usage") or {}
        tot["in"] += int(u.get("inputTokens") or 0)
        tot["out"] += int(u.get("outputTokens") or 0)
        tot["cache_read"] += int(u.get("cacheReadTokens") or 0)
        tot["cache_write"] += int(u.get("cacheWriteTokens") or 0)
    return tot


def claude(stream):
    by_id = {}
    for o in stream:
        if o.get("type") != "assistant":
            continue
        m = o.get("message") or {}
        u = m.get("usage") or {}
        if not u:
            continue
        mid = m.get("id") or o.get("uuid") or str(id(o))
        by_id[mid] = {                      # 同 id 取最后一次（流式中间态会把 output 写成 0）
            "in": int(u.get("input_tokens") or 0),
            "out": int(u.get("output_tokens") or 0),
            "cache_read": int(u.get("cache_read_input_tokens") or 0),
            "cache_write": int(u.get("cache_creation_input_tokens") or 0),
        }
    tot = {"in": 0, "out": 0, "cache_read": 0, "cache_write": 0}
    for v in by_id.values():
        for k in tot:
            tot[k] += v[k]
    return tot


def codex(stream):
    per_turn = {}
    for o in stream:
        # rollout 里两种包法都见过：顶层 type=token_usage_record，或 response_item 包一层。
        if o.get("type") == "token_usage_record":
            p = o.get("payload") or {}
        elif o.get("type") == "response_item" and (o.get("payload") or {}).get("type") == "token_usage_record":
            p = o.get("payload") or {}
        else:
            continue
        u = p.get("turn_token_usage") or {}
        if not u:
            continue
        per_turn[p.get("turn_id") or "?"] = {
            "in": int(u.get("input_tokens") or 0),
            "out": int(u.get("output_tokens") or 0),
            "cache_read": int(u.get("cached_input_tokens") or 0),
            "cache_write": int(u.get("cache_write_input_tokens") or 0),
        }
    tot = {"in": 0, "out": 0, "cache_read": 0, "cache_write": 0}
    for v in per_turn.values():
        for k in tot:
            tot[k] += v[k]
    return tot


def detect(path):
    if path.endswith(".zstd"):
        return "dsh"
    for o in lines(path):
        t = o.get("type")
        if t == "session":
            return "dsh"
        if t == "response_item":
            return "codex"
        if t in ("assistant", "user"):
            return "claude"
    return None


def main():
    root = sys.argv[1]
    fam = sys.argv[2] if len(sys.argv) > 2 else None
    tot = {"in": 0, "out": 0, "cache_read": 0, "cache_write": 0}
    n = 0
    for f in files(root):
        fam_f = fam or detect(f)
        if fam_f is None:
            continue
        part = {"dsh": dsh, "claude": claude, "codex": codex}[fam_f](lines(f))
        for k in tot:
            tot[k] += part[k]
        n += 1
    print(json.dumps({**tot, "files": n}, ensure_ascii=False))


if __name__ == "__main__":
    main()

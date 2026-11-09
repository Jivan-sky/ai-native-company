#!/usr/bin/env python3
"""不变性：同一份内容换容器（重新压缩 / 打乱行序），`anc` 的数字该不该变？

对每个变体先写**预期**，再跑，再看实际 —— 不预测就不知道「变了」是 bug 还是语义。

  recompress  zstd 换个压缩级别，解压后字节相同 → 数字必须一模一样（真不变）
  shuffle     把所有行打乱 → 只对「求和/按 id 合并」的字段不变；
              codex 的 turn_token_usage 是**每轮累计、取最后一个**，打乱后「最后一个」可能变小，
              这一格**预期会变**，变了不算 bug（正好说明读取器用的是累计口径）

用法：invariance.py <vault> <config> <harness> <记录根> <anc 二进制>
"""
import json, os, random, shutil, subprocess, sys, tempfile

VAULT, CFG, HARNESS, ROOT, BIN = sys.argv[1:6]


def totals(bin_path, root):
    out = subprocess.run([bin_path, "trail", VAULT, "--harness", HARNESS,
                          "--claude-home", root, "--config", CFG, "--json"],
                         capture_output=True, text=True)
    # 退出码 1 表示「有会话读得不全」（带 Problems），**不是** 没输出 ——
    # 拿退出码当失败会把「账读出来了但缺一格」误判成「读不到」。
    try:
        t = json.loads(out.stdout)["totals"]
    except Exception:
        return {"error": (out.stderr or out.stdout).strip()[:120]}
    return {k: t[k] for k in ("in", "out", "cache_read", "cache_write")}


def each_file(root):
    for base, _d, names in os.walk(root):
        for n in names:
            if n.endswith((".jsonl", ".zstd")):
                yield os.path.join(base, n)


def rebuild(src_root, dst_root, transform):
    """把整棵记录树复制一遍，逐文件做 transform(bytes) -> bytes。"""
    for src in each_file(src_root):
        rel = os.path.relpath(src, src_root)
        dst = os.path.join(dst_root, rel)
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        transform(src, dst)


def plain_bytes(path):
    if path.endswith(".zstd"):
        return subprocess.run(["zstd", "-dc", "--", path], capture_output=True, check=True).stdout
    return open(path, "rb").read()


def write(path, data, zstd_level=None):
    if path.endswith(".zstd"):
        p = subprocess.run(["zstd", "-q", f"-{zstd_level}", "-f", "-o", path, "-"],
                           input=data, capture_output=True)
        if p.returncode != 0:
            raise RuntimeError(p.stderr.decode()[:200])
    else:
        open(path, "wb").write(data)


def recompress(src, dst):
    write(dst, plain_bytes(src), zstd_level=3)


def shuffle(src, dst):
    lines = [l for l in plain_bytes(src).splitlines() if l.strip()]
    random.Random(7).shuffle(lines)
    write(dst, b"\n".join(lines) + b"\n", zstd_level=1)


def main():
    base = totals(BIN, ROOT)
    print("基线    ", json.dumps(base, sort_keys=True))
    fail = 0
    with tempfile.TemporaryDirectory() as tmp:
        for name, fn, expect in (
            ("recompress", recompress, "same"),
            ("shuffle", shuffle, "same" if HARNESS != "codex" else "may differ"),
        ):
            dst_root = os.path.join(tmp, name)
            rebuild(ROOT, dst_root, fn)
            got = totals(BIN, dst_root)
            if expect == "same":
                ok = got == base
                fail += 0 if ok else 1
                print(f"  {name:<10} {'✅ 数字不变' if ok else '❌ 变了：' + json.dumps(got, sort_keys=True)}")
            else:
                print(f"  {name:<10} 预期可能变（累计口径），实际 {json.dumps(got, sort_keys=True)}")
    sys.exit(1 if fail else 0)


if __name__ == "__main__":
    main()

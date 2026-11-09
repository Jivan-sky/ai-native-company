# 独立神谕：只认字节，不认 anc 的代码。数「记录里发生过多少次工具调用、各叫什么」。
import json, subprocess, sys, collections
path = sys.argv[1]
def files(p):
    import os
    if not os.path.isdir(p):
        return [p]
    out = []
    for base, _dirs, names in os.walk(p):
        for n in names:
            if n.endswith((".jsonl", ".zstd")):
                out.append(os.path.join(base, n))
    return sorted(out)

def lines(p):
    if p.endswith(".zstd"):
        out = subprocess.run(["zstd","-dc","--",p], capture_output=True, check=True).stdout
        stream = out.decode("utf-8", "replace").splitlines()
    else:
        stream = open(p, encoding="utf-8", errors="replace")
    for ln in stream:
        ln = ln.strip()
        if not ln: continue
        try: yield json.loads(ln)
        except Exception: continue
c = collections.Counter()
for f in files(path):
  for o in lines(f):
    t = o.get("type")
    if t == "tool/call":            # DSH
        c[o["data"].get("name","?")] += 1
    elif t == "response_item":      # codex rollout
        p = o.get("payload") or {}
        if p.get("type") == "function_call":
            c[p.get("name","?")] += 1
    elif t == "assistant":          # claude
        for b in ((o.get("message") or {}).get("content") or []):
            if isinstance(b, dict) and b.get("type") == "tool_use":
                c[b.get("name","?")] += 1
print(json.dumps({"total": sum(c.values()), "by_tool": dict(c)}, ensure_ascii=False, sort_keys=True))

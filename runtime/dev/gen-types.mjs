// 契约漂移检查：拿**真实 payload** 去核对前端类型，不一致就非零退出。
//
// 为什么要有它：那份 contract-first 的评审里，唯一能自动化的判据是
// "前端类型是否忠实于后端契约"。我们没装 OpenAPI，但真服务就是契约本身 ——
// 直接抓它的返回，跟 core/types.ts 里声明的字段比。
//
// 它不发货、不进二进制、不引任何运行时：只在开发期跑。
//   跑法：node dev/gen-types.mjs            （看板需在 127.0.0.1:8710）
//   或：  bash dev/dev.sh types

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const TYPES = join(HERE, "..", "boardui", "src", "core", "types.ts");
const BASE = process.env.ANC_BASE ?? "http://127.0.0.1:8710";

// 类型 → 端点。只列**契约性质**的几份；页面内部的小类型不在核对面里。
const MAP = [
  ["BoardView", "/api/board"],
  ["ApprovalView", "/api/approvals"],
  ["TimelineView", "/api/timeline"],
  ["AuditView", "/api/audit"],
  ["ContextView", "/api/context?who=alice"],
  ["GrantsView", "/api/grants"],
  ["RuntimeView", "/api/runtime"],
];

// 解析 `export type X = { a: string; b?: number }` 里的一级字段名。
// 大括号配对要稳：类型体里全是嵌套对象，数偏一层就会把字段全漏掉。
function fieldsOf(src, name) {
  const head = `export type ${name} = {`;
  const i = src.indexOf(head);
  if (i < 0) return null;
  let depth = 0, body = "";
  for (let j = i + head.length - 1; j < src.length; j++) {
    const c = src[j];
    if (c === "{") { depth++; if (depth === 1) continue; }
    if (c === "}") { depth--; if (depth === 0) break; }
    body += c;
  }
  const out = [];
  let d = 0;
  for (const line of body.split("\n")) {
    const t = line.trim();
    // 只数花括号与圆括号：数组类型的 `[]` 会在同一行里开而不闭，
    // 数它就会把后面所有字段都吞掉（第一版就栽在这）。
    const opens = (line.match(/[{(]/g) ?? []).length;
    const closes = (line.match(/[})]/g) ?? []).length;
    if (d === 0) {
      // 一行里可能塞了好几个字段（`schema: string; wired: boolean;`）—— 逐个取。
      // 行内嵌套对象（`scopes: { domain: … }`）的花括号自成一体，
      // 按 ";" 切会把里面的字段当成一级字段 —— 先在第一个 "{" 处截断。
      const flat = t.includes("{") ? t.slice(0, t.indexOf("{") + 1) : t;
      for (const seg of flat.split(";")) {
        const q = seg.trim();
        if (/^[A-Za-z_][\w]*\??\s*:/.test(q)) {
          const nm = q.split(":")[0].trim();
          // 记住它是不是可选：`x?:` 的字段在真实返回里缺席是正常的（服务端 omitempty）。
          out.push({ name: nm.replace(/\?$/, ""), optional: nm.endsWith("?") });
        }
      }
    }
    d += opens - closes;
  }
  return out;
}

const src = readFileSync(TYPES, "utf8");
const problems = [];

for (const [type, path] of MAP) {
  const declared = fieldsOf(src, type);
  if (!declared) { problems.push(`types.ts 里找不到 ${type}`); continue; }
  let body;
  try {
    const r = await fetch(BASE + path);
    body = await r.json();
  } catch {
    problems.push(`${path} 取不到（看板在跑吗？）`);
    continue;
  }
  const keys = new Set(Object.keys(body ?? {}));
  // 前端声明了、契约里没有 → 编造字段（最严重）
  for (const f of declared) {
    if (!keys.has(f.name) && !f.optional) {
      problems.push(`${type}.${f.name} 在 ${path} 的真实返回里不存在 —— 编造字段（要么删掉它，要么标成可选）`);
    }
  }
  // 契约里有、前端没声明 → 不是错，但要报：可能是漏了一个能力
  const decl = new Set(declared.map((f) => f.name));
  const missing = [...keys].filter((k) => !decl.has(k));
  if (missing.length) console.log(`· ${type}: 契约有而这层没用到的字段 → ${missing.join(", ")}`);
}

if (problems.length) {
  console.log(`❌ 契约漂移 ${problems.length} 条：`);
  for (const p of problems) console.log(" - " + p);
  process.exit(1);
}
console.log(`✅ ${MAP.length} 份类型与真实 payload 对得上`);

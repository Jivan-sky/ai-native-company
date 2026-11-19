// 契约快照检查（治理判据 9）：**破坏性变更必须让它失败**。
//
// 之前 `gen-types.mjs` 只拦"前端编字段"——删字段、改名同改、标可选三种全绿。
// 那不是契约门禁，那是"前端别乱写"检查。真正的契约问题在**生产方**：
// 他改了一个字段的类型或删了一个字段，消费方在客户现场才发现。
//
// 做法：把真实 payload 的**形状**（键 + 类型，两层深）存成快照，提交进仓库。
//   新增字段  → 不破坏（提示，需要人确认后 --update）
//   删字段    → BREAKING
//   改类型    → BREAKING
//
// 限制（写清楚，别当它万能）：看不见"可选性"与"取值范围"，也看不见语义。

import { readFileSync, writeFileSync, existsSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = join(HERE, "..");
const SNAP_DIR = join(ROOT, "contracts");
const BASE = process.env.ANC_BASE ?? "http://127.0.0.1:8710";
const UPDATE = process.argv.includes("--update");

const ENDPOINTS = [
  ["board", "/api/board"],
  ["approvals", "/api/approvals"],
  ["timeline", "/api/timeline?limit=1"],
  ["audit", "/api/audit?limit=1"],
  ["context", "/api/context?who=alice"],
  ["grants", "/api/grants"],
  ["runtime", "/api/runtime"],
];

function typeOf(v) {
  if (v === null) return "null";
  if (Array.isArray(v)) return v.length === 0 ? "array<unknown>" : "array<" + typeOf(v[0]) + ">";
  if (typeof v === "object") return "object";
  return typeof v; // string | number | boolean
}

// 有些键名是**动态的**（例如 task_bands 以提案 id 为键：`prop:p-gov-1`）。
// 把它们原样写进快照，队列一变就会误报"字段被删" —— 这不是契约变更，是数据变了。
// 归一成 <key>，只保留"这里是一个映射"这个事实。
function dynamicKey(k) {
  return k.includes(":") || /^[a-z0-9]+(-[a-z0-9]+)+$/.test(k);
}

// shape 只取两层：顶层对象的键与类型；数组取第一元素的键与类型。
function shape(v) {
  if (v === null || typeof v !== "object") return typeOf(v);
  if (Array.isArray(v)) {
    return v.length === 0 ? "array<unknown>" : shape(v[0]);
  }
  const out = {};
  for (const k of Object.keys(v).sort()) {
    const t = typeOf(v[k]);
    if (t === "object") {
      const inner = {};
      for (const k2 of Object.keys(v[k]).sort()) inner[dynamicKey(k2) ? "<key>" : k2] = typeOf(v[k][k2]);
      out[k] = inner;
    } else {
      out[k] = t;
    }
  }
  return out;
}

function compare(oldS, newS, path = "") {
  const breaking = [];
  const added = [];
  if (typeof oldS === "string" || typeof newS === "string") {
    if (oldS !== newS) breaking.push(`${path || "(根)"}：类型 ${oldS} → ${newS}`);
    return { breaking, added };
  }
  for (const k of Object.keys(oldS)) {
    const p = path ? `${path}.${k}` : k;
    if (!(k in newS)) { breaking.push(`${p}：字段被删`); continue; }
    const r = compare(oldS[k], newS[k], p);
    breaking.push(...r.breaking);
    added.push(...r.added);
  }
  for (const k of Object.keys(newS)) {
    if (!(k in oldS)) added.push(path ? `${path}.${k}` : k);
  }
  return { breaking, added };
}

let failed = 0;
for (const [name, path] of ENDPOINTS) {
  let body;
  try {
    const r = await fetch(BASE + path);
    body = await r.json();
  } catch {
    console.log(`⚠️  ${name}: 取不到（看板在跑吗？）—— 跳过`);
    continue;
  }
  const live = shape(body);
  const file = join(SNAP_DIR, name + ".json");
  if (UPDATE || !existsSync(file)) {
    mkdirSync(SNAP_DIR, { recursive: true });
    writeFileSync(file, JSON.stringify(live, null, 2) + "\n");
    console.log(`· ${name}: 快照已写入 contracts/${name}.json`);
    continue;
  }
  const old = JSON.parse(readFileSync(file, "utf8"));
  const { breaking, added } = compare(old, live);
  if (breaking.length) {
    console.log(`❌ ${name}: ${breaking.length} 处破坏性变更`);
    for (const b of breaking) console.log("     " + b);
    failed++;
  } else if (added.length) {
    console.log(`· ${name}: 新增字段（不破坏）→ ${added.join(", ")} ｜确认后跑 --update`);
  }
}

if (UPDATE) {
  console.log("✅ 快照已更新 —— 请把 contracts/ 的改动一起提交，并在 commit 里说明为什么改契约");
  process.exit(0);
}
if (failed) {
  console.log(`\n❌ ${failed} 个端点出现破坏性变更。要么改回，要么起一个新的 schema 版本并同步消费方。`);
  process.exit(1);
}
console.log(`✅ ${ENDPOINTS.length} 个端点的契约快照无破坏性变更`);

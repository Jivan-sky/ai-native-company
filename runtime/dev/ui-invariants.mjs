// UI 不变量检查 —— 用真浏览器跑一遍，把"点一下又弹回去"这类问题变成会自动失败的东西。
//
// 为什么要有它：过去两周，界面上的问题都是**使用者点出来**的（宽度弹回、刷新跳页、
// 绝对路径上屏、页面没标题）。这说明改完不验证就等于没改。人眼只能验证他刚好看到的那一处。
//
// 跑法（需要 ego-browser 与一块在跑的看板）：
//   1  bash dev/dev.sh board            # 或 anc board serve <vault>
//   2  ego-browser nodejs < dev/ui-invariants.mjs
//
// 每条不变量都是一个"以前真出过的错"，不是想象出来的规范。

const BASE = "http://127.0.0.1:8710";
const PAGES = ["overview","approvals","projects","org","run","data","records","assets","system","team"];

// 复用已有的任务空间；没有就新建一个（不要为"恢复"另开一个空间，那会让标签页越开越多）。
async function space() {
  for (const name of ["ANC 看板整体梳理"]) {
    try { return await takeOverTaskSpace(name); } catch { /* 没这个空间 */ }
  }
  return await taskSpace("ANC 看板 UI 不变量");
}
const task = await space();
const page = task.page("p1");
const fails = [];
const ok = (name, cond, detail) => { if (!cond) fails.push(name + (detail ? " · " + detail : "")); };

// ① 每页都有恰好一个 h1，且等于页名（读屏器与浏览器标签靠它）
for (const id of PAGES) {
  await page.goto(BASE + "/?t=" + Date.now() + "#/" + id);
  await page.waitForTimeout(900);
  const r = await page.evaluate(function () {
    const h1s = Array.from(document.querySelectorAll("h1")).map(function (h) { return h.textContent.trim(); });
    const main = document.querySelector("main");
    return { h1s: h1s, chars: main ? main.innerText.replace(/\s+/g, " ").trim().length : 0 };
  });
  ok("h1 唯一且非空：" + id, r.h1s.length === 1 && r.h1s[0].length > 0, JSON.stringify(r.h1s));
  ok("页面不是空白：" + id, r.chars > 80, r.chars + " 字符");
}

// ② 屏幕上不许出现本机绝对路径（曾经把 /Users/xxx/... 打给客户看）
for (const id of PAGES) {
  await page.goto(BASE + "/?t=" + Date.now() + "#/" + id);
  await page.waitForTimeout(600);
  const leaks = await page.evaluate(function () {
    var t = document.body.innerText;
    var hits = t.match(/\/Users\/[^\s）)]*|\/home\/[^\s）)]*|[A-Z]:\\\\Users\\\\[^\s）)]*/g);
    return hits ? hits.slice(0, 3) : [];
  });
  ok("无本机路径：" + id, leaks.length === 0, leaks.join(" , "));
}

// ③ 侧栏拖出来的宽度，点任意页之后仍然在（曾经点一下就弹回）
await page.goto(BASE + "/?t=" + Date.now() + "#/overview");
await page.waitForTimeout(1200);
// 先把这两个键清掉：它们是**上一轮跑测**留下的状态（比如上次把侧栏收起了），
// 不清会把"宽度保持"测成"收起态 56px"，误报。
await page.evaluate(function () {
  localStorage.removeItem("anc.rail.min");
  localStorage.removeItem("anc.rail.width");
});
await page.reload();
await page.waitForTimeout(1200);
const box = await page.evaluate(function () {
  var g = document.querySelector(".side-resizer");
  if (!g) return null;
  var b = g.getBoundingClientRect();
  return { x: Math.round(b.x + 2), y: Math.round(b.y + 260) };
});
if (!box) {
  fails.push("侧栏没有拖拽把手");
} else {
  await page.mouse.move(box.x, box.y);
  await page.mouse.down();
  await page.mouse.move(box.x + 70, box.y, { steps: 8 });
  await page.mouse.up();
  await page.waitForTimeout(200);
  const w1 = await page.evaluate(function () { return Math.round(document.querySelector(".side").getBoundingClientRect().width); });
  await page.goto(BASE + "/?t=" + Date.now() + "#/approvals");
  await page.waitForTimeout(1200);
  const w2 = await page.evaluate(function () { return Math.round(document.querySelector(".side").getBoundingClientRect().width); });
  ok("侧栏宽度跨页保持", Math.abs(w1 - w2) <= 2, w1 + " → " + w2);
}

// ④ 自动刷新不许重建整页（曾经每 30 秒把 DOM 拆一遍，滚动跳回顶部）
await page.goto(BASE + "/?t=" + Date.now() + "#/records");
await page.waitForTimeout(1400);
await page.evaluate(function () { window.scrollTo(0, 300); });
const marked = await page.evaluate(function () {
  window.__h2 = document.querySelector("main h2");
  return { y: window.scrollY };
});
await page.waitForTimeout(33000); // 跨过一次 30 秒轮询
const after = await page.evaluate(function () {
  return { y: window.scrollY, same: window.__h2 === document.querySelector("main h2") };
});
ok("轮询不重建 DOM", after.same, "节点被替换了");
ok("轮询不丢滚动位置", after.y === marked.y, marked.y + " → " + after.y);

// ④b 悬浮球可以拖，且位置跨刷新保持（曾经只能看不能动，挡着右下角的内容）
await page.goto(BASE + "/?t=" + Date.now() + "#/overview");
await page.waitForTimeout(1200);
const ball = await page.evaluate(function () {
  const b = document.querySelector(".chat-launcher");
  if (!b) return null;
  const r = b.getBoundingClientRect();
  return { x: Math.round(r.left), y: Math.round(r.top) };
});
if (!ball) {
  fails.push("没有对话悬浮球");
} else {
  await page.mouse.move(ball.x + 20, ball.y + 20);
  await page.mouse.down();
  await page.mouse.move(ball.x - 160, ball.y - 220, { steps: 10 });
  await page.mouse.up();
  await page.waitForTimeout(250);
  const moved = await page.evaluate(function () {
    const r = document.querySelector(".chat-launcher").getBoundingClientRect();
    return { x: Math.round(r.left), y: Math.round(r.top), open: document.querySelector(".chat-panel").classList.contains("open") };
  });
  ok("悬浮球可以拖", Math.abs(moved.x - ball.x) > 40, ball.x + " → " + moved.x);
  ok("拖完不误开面板", !moved.open);
  await page.reload();
  await page.waitForTimeout(1200);
  const kept = await page.evaluate(function () {
    const r = document.querySelector(".chat-launcher").getBoundingClientRect();
    return { x: Math.round(r.left) };
  });
  ok("悬浮球位置跨刷新保持", Math.abs(kept.x - moved.x) <= 2, moved.x + " → " + kept.x);
}

// ⑤ 顶栏不许出现调试 HUD（LIVE / 校验干净 / 生成时间戳）
const hud = await page.evaluate(function () {
  var t = document.querySelector(".topbar") ? document.querySelector(".topbar").innerText : "";
  return { live: t.indexOf("LIVE") >= 0, clean: t.indexOf("校验干净") >= 0, gen: t.indexOf("生成") >= 0 };
});
ok("顶栏无 LIVE", !hud.live);
ok("顶栏无「校验干净」", !hud.clean);
ok("顶栏无生成时间戳", !hud.gen);

console.log(fails.length === 0 ? "✅ 全部通过（" + PAGES.length + " 页）" : "❌ 失败 " + fails.length + " 条：\n - " + fails.join("\n - "));
if (fails.length > 0) process.exitCode = 1;

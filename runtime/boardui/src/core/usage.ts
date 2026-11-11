// 看板自己的使用记录：只落这台浏览器的 localStorage。
//
// **不写进客户的 audit 流**。客户的审计是他的账本（谁对谁行使了什么），
// 混进「有人在看第 3 页」是把产品分析塞进别人的合规记录里 —— 那是不该做的事。
//
// 记这三样就够回答「哪一屏有人用」：
//   page  打开了哪一页
//   deny  哪一页被身份挡住（说明有人想进）
//   metric 哪个数字被点开看过来源（阶段 2 才有，见 PLAN §1 押注）
export type UsageEvent = { at: string; kind: "page" | "deny" | "metric"; what: string };

const KEY = "anc.usage";
const CAP = 300;

export function usage(): UsageEvent[] {
  try {
    const raw = localStorage.getItem(KEY) ?? "[]";
    const arr = JSON.parse(raw) as UsageEvent[];
    return Array.isArray(arr) ? arr : [];
  } catch {
    return [];
  }
}

export function track(kind: UsageEvent["kind"], what: string): void {
  const all = usage();
  // 同一次打开会被渲染两趟（先骨架后数据）—— 两秒内的同一条事件算一次，
  // 否则「哪一页被打开」这个数会稳定翻倍。
  const last = all[all.length - 1];
  if (last && last.kind === kind && last.what === what && Date.now() - Date.parse(last.at) < 2000) return;
  all.push({ at: new Date().toISOString(), kind, what });
  // 只留最近 300 条：这是「最近谁在用」，不是归档。
  try { localStorage.setItem(KEY, JSON.stringify(all.slice(-CAP))); } catch { /* 隐私模式不记 */ }
}

// 给控制台留一个手口：想看清楚这一台机器上到底发生了什么，不用开后端。
(window as unknown as { ancUsage?: () => UsageEvent[] }).ancUsage = usage;

import type { ApprovalPending, ApprovalSignal, ApprovalView, Data, DecideReply, Kid } from "../core/types";
import { postJSON } from "../core/api";
import { banner, chip, dash, el, section, table } from "../core/dom";
import { localTime, shortTime, sinceText } from "../core/format";
import { reload, rerender } from "../core/refresh";
import { cannot } from "../ui/blocks";
import { emptyState, unknownState } from "../ui/states";
import { metricButton } from "../core/metrics";
import { byWho, setByWho } from "../ui/bywho";
import { bandDot, bandWord, dotLine } from "../ui/status";

// 刚点完头的回执。只放内存：它讲的是「刚才那一下」，不是存档（存档在 timeline / audit）。
export let lastReceipt: DecideReply | null = null;
export let lastDecideErr = "";

export function receiptBanner(r: DecideReply): HTMLElement {
  return el("div", { class: "banner ok" },
    el("h3", {}, "刚刚点头：" + dash(r.verdict) + "（" + (r.by === "" ? "没记谁" : r.by) + "）"),
    el("ul", {},
      el("li", {}, el("span", { class: "mono" }, dash(r.proposal)), " —— ", dash(r.note)),
      el("li", {}, "落库 ", el("span", { class: "mono" }, dash(r.timeline))),
      el("li", {}, "审计 ", el("span", { class: "mono" }, dash(r.audit)))));
}

// 「谁在点」—— 看板没有免登，所以这一栏由页面出，并记在本机（下次不用再打）。
export function bySection(): Kid {
  const input = el("input", {
    class: "by", type: "text",
    placeholder: "你的名字 / 岗位（会原样写进留痕）",
    value: byWho,
  });
  input.addEventListener("input", () => {
    setByWho((input as HTMLInputElement).value);
  });
  return section("谁在点", "", el("div", { class: "field" }, input));
}

// 一条提案一张卡：状态在上，人话在中，按钮在下。
//
// 卡里不放「当前态 pending」—— 这一整列都是 pending，写一行等于没写；
// 也不放「靛 · 」这种颜色词 —— 形状就在旁边，颜色从不单独承载信息。
const dotSep = (): HTMLElement => el("span", { class: "dotsep" }, "·");

export function pendingCard(v: ApprovalView, p: ApprovalPending): HTMLElement {
  const title = (p.title ?? "").trim();
  const subject = [p.domain, p.project].map((x) => (x ?? "").trim()).filter((x) => x !== "").join(" / ");
  // 判真那一档：**在队列里 = 有人在等你给一句话**（暂停形 + 靛蓝）。
  // 档由服务端给（band），前端照画。颜色不单独承载信息：旁边永远有词。
  const band = (p.band ?? "").trim();
  const head = el("div", { class: "phead" },
    band === "" ? el("span", {}) : dotLine(bandDot(band), el("span", {}, bandWord(band).split(" · ").pop() ?? "")),
    el("span", { class: "pid mono" }, dash(p.id)));
  const to = (p.to ?? "").trim();
  // 分隔符贴在后一项**前面**、与它同属一个 flex 项：折行时「·」落在行首，
  // 不会孤零零挂在上一行末尾（挂在那里看着像少了一个词）。
  const facts: Kid[] = [
    el("span", {}, "该谁批 ", to === "" ? cannot("域表没写，不知道该谁批") : el("b", {}, to)),
    el("span", {}, dash(p.who), " 提"),
  ];
  if (subject !== "") facts.push(el("span", {}, subject));
  facts.push(el("span", { title: localTime(p.enqueued) }, sinceText(p.enqueued)));
  const meta = el("div", { class: "pmeta" },
    ...facts.map((f, i) => (i === 0 ? f : el("span", { class: "pair" }, dotSep(), f))));
  return el("div", { class: "pcard" },
    head,
    el("h3", { class: "ptitle" }, title === "" ? "（提案没写标题）" : title),
    meta,
    el("p", { class: "pbody" }, (p.body ?? "").trim() === "" ? cannot("提案没有正文") : p.body),
    actsRow(v, p));
}

// 三个按钮来自服务端给的 signals —— 前端不写死信号词，加一个词只改服务端。
export function actsRow(v: ApprovalView, p: ApprovalPending): HTMLElement {
  const why = el("input", { class: "why", type: "text", placeholder: "为什么这么批（可空；驳回最好写一句）" });
  const row = el("div", { class: "acts" });
  for (const sig of v.signals) {
    const b = el("button", { class: "act " + sig.value, type: "button" },
      sig.label === "" ? sig.value : sig.label);
    b.addEventListener("click", () => {
      void decide(v, p, sig, why as HTMLInputElement, row);
    });
    row.append(b);
  }
  if (v.signals.length === 0) row.append(cannot("服务没给可点的信号 —— 这一版点不了"));
  return el("div", {}, why, row);
}

// 点一次头：发一次信号，成功就整页重取（队列真的变了）。
export async function decide(
  v: ApprovalView, p: ApprovalPending, sig: ApprovalSignal,
  why: HTMLInputElement, row: HTMLElement,
): Promise<void> {
  lastDecideErr = "";
  const who = byWho.trim();
  if (who === "") {
    // 后端也这么拒（「匿名点头等于没点头」）—— 这里先说，省一次往返。
    lastDecideErr = "先在上面填「谁在点」：匿名点头等于没点头（后端也这么拒）。";
    rerender();
    return;
  }
  if (v.write_path === "") {
    lastDecideErr = "服务没给写口地址（write_path 是空的）—— 刷新看看，或看服务端日志。";
    rerender();
    return;
  }
  // 点下去就先按住按钮：连着点两下 = 第二次必然「不在队列里」（那条已经结过账了）。
  for (const b of Array.from(row.querySelectorAll("button"))) b.setAttribute("disabled", "disabled");
  const res = await postJSON<DecideReply & { error?: string; hint?: string }>(v.write_path, {
    id: p.id, by: who, signal: sig.value, why: why.value,
  });
  if (res.status === 200 && res.body && res.body.ok) {
    lastReceipt = res.body;
    reload(true);
    return;
  }
  const msg = res.body?.error ?? res.err;
  const hint = res.body?.hint ?? "";
  lastDecideErr = (msg === "" ? "HTTP " + res.status : msg) + (hint === "" ? "" : "（" + hint + "）");
  rerender();
}

// 成员：按「该谁批」分组 —— 一个人打开这一页，要能一眼看到「我这摊有几件」。
// `to` 是岗位不是人名（人由展示层现算，见后端 approvals.Approver），所以标题写岗位。
export function pendingSection(v: ApprovalView): Kid {
  if (v.pending.length === 0) {
    return section("成员", "", emptyState("没有等点头的提案。", "有提案进来会自己出现在这里。"));
  }
  const groups = new Map<string, ApprovalPending[]>();
  for (const p of v.pending) {
    const to = (p.to ?? "").trim();
    groups.set(to === "" ? "域表没写该谁批" : to, [...(groups.get(to) ?? []), p]);
  }
  const blocks = [...groups.entries()].map(([who, list]) =>
    el("div", { class: "qgroup" },
      el("h3", {}, el("span", {}, who),
        el("span", { class: "hint" }, (who === "域表没写该谁批" ? "" : "岗位 · ") + list.length + " 件")),
      el("div", { class: "grid props" }, ...list.map((p) => pendingCard(v, p)))));
  return section("成员", "共 " + v.count + " 件 · 按该谁批分组", ...blocks);
}

// Agent：谁在跑、正在做什么。这块回答的是「Agent 那半边」——
// 它们不需要人点头，但需要人知道它们在干什么（以及有没有停在那儿）。
export function agentSection(d: Data): Kid {
  const a = d.agents;
  if (!a) {
    return section("Agent", "", unknownState("读不到 Agent 运行态。",
      "服务没回 anc.agents/v1 —— 这一块的空不是「没有 agent」。"));
  }
  if (!a.wired) {
    return section("Agent", "", unknownState("Agent 运行态还没接上。", dash(a.why ?? "")));
  }
  if (a.entries.length === 0) {
    return section("Agent", "", emptyState("还没有登记在跑的 agent。",
      "agents.md 里登记之后才会出现在这里。"));
  }
  const rows: Kid[][] = a.entries.map((g) => [
    dotLine(g.online ? "dot ok" : g.stale ? "dot warn" : "dot gray", el("span", {}, g.name)),
    el("span", { class: "mono nw" }, dash(g.domain_name || g.domain || "")),
    (g.task_title ?? "") === "" ? cannot("没在做什么") : el("span", {}, g.task_title ?? ""),
    (g.note ?? "") === "" ? "—" : el("span", { class: "quiet" }, g.note ?? ""),
    (g.last_seen ?? "") === ""
      ? cannot("从没心跳")
      : el("span", { class: "mono nw", title: localTime(g.last_seen ?? "") }, shortTime(g.last_seen ?? "")),
  ]);
  return section("Agent", a.online + " / " + a.total + " 在线",
    table(["Agent", "业务域", "正在做什么", "备注", "心跳"], rows));
}

export function renderApprovals(d: Data): Kid[] {
  const v = d.approvals;
  if (!v) {
    return [banner("bad", "读不到待批队列", [
      el("span", {}, d.approvalsErr === "" ? "服务没回 anc.approvals/v1" : d.approvalsErr),
    ])];
  }
  const out: Kid[] = [];
  if (lastReceipt) out.push(receiptBanner(lastReceipt));
  if (lastDecideErr !== "") out.push(banner("bad", "上一次点头没成功", [el("span", {}, lastDecideErr)]));
  if ((v.error ?? "") !== "") out.push(banner("bad", "热层读不动", [el("span", {}, dash(v.error))]));
  if (!v.wired) {
    out.push(banner("warn", "待批队列还没接上", [
      el("span", {}, dash(v.why)),
      el("br"),

    ]));
  }
  out.push(bySection());
  if (v.wired) out.push(pendingSection(v));
  out.push(agentSection(d));
  return out;
}

// 时间线 —— 读者：跟进的人 / 老板。数据源是人和 agent 用 `anc timeline add` 写下的留存记录。
//
// 这一页的纪律（与后端 timeline 包同一条口径）：
//   - 这些行是**写下来的**，不是看板从会话里推出来的 —— 没记就是没记，不替谁总结、更不编一条；
//   - 三档只按 status 配色，认不出的词**原样保留**（画灰），以后加词只改数据、不改这里；

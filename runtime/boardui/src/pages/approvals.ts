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
  // 第一行是**人要的那一句**：批了什么、谁批的、接下来交给谁。
  // 落库路径与文件名收进第二层 —— 工程师要看，使用者不用第一眼就看到。
  const next = (r.to ?? "").trim();
  return el("div", { class: "banner ok" },
    el("h3", {}, "已" + dash(r.verdict) + "（" + (r.by === "" ? "没记谁" : r.by) + "）"),
    el("p", { class: "next" },
      next === ""
        ? "这一件没有「下一手」—— 它是终态，或者提的那方没写该交给谁。"
        : "下一步：交给 " + next + "，等他回执。"),
    el("details", {}, el("summary", {}, "留痕在哪"),
      el("ul", {},
        el("li", {}, el("span", { class: "mono" }, dash(r.proposal)), " —— ", dash(r.note)),
        el("li", {}, "落库 ", el("span", { class: "mono" }, dash(r.timeline))),
        el("li", {}, "审计 ", el("span", { class: "mono" }, dash(r.audit))))));
}

// 「谁在点」—— 看板没有免登，所以这一栏由页面出，并记在本机（下次不用再打）。
// 「谁在点」会写成留痕的文件名（timeline/<月>.<谁>.jsonl），所以只允许字母数字与 ._- 空格。
// 后端本来就拒，但**拒在点完之后**：用户填了中文名、点了同意，才被告知写不成 ——
// 那一刻他填的「为什么」已经跟着重画丢了。所以在这里先说，边打边说。
const BY_OK = /^[A-Za-z0-9._\- ]+$/;

export function bySection(d: Data | null): Kid {
  // 身份不再靠手打英文 slug。
  //
  // 以前这一栏是一个自由输入框，提示写着「英文名或成员 slug，例如 zhangwei（会写进文件名）」——
  // 那是在要求使用者**自己把名字编码成文件名**，而这恰恰是系统该做的事。
  // 现在从组织事实里列人，选一下就完事；手填只作为兜底（组织里还没登记你）。
  const members = (d?.board?.members ?? []).filter((m) => !m.disabled);
  const sel = el("select", { class: "by-select" },
    ...members.map((m) => {
      const opt = el("option", { value: m.name },
        (m.display_name && m.display_name !== m.name ? m.display_name + "（" + m.name + "）" : m.name) +
        " · " + (m.role || "无岗位"));
      if (m.name === byWho) opt.setAttribute("selected", "selected");
      return opt;
    }),
    el("option", { value: "__manual__" }, "其它（手动输入）"));
  const input = el("input", {
    class: "by", type: "text",
    placeholder: "英文名或成员 slug，例如 zhangwei",
    value: BY_OK.test(byWho) ? byWho : "",
    "aria-describedby": "by-rule",
  });
  const rule = el("p", { class: "quiet", id: "by-rule" }, "");
  const manual = (): boolean => (sel as HTMLSelectElement).value === "__manual__";

  const check = (): void => {
    const v = manual() ? (input as HTMLInputElement).value.trim() : (sel as HTMLSelectElement).value;
    rule.classList.toggle("bad", v !== "" && !BY_OK.test(v));
    rule.textContent = v === ""
      ? "先选一个人 —— 匿名点头等于没点头（后端也这么拒）。"
      : BY_OK.test(v)
        ? "留痕会写到「" + v + "」名下。"
        : "这串字符写不成文件名：留痕按名字分文件，只收字母、数字、. _ - 和空格。";
    setByWho(v);
  };

  input.addEventListener("input", check);
  sel.addEventListener("change", () => {
    if (!manual()) (input as HTMLInputElement).value = "";
    check();
  });
  if (members.length === 0) sel.setAttribute("disabled", "disabled");

  // 默认值：上次选过就用上次的；否则用第一个成员（总比空着强，且看得见是谁）。
  const first = members[0];
  if (byWho === "" && first) {
    (sel as HTMLSelectElement).value = first.name;
    setByWho(first.name);
  }
  check();

  return section("谁来点", members.length === 0 ? "组织里还没有登记成员 —— 只能手填" : "从成员里选，不用记 slug",
    el("div", { class: "field" }, sel, manual() ? input : el("span", {}), rule));
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
  // 同一个规则在前端先过一遍：省一次往返，也不让填好的「为什么」白丢。
  if (!BY_OK.test(who)) {
    lastDecideErr = "「" + who + "」写不成文件名 —— 留痕按名字分文件，只收字母、数字、. _ - 和空格。换一个再点。";
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
    // 签完**原地变回执**，不刷新整页。
    //
    // 为什么：签是一个"我做过这件事"的证据。整页重绘会把它冲掉，人只看到数字少了一个，
    // 却看不到自己刚签的字。台账的规矩是"签过的地方留章"，不是"翻到下一页就没了"。
    // 队列计数由 30 秒静默刷新去对齐，不靠这一下重绘。
    const slip = row.closest(".ledger-slip");
    const lr = slip?.previousElementSibling as HTMLElement | null;
    if (lr) {
      const v = (res.body.verdict ?? "").trim();
      const at = new Date().toLocaleTimeString("zh-CN", { hour12: false });
      lr.className = "ledger-row signed";
      lr.textContent = "";
      lr.append(
        el("div", { class: "who" }, "已签"),
        el("div", { class: "what" }, (who || "你") + " · " + (v === "" ? "已表态" : v)),
        el("div", { class: "when" }, at));
      lr.removeAttribute("role");
      lr.removeAttribute("tabindex");
    }
    slip?.remove();
    return;
  }
  const msg = res.body?.error ?? res.err;
  const hint = res.body?.hint ?? "";
  lastDecideErr = (msg === "" ? "HTTP " + res.status : msg) + (hint === "" ? "" : "（" + hint + "）");
  rerender();
}

// 成员：先回答「有没有一件是要我点的」，再按岗位铺开。
//
// 以前只按 `to` 分组，读者得自己在几组里找"哪组是我"。现在先算一遍：
// 当前选中的这个人，他的岗位是哪个；`to` 命中该岗位或命中本人的，归到「该你批」。
//
// `to` 写的是岗位不是人名（人由后端 approvals.Approver 现算），所以先拿岗位比，
// 再拿名字比 —— 两种写法都认，与后端同一套语义。
export function pendingSection(v: ApprovalView, d: Data | null): Kid {
  if (v.pending.length === 0) {
    return section("成员", "", emptyState("没有等点头的提案。", "有提案进来会自己出现在这里。"));
  }
  const me = byWho.trim();
  const myRole = (d?.board?.members ?? []).find((m) => m.name === me)?.role ?? "";
  const mine: ApprovalPending[] = [];
  const others: ApprovalPending[] = [];
  for (const p of v.pending) {
    const to = (p.to ?? "").trim();
    const isMine = to !== "" && ((myRole !== "" && to === myRole) || to === me || to === "member:" + me);
    (isMine ? mine : others).push(p);
  }
  const groupOf = (list: ApprovalPending[]): HTMLElement[] => {
    const groups = new Map<string, ApprovalPending[]>();
    for (const p of list) {
      const to = (p.to ?? "").trim();
      groups.set(to === "" ? "域表没写该谁批" : to, [...(groups.get(to) ?? []), p]);
    }
    return [...groups.entries()].map(([who, sub]) =>
      el("div", { class: "qgroup" },
        el("h3", {}, el("span", {}, who),
          el("span", { class: "hint" }, (who === "域表没写该谁批" ? "" : "岗位 · ") + sub.length + " 件")),
        el("div", { class: "grid props" }, ...sub.map((p) => pendingCard(v, p)))));
  };

  // 台账行：字段名在左、事在右、时间在最后。一行读完就知道要不要点 —— 不用先点开。
  // 这是交互设计里的主循环第一步（翻），也是"不用卡片"那条规矩的落地。
  const ledgerRow = (p: ApprovalPending, sign: boolean): HTMLElement => {
    const who = (p.who ?? "").trim() || "没署名";
    const title = (p.title ?? "").trim() || "（没写标题）";
    const at = (p.enqueued ?? "").replace("T", " ").slice(5, 16);
    const rowEl = el("div", { class: "ledger-row" + (sign ? " sign" : ""), role: "button", tabindex: "0" },
      el("div", { class: "who" }, sign ? "待你签" : who),
      el("div", { class: "what" }, title),
      el("div", { class: "when" }, at));
    // 点行 = 展开那张单（附页式）。再点收起。键盘 Enter / Space 同效。
    const toggle = (): void => {
      const slip = rowEl.parentElement?.querySelector(".ledger-slip");
      if (slip) slip.classList.toggle("open");
    };
    rowEl.addEventListener("click", toggle);
    rowEl.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); toggle(); }
    });
    return rowEl;
  };

  const blocks: Kid[] = [];
  if (mine.length > 0) {
    blocks.push(el("div", { class: "mine" },
      el("h3", { class: "mine-h" }, el("span", {}, "待你签"), el("span", { class: "hint" }, mine.length + " 件")),
      el("div", { class: "ledger" },
        ...mine.map((p) => el("div", {},
          ledgerRow(p, true),
          // 展开的那张"单"：正文 + 签字动作。点行才显示，行本身保持一行高度。
          el("div", { class: "ledger-slip" }, pendingCard(v, p)))))));
  }
  if (others.length > 0) {
    blocks.push(el("div", { class: "others" },
      el("h3", { class: "others-h" },
        el("span", {}, mine.length > 0 ? "其他人在等" : "在等点头"),
        el("span", { class: "hint" }, others.length + " 件 · 不是你的岗位，看着就好")),
      ...groupOf(others)));
  }
  return section("成员", "共 " + v.count + " 件 · " + (mine.length > 0 ? "有 " + mine.length + " 件该你点" : "没有该你点的"),
    ...blocks);
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
  out.push(bySection(d));
  if (v.wired) out.push(pendingSection(v, d));
  out.push(agentSection(d));
  return out;
}

// 时间线 —— 读者：跟进的人 / 老板。数据源是人和 agent 用 `anc timeline add` 写下的留存记录。
//
// 这一页的纪律（与后端 timeline 包同一条口径）：
//   - 这些行是**写下来的**，不是看板从会话里推出来的 —— 没记就是没记，不替谁总结、更不编一条；
//   - 三档只按 status 配色，认不出的词**原样保留**（画灰），以后加词只改数据、不改这里；

import type { Data, Kid } from "../core/types";
import { el } from "../core/dom";

// 第一屏 = 一份 10 秒简报。
//
// 十秒三问：现在有没有事 / 谁在等 / 我下一步做什么。
// 所以这一屏只有：一句结论 + 三个数 + 三栏。细节一律去各自的页，不在这里堆表。

// 运行态的四档词是 ok / warn / fail / unwired —— 不是 red。
// 这里必须与左栏、状态图例用**同一个词**，否则第一屏会自相矛盾（踩过）。
function redBots(d: Data): number {
  return (d.runtime?.bots ?? []).filter((b) => b.state === "fail").length;
}

function pendingCount(d: Data): number {
  return d.approvals?.wired ? (d.approvals.count ?? 0) : (d.approvals?.pending ?? []).length;
}

function issueCount(d: Data): number {
  return (d.issues?.fatal?.length ?? 0) + (d.issues?.warn?.length ?? 0);
}

// 结论只有四种，对应四个语义色。**不许**出现「看起来没事」而其实读不到：
// 读不到就明说是读不到（unknown），这是这一屏最重要的状态设计。
function verdict(d: Data): { text: string; tone: string } {
  if (d.err !== "") return { text: "真相源读不到，先别信这一屏", tone: "unknown" };
  if ((d.issues?.fatal?.length ?? 0) > 0) return { text: `真相源有 ${d.issues.fatal.length} 处红档`, tone: "block" };
  if (redBots(d) > 0) return { text: `有 ${redBots(d)} 个 bot 回不了话`, tone: "block" };
  if (pendingCount(d) > 0) return { text: `有 ${pendingCount(d)} 件在等人点头`, tone: "warn" };
  if ((d.issues?.warn?.length ?? 0) > 0) return { text: `没有卡点，但有 ${d.issues.warn.length} 处提醒`, tone: "warn" };
  return { text: "现在没有要你动手的事", tone: "ok" };
}

function row(what: Kid, meta: string, pill?: string): HTMLElement {
  return el("div", { class: "row" },
    el("div", { class: "what" }, what),
    el("div", { class: "meta" }, meta),
    pill ? el("span", { class: "pill " + pill }, pill === "block" ? "卡点" : pill === "warn" ? "等人" : "正常") : "",
  );
}

function todoCard(d: Data): Kid {
  const items: Kid[] = [];
  for (const a of (d.approvals?.pending ?? []).slice(0, 3)) {
    items.push(row(el("b", {}, a.title ?? a.id ?? "待批"), a.who ?? "", "warn"));
  }
  // 同一个原因导致的多个卡点归并成一条：三条一模一样的行，读的人要自己合并，那是设计偷懒。
  const fails = (d.runtime?.bots ?? []).filter((x) => x.state === "fail");
  const byWhy = new Map<string, string[]>();
  for (const b of fails) {
    const k = b.why || "（没给原因）";
    byWhy.set(k, [...(byWhy.get(k) ?? []), b.project]);
  }
  for (const [why, projects] of byWhy) {
    items.push(row(
      el("b", {}, projects.length > 1 ? `${projects.length} 个 bot：${projects.join("、")}` : (projects[0] ?? "")),
      why, "block"));
  }
  if (d.approvals == null && d.runtime == null) {
    items.push(el("div", { class: "denied" }, "运行态与待批都读不到 —— 不是没事，是看不见。"));
  }
  return el("div", { class: "card" },
    el("h3", {}, "要我做", el("span", { class: "hint" }, "卡点 + 待批")),
    items.length === 0 ? el("div", { class: "empty-state" }, "没有卡点，也没有待批。") : el("div", {}, ...items));
}

function whereCard(d: Data): Kid {
  const b = d.board;
  const line = (k: string, v: number, href: string): Kid =>
    row(el("a", { href, "data-link": "1" }, k), `${v}`, "");
  return el("div", { class: "card" },
    el("h3", {}, "东西在哪", el("span", { class: "hint" }, "按动词找")),
    b
      ? el("div", {},
          line("成员与 Agent", b.members.length, "#members"),
          line("业务域", b.domains.length, "#domains"),
          line("项目", b.projects.length, "#projects"))
      : el("div", { class: "denied" }, "真相源读不到，这一栏为空不是没有。"));
}

function historyCard(d: Data): Kid {
  const items = (d.audit?.records ?? []).slice(0, 3).map((a: { actor?: string; action?: string; at?: string; result?: string }) =>
    row(el("b", {}, `${a.actor ?? "?"} ${a.action ?? ""}`), (a.at ?? "").slice(5, 16),
      a.result === "ok" ? "ok" : "block"));
  return el("div", { class: "card" },
    el("h3", {}, "出过什么事", el("span", { class: "hint" }, "最近三条")),
    items.length === 0 ? el("div", { class: "empty-state" }, "还没有流水。") : el("div", {}, ...items));
}

export function renderOverviewV2(d: Data): Kid[] {
  const v = verdict(d);
  const b = d.board;
  const summary = el("div", { class: "v2" },
    el("div", { class: "kicker" }, "现在"),
    el("div", { class: "brief" },
      el("div", {},
        el("div", { class: "answer " + v.tone }, v.text),
        el("div", { class: "asof" },
          `${b?.company?.name ?? "公司"} · 截至 ${(b?.generated_at ?? "").replace("T", " ").slice(0, 16) || "—"}` +
          (issueCount(d) > 0 ? ` · 校验 ${issueCount(d)} 处` : "")),
      ),
      el("div", { class: "stats" },
        el("div", { class: "stat block" }, el("div", { class: "n" }, String(redBots(d))), el("div", { class: "k" }, "bot 红")),
        el("div", { class: "stat warn" }, el("div", { class: "n" }, String(pendingCount(d))), el("div", { class: "k" }, "待批")),
        el("div", { class: "stat" }, el("div", { class: "n" }, String(b?.projects?.length ?? 0)), el("div", { class: "k" }, "项目")),
      ),
    ),
    el("div", { class: "cols" }, todoCard(d), whereCard(d), historyCard(d)),
    el("div", { class: "links" },
      el("a", { href: "#data" }, "数据 →"),
      el("a", { href: "#apps" }, "应用 →"),
      el("a", { href: "#jobs" }, "作业 →"),
      el("a", { href: "#audit" }, "审计 →"),
      el("a", { href: "#timeline" }, "时间线 →"),
    ),
  );
  return [summary as unknown as Kid];
}

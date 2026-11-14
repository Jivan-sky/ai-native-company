import type { Data, DomainRow, Kid } from "../core/types";
import { el } from "../core/dom";
import { allowed, currentRole } from "../core/roles";
import { metricButton } from "../core/metrics";
import { efficiencyBlock } from "../ui/spark";

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
// verdict 是这一屏的**一句话结论**。优先级只有一个标准：
// **什么事需要人现在动手**。所以顺序是：读不到 → 真相源坏了 → 有件要你点头 → 没事。
//
// bot 红曾经排在待批前面 —— 于是老板打开看到的最大一行字是「有 3 个 bot 回不了话」，
// 而那是运维的事，不是他的事。现在它降级成一句附注（见 lead），不再抢标题。
function verdict(d: Data): { text: string; tone: string; lead: string } {
  if (d.err !== "") return { text: "真相源读不到，先别信这一屏", tone: "unknown", lead: d.err };
  if ((d.issues?.fatal?.length ?? 0) > 0) {
    return { text: `真相源有 ${d.issues.fatal.length} 处红档`, tone: "block",
      lead: "红档会拦住落盘。先修真相源 —— 看板不替你把红档藏起来。" };
  }
  if (pendingCount(d) > 0) {
    return { text: `有 ${pendingCount(d)} 件事等你点头`, tone: "warn",
      lead: "点头只留痕，不改真相源；该不该批仍然由你判断。" };
  }
  const red = redBots(d);
  const warn = d.issues?.warn?.length ?? 0;
  const tail = red > 0 ? `另外：${red} 个 bot 起不来（运维的事，不用你处理，去运行矩阵看该谁修）。` : "";
  if (warn > 0) return { text: `眼下没有要你点头的事`, tone: "warn", lead: `有 ${warn} 处提醒值得过目。` + tail };
  return { text: "眼下没有要你动手的事", tone: "ok", lead: tail || "该办的都办完了。" };
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
    el("h3", {}, "要我做"),
    items.length === 0 ? el("div", { class: "empty-state" }, "没有卡点，也没有待批。") : el("div", {}, ...items));
}

function whereCard(d: Data): Kid {
  const b = d.board;
  const role = currentRole();
  // 链接也要按身份过滤：成员点进「成员管理」只会看到一屏「看不到」，
  // 那不如一开始就不给他这个入口 —— 导航里的每一项都该是能进去的。
  const line = (k: string, v: number, id: string): Kid =>
    allowed(role, id) ? row(el("a", { href: "#" + id, "data-link": "1" }, k), `${v}`, "") : "";
  return el("div", { class: "card" },
    el("h3", {}, "东西在哪"),
    b
      ? el("div", {},
          line("成员与 Agent", b.members.length, "members"),
          line("业务域", b.domains.length, "domains"),
          line("项目", b.projects.length, "projects"))
      : el("div", { class: "denied" }, "真相源读不到，这一栏为空不是没有。"));
}

function historyCard(d: Data): Kid {
  const items = (d.audit?.records ?? []).slice(0, 3).map((a: { actor?: string; action?: string; at?: string; result?: string }) =>
    row(el("b", {}, `${a.actor ?? "?"} ${a.action ?? ""}`), (a.at ?? "").slice(5, 16),
      a.result === "ok" ? "ok" : "block"));
  return el("div", { class: "card" },
    el("h3", {}, "出过什么事"),
    items.length === 0 ? el("div", { class: "empty-state" }, "还没有流水。") : el("div", {}, ...items));
}

// 总览底部的快捷入口。同样按身份过滤 —— 详见 whereCard 的注释。
// 并页之后，「作业」这类入口要落到对应的**标签**，否则会停在那页的默认标签上
// （点「作业」却看到「应用」，是最容易漏的一种导航错）。
const SHORTCUTS: { id: string; label: string; href?: string }[] = [
  { id: "data", label: "数据" },
  { id: "projects", label: "项目" },
  { id: "run", label: "作业", href: "#/run?tab=jobs" },
  { id: "cases", label: "卡点" },
  { id: "audit", label: "审计" },
  { id: "timeline", label: "时间线" },
];

export function renderOverviewV2(d: Data): Kid[] {
  const v = verdict(d);
  const role = currentRole();
  const b = d.board;
  const summary = el("div", { class: "v2" },
    el("div", { class: "brief" },
      el("div", {},
        el("div", { class: "answer " + v.tone }, v.text),
        v.lead ? el("p", { class: "lead" }, v.lead) : "",
        el("div", { class: "asof" },
          `${b?.company?.name ?? "公司"} · 截至 ${(b?.generated_at ?? "").replace("T", " ").slice(0, 16) || "—"}` +
          (issueCount(d) > 0 ? ` · 校验 ${issueCount(d)} 处` : "")),
      ),
      el("div", { class: "stats" },
        el("div", { class: "stat warn" }, el("div", { class: "n" }, metricButton("pending", String(pendingCount(d)))), el("div", { class: "k" }, "等你点头")),
        el("div", { class: "stat" }, el("div", { class: "n" }, String(domainsOf(d).length)), el("div", { class: "k" }, "业务模块")),
        el("div", { class: "stat" }, el("div", { class: "n" }, String(b?.projects?.length ?? 0)), el("div", { class: "k" }, "在办项目")),
      ),
    ),
    // 「效率」那块原本在这里，用 done ÷ (done+failed) 算达成率。
    // 那个公式是替客户定义"什么叫效率"—— 客户接的是 ERP 与工单，人效根本不是这个算法。
    // 口径必须是配置项（谁定义、按什么算、窗口多长），写死在代码里就是在逼客户造假。
    // 在口径可配置之前，这块先不下屏 —— 空着比编一个强。
    modulesGrid(d),
    el("div", { class: "cols" }, todoCard(d), whereCard(d), historyCard(d)),
    el("div", { class: "links" },
      ...SHORTCUTS.filter((s) => allowed(role, s.id))
        .map((s) => el("a", { href: s.href ?? "#" + s.id }, s.label + " →"))),
  );
  return [summary as unknown as Kid];
}

// modulesGrid：一屏之内看「这家公司有哪几块业务、每块几个人几件事」。
//
// 格子**从真相源的业务域生成**，不是我在代码里写死的四个格。
// 客户加一个域（domains.md 多一行），这里就多一格 —— 这才叫可配置。
// 空域也要画出来：它「有人认领但还没资料」是真实状态，藏起来就成了假绿。
function domainsOf(d: Data): DomainRow[] {
  return d.board?.domains ?? [];
}

function modulesGrid(d: Data): Kid {
  const doms = domainsOf(d);
  if (doms.length === 0) {
    return el("div", { class: "v2" },
      el("div", { class: "card" },
        el("p", { class: "quiet" }, "真相源里还没有业务域（domains.md）—— 这一格空着是事实，不是错误。")));
  }
  const members = d.board?.members ?? [];
  const projects = d.board?.projects ?? [];
  const agents = d.board?.agents ?? [];
  const cells = doms.map((dom) => {
    const people = members.filter((m) => !m.disabled && (m.domains ?? []).includes(dom.slug)).length;
    const projs = projects.filter((p) => p.domain === dom.slug).length;
    const ags = agents.filter((a) => a.domain === dom.slug).length;
    const empty = people === 0 && projs === 0 && ags === 0;
    return el("a", { class: "mcell" + (empty ? " empty" : ""), href: "#/projects?domain=" + encodeURIComponent(dom.slug) },
      el("div", { class: "m-name" }, dom.name || dom.slug),
      el("div", { class: "m-nums" },
        el("span", {}, el("b", {}, String(people)), " 人"),
        el("span", {}, el("b", {}, String(projs)), " 项目"),
        el("span", {}, el("b", {}, String(ags)), " agent")),
      el("div", { class: "m-what" }, empty ? "有人认领，但还没有资料" : (dom.what || "—")));
  });
  return el("div", { class: "v2" },
    el("div", { class: "mods" },
      el("div", { class: "mods-h" }, el("h2", {}, "业务模块"), el("span", { class: "hint" }, doms.length + " 块 · 点进去看这块的活")),
      el("div", { class: "mods-grid" }, ...cells)));
}

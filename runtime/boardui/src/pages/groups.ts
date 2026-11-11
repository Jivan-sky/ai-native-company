import type { Attrs, Data, Kid } from "../core/types";
import { el } from "../core/dom";
import { deniedState } from "../ui/states";
import { currentId, hashQuery } from "../core/query";
import { allowed, currentRole, roleLabel } from "../core/roles";
import { renderAssets, renderCases, renderSediment } from "./assets";
import { renderAudit } from "./audit";
import { renderDomains, renderMembers } from "./org";
import { renderContext } from "./context";
import { renderData } from "./data";
import { renderGraph } from "./graph";
import { renderSearch } from "./search";
import { renderTimeline } from "./timeline";

// 三组合并页：资产 / 组织 / 记录。
//
// 合并的理由只有一条：**同一件事的几面不该各占一条主导航**。读者要自己在脑子里
// 把它们拼起来，而拼法本来是可以画出来的（一组标签）。
//
// 每条标签各自有自己的权限（用 tab 的 id 查 GATED）：所以「沉淀」只有管理员看得到，
// 而同一个「资产」页里的「原料」「检索」对所有人开放 —— 合并不会顺手放权。

export type SubTab = { id: string; label: string; hint: string; view: (d: Data) => Kid[] };

export function activeSubTab(tabs: SubTab[], fallback: string): SubTab {
  const here = currentId();
  const q = (hashQuery().get("tab") ?? "").trim();
  return tabs.find((t) => t.id === q) ?? tabs.find((t) => t.id === here) ?? tabs.find((t) => t.id === fallback) ?? tabs[0]!;
}

// 用户明确要的是哪一个标签（没有就说 null）。与「默认落在哪」是两件事。
function explicitSubTab(tabs: SubTab[]): SubTab | null {
  const here = currentId();
  const q = (hashQuery().get("tab") ?? "").trim();
  return tabs.find((t) => t.id === q) ?? tabs.find((t) => t.id === here) ?? null;
}

export function subHref(page: string, id: string): string {
  return "#/" + page + "?tab=" + id;
}

// 标签栏。**只画这个身份装得下的标签** —— 装不下的不是「灰掉」，是不出现。
export function subTabs(page: string, tabs: SubTab[], cur: SubTab): HTMLElement {
  const role = currentRole();
  return el("div", { class: "subtabs", role: "tablist" },
    ...tabs.filter((t) => allowed(role, t.id)).map((t) => {
      const attrs: Attrs = {
        class: t.id === cur.id ? "subtab active" : "subtab",
        href: subHref(page, t.id), role: "tab",
      };
      if (t.id === cur.id) attrs["aria-selected"] = "true";
      return el("a", attrs, el("b", {}, t.label), el("small", {}, t.hint));
    }));
}

function renderGroup(page: string, tabs: SubTab[], fallback: string, d: Data): Kid[] {
  const role = currentRole();
  const visible = tabs.filter((t) => allowed(role, t.id));
  if (visible.length === 0) return [];
  // 只有当用户**明确要**某个标签（`?tab=` 或 hash 就是那个 id）时才谈「无权」；
  // 没明说就落在第一个能看的 —— 否则成员打开「组织」会先吃到一句「成员与 Agent 不开放」。
  const asked = explicitSubTab(tabs);
  // 要的那一个标签装不下时说清楚，**不要**静默换成第一个能看的：
  // 一个把「审计」加进书签的人，落到「时间线」会以为审计是空的。
  const shown = asked === null || !allowed(role, asked.id) ? visible[0]! : asked;
  const denied = asked !== null && !allowed(role, asked.id)
    ? [deniedState(
        "「" + asked.label + "」对" + roleLabel(role) + "不开放。",
        "这一组里你能看的是：" + visible.map((t) => t.label).join(" / ") + "。要更多权限找你的上级或超管。")]
    : [];
  return [subTabs(page, tabs, shown), ...denied, ...(denied.length === 0 ? shown.view(d) : [])];
}

export const ASSET_TABS: SubTab[] = [
  { id: "assets", label: "原料", hint: "手上有什么料、多久没动了", view: renderAssets },
  { id: "search", label: "检索", hint: "按我的可见范围搜知识库", view: renderSearch },
  { id: "sediment", label: "沉淀", hint: "哪些能提炼成技能", view: renderSediment },
];
export const ORG_TABS: SubTab[] = [
  { id: "members", label: "成员与 Agent", hint: "谁在负责什么", view: renderMembers },
  { id: "domains", label: "业务域", hint: "一块业务是什么、数据在哪", view: renderDomains },
  { id: "graph", label: "关系图谱", hint: "怎么挂在一起的", view: renderGraph },
];
export const RECORD_TABS: SubTab[] = [
  { id: "timeline", label: "时间线", hint: "人 / agent 写下的东西", view: renderTimeline },
  { id: "cases", label: "卡点", hint: "现在卡在哪、谁该动", view: renderCases },
  { id: "audit", label: "审计", hint: "系统记下来的行使", view: renderAudit },
];

// 数据 = 数据源与策略 / 上下文。两者是同一件事的两端：
// 一头是「库里有什么」，一头是「agent 拿到什么去干活」。
export const DATA_TABS: SubTab[] = [
  { id: "data", label: "数据源与策略", hint: "连了什么、目录、策略、查询", view: renderData },
  { id: "context", label: "上下文", hint: "一个 agent 的工作上下文", view: renderContext },
];

export const renderDataGroup = (d: Data): Kid[] => renderGroup("data", DATA_TABS, "data", d);
export const renderAssetGroup = (d: Data): Kid[] => renderGroup("assets", ASSET_TABS, "assets", d);
export const renderOrgGroup = (d: Data): Kid[] => renderGroup("org", ORG_TABS, "members", d);
export const renderRecordGroup = (d: Data): Kid[] => renderGroup("records", RECORD_TABS, "timeline", d);

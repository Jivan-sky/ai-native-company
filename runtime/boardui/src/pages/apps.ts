import type { AppRow, Data, Kid } from "../core/types";
import { banner, chip, el, section, table } from "../core/dom";
import { shortTime } from "../core/format";
import { emptyNote } from "../ui/blocks";

// 「应用」页：装了什么、审没审、内容对不对。
//
// 这一页只读；装/审/撤都在 CLI（`anc app …`）—— 看板不发明第二套写口。
// 状态三档：pending（还没审）/ approved（可跑）/ revoked（撤了，立刻生效）。

export function appStateChip(state: string): HTMLElement {
  switch (state) {
    case "approved":
      return chip("已审批", true);
    case "revoked":
      return chip("已撤销", true);
    case "pending":
      return chip("待审批", true);
    default:
      return chip(state || "认不出", true);
  }
}

function rowCell(a: AppRow): Kid[] {
  return [
    el("span", { class: "mono nw" }, a.name),
    el("span", { class: "mono nw" }, a.version),
    appStateChip(a.state),
    // 内容 hash 全铺是 64 个字符，它会把这一列撑成一张纸。前 12 位足够对身份，全文在 title 上。
    el("span", { class: "mono nw", title: a.sha256 }, a.sha256.slice(0, 12) + "…"),
    (a.approved_by ?? "") === ""
      ? el("span", { class: "quiet" }, "—")
      : el("span", { class: "nw" }, a.approved_by ?? "", " · ", shortTime(a.approved_at ?? "")),
  ];
}

export function renderApps(d: Data): Kid[] {
  const v = d.apps;
  if (!v) {
    return [banner("bad", "读不到应用清单", [
      el("span", {}, d.appsErr === "" ? "服务没回 anc.apps/v1" : d.appsErr),
    ])];
  }
  if (!v.wired) {
    return [banner("warn", "应用目录没接上", [el("span", {}, v.why ?? "")])];
  }
  const out: Kid[] = [];
  out.push(section("装了什么", `共 ${v.apps.length} 个`,
    v.apps.length === 0
      ? emptyNote("还没有装应用。装一个：`anc app install <vault> <目录>`。")
      : table(["名字", "版本", "状态", "内容 hash", "谁审的"],
          v.apps.map((a) => rowCell(a)))));

  const pending = v.apps.filter((a) => a.state === "pending").length;
  if (pending > 0) {
    out.push(banner("warn", `有 ${pending} 个应用在等人审批`, [
      el("span", {}, "批准之前它们不会执行任何东西。"),
    ]));
  }
  return out;
}

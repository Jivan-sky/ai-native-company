import type { AuditEntryView, Data, Kid } from "../core/types";
import { pager, sliced } from "../ui/pager";
import { banner, chip, dash, el, section, table } from "../core/dom";
import { localTime, shortTime } from "../core/format";
import { hashQuery } from "../core/query";
import { cannot, dist, dn, dsep } from "../ui/blocks";
import { cmd, emptyState } from "../ui/states";
import { metricButton } from "../core/metrics";
import { dotLine } from "../ui/status";

// 四档结果：ok 实心圆 / denied 菱形 / failed 菱形 / 认不出的词虚线空圈。
// denied 与 failed 是两个词、两件事 —— 「权限没给」不能说成「故障」（后端同一条纪律）。
// 形状已经承担了颜色那的信息，所以文字只写「是什么」，「红 · 绿 · 」是废话。
export const AUDIT_DOT: Record<string, string> = {
  ok: "dot ok", denied: "dot bad", failed: "dot bad", unknown: "dot gray",
};
export const AUDIT_WORD: Record<string, string> = {
  ok: "成功", denied: "被拦下", failed: "失败", unknown: "判不出",
};
export const auditDot = (b: string): string => AUDIT_DOT[b] ?? "dot gray";
export const auditWord = (b: string): string => AUDIT_WORD[b] ?? "判不出";

// 「对谁」那一栏。四类各自是什么见后端 Zone 常量 ——
// 关键是**「判不出」不许显示成「不跨域」**：把「没判」画成「没事」是最坏的一种假绿。
export function zoneCell(e: AuditEntryView): Kid {
  switch (e.zone) {
    case "domain":
      if (!e.known) return el("span", {}, chip(e.actee, true), cannot(" 跨域未判"));
      if (e.cross) return el("span", {}, chip(e.actee, true), el("strong", {}, " ⚠ 跨域"));
      return chip(e.actee, true);
    case "vault":
      return cannot("vault 内 · 非域");
    case "outside":
      return el("strong", {}, "⚠ 域外");
    default:
      return cannot("判不出");
  }
}

// 一行流水只有一个长相：被拦下的那张表跟主流水用同一个渲染，
// 否则两张表的列会悄悄长歪，读的人得自己对齐。
// 一张流水扫的是「哪天、几点、谁、干了什么」。年份和秒在每一行都重复，
// 完整时间挂在 title 上 —— 要精确的时候悬停就有，不用的时候不占宽度。
function stamp(at: string): HTMLElement {
  return el("span", { class: "mono nw", title: localTime(at) }, shortTime(at));
}

function auditRow(e: AuditEntryView): Kid[] {
  return [
    dotLine(auditDot(e.band), el("span", {}, auditWord(e.band))),
    stamp(e.at),
    el("span", { class: "mono" }, dash(e.actor)),
    el("span", { class: "chip mono" }, dash(e.action)),
    zoneCell(e),
    targetCell(e.object),
  ];
}
// 目标只有一格宽：一行一条，长的省略，完整值在 title 上。
// 让一格折成三行，整张表的行高就会忽高忽低，扫起来更慢。
function targetCell(obj: string | undefined): HTMLElement {
  const s = dash(obj);
  return el("span", { class: "mono wide", title: s }, s);
}
const HEAD = ["结果", "时间", "谁", "类别", "对谁", "目标"];
// 顶部「要看的」只摆这几条：再多就不是提示，是糊墙。
const FLAG_CAP = 5;

export function renderAudit(d: Data): Kid[] {
  const v = d.audit;
  if (!v) {
    return [banner("bad", "读不到审计流水", [
      el("span", {}, d.auditErr === "" ? "服务没回 anc.audit/v1" : d.auditErr),
    ])];
  }
  if (!v.wired) {
    return [banner("warn", "审计还没接上", [
      el("span", {}, dash(v.error)),
      el("br"),
      el("span", { class: "mono" }, "anc audit collect <vault> --config <config.toml> --write"),
    ])];
  }

  const out: Kid[] = [];
  if ((v.error ?? "") !== "") out.push(banner("bad", "这一页读不动", [el("span", {}, dash(v.error))]));
  if (v.bad.length > 0) {
    out.push(banner("warn", "有 " + v.bad.length + " 行读不懂（逐行列出来，不静默跳过）",
      v.bad.map((b) => el("span", { class: "mono" }, b))));
  }

  // 要处理的排在最前：被拦下 = 权没给（查授权），失败 = 执行失败（查环境）。
  // 这两类才是「看完要动手」的；四档分布是读数，不是任务。
  const flaggedAll = v.records.filter((e) => e.band === "denied" || e.band === "failed");
  const flagged = flaggedAll.slice(0, FLAG_CAP);
  if (flagged.length > 0) {
    out.push(section("要看的",
      "被拦下 " + v.bands.denied + " · 失败 " + v.bands.failed,
      table(HEAD, flagged.map(auditRow)),
      flaggedAll.length > flagged.length
        ? el("p", { class: "quiet" }, "余下 " + (flaggedAll.length - flagged.length) + " 条在下面流水里。")
        : ""));
  }

  const au = sliced(v.records, hashQuery());
  out.push(section("流水",
    el("span", {}, "共 ", metricButton("audit-total", String(v.total)), " 条"),
    au.rows.length === 0
      ? emptyState("audit/ 里一行都还没有。",
          el("span", {}, "从 harness 记录归集：", cmd("anc audit collect <vault> --write"),
            "；补记一条：", cmd("anc audit add")))
      : el("div", {},
          table(HEAD, au.rows.map(auditRow)),
          pager(v.records.length, au.cur, "audit")),
    el("p", { class: "quiet" },
      "原话（工具结果 / 命令行）留在本机，不带上看板：",
      el("span", { class: "mono" }, "anc audit log <vault>"))));

  // 分布压成一行：它回答「有没有事」，不回答「怎么读」。
  out.push(section("分布", "",
    dist(
      dn(v.bands.ok, "成功", false, "audit-bands"),
      dn(v.bands.denied, "被拦下", true, "audit-bands"),
      dn(v.bands.failed, "失败", true, "audit-bands"),
      dn(v.bands.unknown, "结果认不出", false, "audit-bands"),
      dsep(),
      dn(v.scopes.domain, "域内", false, "audit-scopes"),
      dn(v.scopes.cross, "跨域", true, "audit-scopes"),
      dn(v.scopes.outside, "域外", true, "audit-scopes"),
      dn(v.scopes.vault, "vault 内", false, "audit-scopes"),
      dn(v.scopes.unknown, "目标抽不出", false, "audit-scopes"),
    )));

  // 答不了的照写 —— 这一页是「系统记下来的行使」，不是「全部行使」。
  if (v.missing.length > 0) {
    out.push(section("这一页答不了", "", el("ul", {}, ...v.missing.map((m) => el("li", {}, m)))));
  }
  return out;
}

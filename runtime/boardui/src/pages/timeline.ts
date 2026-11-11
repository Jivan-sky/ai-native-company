import type { Data, Kid, TimelineCaseView } from "../core/types";
import { pager, sliced } from "../ui/pager";
import { banner, chip, dash, el, section, table } from "../core/dom";
import { localTime, shortTime } from "../core/format";
import { hashQuery } from "../core/query";
import { cannot, dist, dn, dsep, emptyNote } from "../ui/blocks";
import { bandDot, bandLegend, bandWord, dotLine } from "../ui/status";

// 状态原词照排（不翻译、不吞）：认不出的词也得让人看见它长什么样。
export const statusCell = (status: string | undefined): Kid =>
  (status ?? "").trim() === "" ? cannot("没写 status") : el("span", { class: "mono" }, status ?? "");

// 「谁 → 交给谁」：两头都可能没写 —— 缺哪个就说缺哪个，不拿一个「—」糊过去。
// 这一列是这一页的重点：**下一手是谁的决定**。
export function handOff(by: string | undefined, to: string | undefined): Kid {
  const b = (by ?? "").trim();
  const t = (to ?? "").trim();
  if (b === "" && t === "") return cannot("没写谁在跟、也没说交给谁");
  return el("span", { class: "nw" },
    b === "" ? cannot("没写谁记的") : el("span", { class: "mono" }, b),
    el("span", {}, " → "),
    t === "" ? cannot("还没说交给谁") : el("strong", {}, t));
}

// 时间只写到分：一张 case 表扫的是「哪天、几点」，年份和秒每行都重复。
// 完整时间挂在 title 上 —— 要精确的时候悬停就有，不用的时候不占宽度。
function stamp(at: string): HTMLElement {
  return el("span", { class: "mono nw", title: localTime(at) }, shortTime(at));
}

// case 一格写完：标识、一句话、它在哪个域/项目。
// 原来这三样是三列，中间那列常常是「—」，空列比信息更响。
function caseCell(c: TimelineCaseView): HTMLElement {
  const where = [c.domain, c.project].filter((x) => (x ?? "").trim() !== "").join(" · ");
  return el("div", {},
    el("span", { class: "mono" }, dash(c.key)),
    el("div", {}, dash(c.title)),
    where === "" ? "" : el("div", { class: "quiet" }, where));
}

export function renderTimeline(d: Data): Kid[] {
  const v = d.timeline;
  if (!v) {
    return [banner("bad", "读不到时间线", [
      el("span", {}, d.timelineErr === "" ? "服务没回 anc.timeline/v1" : d.timelineErr),
    ])];
  }
  if (!v.wired) {
    return [banner("warn", "时间线还没接上", [
      el("span", {}, dash(v.error)),
      el("br"),
      el("span", { class: "mono" }, "anc timeline add <vault> -by <谁> -title <一句话>"),
    ])];
  }

  const out: Kid[] = [];
  if ((v.error ?? "") !== "") out.push(banner("bad", "这一页读不动", [el("span", {}, dash(v.error))]));
  if (v.bad.length > 0) {
    out.push(banner("warn", "有 " + v.bad.length + " 行读不懂（逐行列出来，不静默跳过）",
      v.bad.map((b) => el("span", { class: "mono" }, b))));
  }

  // 三档先给数，再给形状图例：数回答「有没有事」，图例回答「这个圆圈是什么意思」。
  // 原来五张卡各带一句 sub（status=done 之类），把同一件事说了五遍 —— 归到图例里说一次。
  out.push(section("三档", "",
    dist(
      dn(v.counts.green, "已完成"),
      dn(v.counts.yellow, "运行中"),
      dn(v.counts.red, "卡点 / 失败", true),
      dn(v.counts.unknown, "认不出"),
      dsep(),
      dn(v.entries, "流水条"),
      dn(v.total, "个 case"),
    ),
    bandLegend()));

  const tl = sliced(v.cases, hashQuery());
  const rows: Kid[][] = tl.rows.map((c) => [
    dotLine(bandDot(c.band), statusCell(c.status)),
    caseCell(c),
    handOff(c.by, c.to),
    String(c.entries),
    stamp(c.last_at),
  ]);
  // 标题就是它的口径：取最后一条当当前态。
  out.push(section("现在在哪一步",
    "共 " + v.cases.length + " 个 case",
    tl.rows.length === 0
      ? emptyNote("时间线还是空的（`timeline/` 里一行都没有）。空不是错 —— 「还没开始记」是个真实状态。")
      : el("div", {},
          table(["档 / status", "case", "谁 → 交给谁", "几条", "最近活动"], rows),
          pager(v.cases.length, tl.cur, "timeline"))));

  const steps: Kid[] = tl.rows.map((c) => {
    const head: Kid[] = [
      el("span", { class: bandDot(c.band) }),
      el("span", { class: "mono" }, dash(c.key)), " ",
      el("strong", {}, dash(c.title)), " ",
      chip(bandWord(c.band) + " · " + ((c.status ?? "").trim() === "" ? "没写 status" : c.status)),
      chip("共 " + c.entries + " 条", true),
    ];
    const inner: Kid[][] = c.recent.map((e) => [
      dotLine(bandDot(e.band), statusCell(e.status)),
      localTime(e.at),
      el("div", {},
        (e.kind ?? "").trim() === "" ? cannot("没写 kind") : el("span", { class: "chip mono" }, (e.kind ?? "").trim()),
        el("div", {}, dash(e.title)),
        (e.detail ?? "").trim() === "" ? "" : el("p", { class: "empty" }, dash(e.detail)),
        (e.refs ?? []).length === 0 ? "" : el("p", { class: "empty mono" }, (e.refs ?? []).join(" · "))),
      handOff(e.by, e.to),
    ]);
    return el("details", {}, el("summary", {}, el("span", { class: "st" }, ...head)),
      table(["档 / status", "时间", "这一步（kind + 一句话）", "谁 → 交给谁"], inner));
  });
  if (steps.length > 0) {
    out.push(section("每一步都留着", "最近几步 · 最新在上", ...steps));
  }

  if (v.total > v.cases.length) {
    out.push(section("还有更早的", "按最近动过排前面",
      el("p", { class: "empty" },
        el("span", {}, "往后翻："), el("span", { class: "mono" }, "anc timeline list <vault> --before " + dash(v.next_before)),
        el("span", {}, "；按档筛："), el("span", { class: "mono" }, "anc timeline list <vault> --band red"))));
  }

  if (v.missing.length > 0) {
    out.push(section("这一页答不了", "", el("ul", {}, ...v.missing.map((m) => el("li", {}, m)))));
  }
  return out;
}

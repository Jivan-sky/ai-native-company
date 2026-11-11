import type { Data, Kid } from "../core/types";
import { el } from "../core/dom";
import { metricButton } from "../core/metrics";

// 效率（每日）：功效 v0 —— 每天完成了多少、达成率多少。
//
// 只有三个点的时候，它不是「趋势线」，是「三天的读数」。所以画柱子不画折线，
// 并且把口径与天数写在下面一行：读的人要能判断这条线值不值得信。
// 数字本身是可点的 —— 点开是它的指标合同（见 core/metrics.ts）。
export function efficiencyBlock(d: Data): Kid | null {
  const series = d.timeline?.series ?? [];
  if (series.length === 0) return null;
  const max = Math.max(...series.map((s) => s.done), 1);
  const cols = series.map((s) => {
    const total = s.done + s.failed;
    const rate = total === 0 ? null : s.done / total;
    return el("div", { class: "sp-col" },
      el("div", { class: "sp-bar" },
        el("i", { style: "height:" + Math.max(3, Math.round((s.done / max) * 100)) + "%" })),
      el("div", { class: "sp-n" }, metricButton("daily-done", String(s.done))),
      el("div", { class: "sp-d" }, s.day.slice(5)),
      el("div", { class: "sp-r" },
        rate === null ? "—" : metricButton("done-rate", Math.round(rate * 100) + "%", "quiet")));
  });
  // 左边三个点，右边口径：三个柱子撑不满一整行，把宽度让给那句必须写清的话。
  return el("div", { class: "v2" },
    el("div", { class: "eff" },
      el("div", { class: "eff-main" },
        el("div", { class: "eff-head" },
          el("h2", {}, "效率"),
          el("span", { class: "hint" }, "每日完成 · " + series.length + " 天")),
        el("div", { class: "sp" }, ...cols)),
      el("div", { class: "eff-note" },
        el("div", {}, "口径：timeline 每日 done 条数；达成率 = done ÷ (done + failed)。"),
        el("div", {}, series.length < 7
          ? "只有 " + series.length + " 天 —— 看趋势，别当基线。"
          : "点数够看趋势了。"),
        el("div", { class: "quiet" }, "点数字看它是怎么算的。"))));
}

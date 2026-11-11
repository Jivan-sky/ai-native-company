import type { Data, JobRow, Kid } from "../core/types";
import { banner, chip, el, section, table } from "../core/dom";
import { emptyNote } from "../ui/blocks";

// 「作业」页：什么时候跑、上次成没成、下次几点。
//
// 下次时间是**后端用调度器同一个函数算的**（job.Due），看板不另算 ——
// 两份算法一定会漂，漂到最后没人知道该信哪份。

function schedText(j: JobRow): string {
  if ((j.every ?? "") === "") return "—";
  const at = (j.at ?? "") === "" ? "" : ` @ ${j.at}`;
  const tz = (j.tz ?? "") === "" ? "" : `（${j.tz}）`;
  return `${j.every}${at}${tz}`;
}

function lastCell(j: JobRow): Kid {
  if ((j.last_at ?? "") === "") return el("span", { class: "empty" }, "从没跑过");
  const when = (j.last_at ?? "").replace("T", " ").slice(0, 16);
  return el("span", {}, when, " ", j.last_ok ? chip("成功", true) : chip("失败", true));
}

function rowCell(j: JobRow): Kid[] {
  return [
    el("span", { class: "mono nw" }, j.name),
    el("span", { class: "mono nw" }, j.kind === "app" ? "应用" : "HTTP"),
    el("span", { class: "mono nw" }, schedText(j)),
    lastCell(j),
    el("span", { class: "mono nw" }, (j.next_at ?? "").replace("T", " ").slice(0, 16) || "—"),
    j.last_ok || (j.last_error ?? "") === ""
      ? el("span", { class: "empty" }, "—")
      : el("span", {}, j.last_error ?? ""),
  ];
}

export function renderJobs(d: Data): Kid[] {
  const v = d.jobs;
  if (!v) {
    return [banner("bad", "读不到作业清单", [
      el("span", {}, d.jobsErr === "" ? "服务没回 anc.jobs/v1" : d.jobsErr),
    ])];
  }
  if (!v.wired) {
    return [banner("warn", "作业目录没接上", [el("span", {}, v.why ?? "")])];
  }
  const out: Kid[] = [];
  out.push(section("作业", `共 ${v.jobs.length} 个`,
    v.jobs.length === 0
      ? emptyNote("还没有作业：写一份 spec 放进 `<vault>/../jobs/`。")
      : table(["名字", "类型", "计划", "上次", "下次", "上次失败原因"],
          v.jobs.map((j) => rowCell(j)))));

  const failed = v.jobs.filter((j) => !j.last_ok && (j.last_at ?? "") !== "").length;
  if (failed > 0) {
    out.push(banner("warn", `有 ${failed} 个作业上次失败`, [
      el("span", {}, "它们会按计划重试；连续失败要看原因，不是等它自己好。"),
    ]));
  }
  return out;
}

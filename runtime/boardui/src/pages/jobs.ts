import type { Data, JobRow, Kid } from "../core/types";
import { banner, chip, el, section, table } from "../core/dom";
import { localTime, shortTime } from "../core/format";
import { currentDomain, currentRole } from "../core/roles";
import { cmd, emptyState, unknownState } from "../ui/states";

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
  if ((j.last_at ?? "") === "") return el("span", { class: "quiet" }, "从没跑过");
  return el("span", { class: "nw" }, shortTime(j.last_at ?? ""), " ",
    j.last_ok ? chip("成功", true) : chip("失败", true));
}

function rowCell(j: JobRow): Kid[] {
  return [
    el("span", { class: "mono nw" }, j.name),
    el("span", { class: "mono nw" }, j.kind === "app" ? "应用" : "HTTP"),
    el("span", { class: "mono nw" }, schedText(j)),
    lastCell(j),
    el("span", { class: "mono nw", title: localTime(j.next_at ?? "") }, (j.next_at ?? "") === "" ? "—" : shortTime(j.next_at ?? "")),
    j.last_ok || (j.last_error ?? "") === ""
      ? el("span", { class: "quiet" }, "—")
      : el("span", {}, j.last_error ?? ""),
  ];
}

// 一个作业属于哪个域：spec 里**没有**域字段，只能按它写到哪反推 ——
// 取 write.path 的第一段，看它落在哪个域的 data 目录（domains.md 的 data 列）下。
// 这是推导，不是事实：推不出来的单列成「判不出归属」，绝不并进某个域。
function domainOf(writePath: string, dataDirs: string[]): string | null {
  const p = (writePath ?? "").trim();
  if (p === "") return null;
  const top = p.split("/")[0] ?? "";
  for (const dir of dataDirs) {
    const d0 = dir.trim();
    if (d0 === "") continue;
    if (top === d0 || p.startsWith(d0 + "/")) return d0;
  }
  return null;
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
  // 负责人：只列自己域那一摊。域由他选，归属按写入目录反推。
  const role = currentRole();
  const slug = currentDomain();
  const myDomain = role === "lead" ? (d.board?.domains ?? []).find((x) => x.slug === slug) : undefined;
  if (role === "lead" && !myDomain) {
    return [unknownState("还没选你负责的域。",
      "右上角选一个域；作业归属按它的写入目录反推（domains.md 的 data 列）。")];
  }
  let jobs = v.jobs;
  let excluded = 0;
  if (myDomain) {
    const mine = (myDomain.data ?? "").split(/[,\s]+/).filter((x) => x !== "");
    jobs = v.jobs.filter((j) => {
      const at = domainOf(j.write_path ?? "", mine);
      return at !== null;
    });
    excluded = v.jobs.length - jobs.length;
  }
  out.push(section("作业",
    myDomain
      ? el("span", {}, "共 ", String(jobs.length), " 个 · 只看 ", el("b", {}, myDomain.name || myDomain.slug),
          " · 另有 " + excluded + " 个不落在这个域")
      : `共 ${v.jobs.length} 个`,
    jobs.length === 0
      ? emptyState(myDomain ? "这个域下还没有作业。" : "还没有作业。",
          el("span", {}, "写一份 spec 放进 ", cmd("<vault>/../jobs/"), "；归属按它的 write.path 反推"))
      : table(["名字", "类型", "计划", "上次", "下次", "上次失败原因"],
          jobs.map((j) => rowCell(j)))));

  if (myDomain) {
    out.push(el("p", { class: "quiet" },
      "归属是", el("b", {}, "反推"), "的：作业 spec 里没有域字段，这里按 write.path 的第一段是否落在 ",
      el("span", { class: "mono" }, (myDomain.data || "（这个域没写 data 目录）")),
      " 下面判断。推不出来的不并进任何域 —— 上表里那些「不落在这个域」的就是。"));
  }
  const failed = jobs.filter((j) => !j.last_ok && (j.last_at ?? "") !== "").length;
  if (failed > 0) {
    out.push(banner("warn", `有 ${failed} 个作业上次失败`, [
      el("span", {}, "按计划会重试；连续失败看右列的失败原因。"),
    ]));
  }
  return out;
}

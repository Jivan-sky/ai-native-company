import type { Attrs, Data, Kid } from "../core/types";
import { el } from "../core/dom";
import { currentId, hashQuery } from "../core/query";
import { allowed, currentRole } from "../core/roles";
import { gatedTabNotice } from "./groups";
import { renderApps } from "./apps";
import { renderIntake } from "./intake";
import { renderJobs } from "./jobs";
import { renderModels } from "./models";

// 运行 —— 应用 / 模型 / 作业 / 接入。
//
// 这四样是同一件事的四个侧面：**谁在跑、跑什么、什么时候跑、怎么接进来**。
// 各占一条主导航等于让读者自己去拼它们的关系；并成一页、四个标签，关系是画出来的。
// 旧 hash（#/apps、#/models、#/jobs、#/intake）仍能打开，落到对应标签。
export type RunTab = { id: string; label: string; hint: string; view: (d: Data) => Kid[] };

export const RUN_TABS: RunTab[] = [
  { id: "apps", label: "应用", hint: "装了什么、审没审", view: renderApps },
  { id: "models", label: "模型", hint: "注册了什么、密钥配没配", view: renderModels },
  { id: "jobs", label: "作业", hint: "什么时候跑、上次成没成", view: renderJobs },
  { id: "intake", label: "接入", hint: "接一个脚本或接口进来", view: renderIntake },
];

export function activeRunTab(): RunTab {
  const here = currentId();
  const q = (hashQuery().get("tab") ?? "").trim();
  return RUN_TABS.find((t) => t.id === q)
    ?? RUN_TABS.find((t) => t.id === here)
    ?? RUN_TABS[0]!;
}

export function runHref(id: string): string {
  return "#/run?tab=" + id;
}

export function renderRun(d: Data): Kid[] {
  // 标签也要按身份筛：负责人只装得下「作业」，不该看见应用 / 模型 / 接入的入口。
  const role = currentRole();
  const visible = RUN_TABS.filter((t) => allowed(role, t.id));
  const asked = activeRunTab();
  const allowedAsked = allowed(role, asked.id);
  const cur = allowedAsked ? asked : visible[0]!;
  const denied = gatedTabNotice(asked, visible, role);
  const tabs = el("div", { class: "subtabs", role: "tablist" },
    ...visible.map((t) => {
      const attrs: Attrs = {
        class: t.id === cur.id ? "subtab active" : "subtab",
        href: runHref(t.id), role: "tab",
      };
      if (t.id === cur.id) attrs["aria-selected"] = "true";
      return el("a", attrs, el("b", {}, t.label), el("small", {}, t.hint));
    }));
  return denied.length === 0 ? [tabs, ...cur.view(d)] : [tabs, ...denied];
}

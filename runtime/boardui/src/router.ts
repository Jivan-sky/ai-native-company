import { icon } from "./core/icons";
import type { Page } from "./core/types";

import { renderApprovals } from "./pages/approvals";

import { renderRun } from "./pages/run";
import { renderProjects } from "./pages/org";
import { renderOverview } from "./pages/overview";
import { renderAssetGroup, renderDataGroup, renderOrgGroup, renderRecordGroup } from "./pages/groups";
import { renderOverviewV2 } from "./pages/overview-v2";
import { renderSystem } from "./pages/system";

// 页面注册表。顺序就是侧栏顺序；分组是「问题」，页名是「答案」。
// 这一版把导航收敛成六组：基础设施不再各占一条，组织关系落到「成员与 Agent」。
export const overviewPage: Page = {
  id: "overview", group: "总览", label: "总览", icon: "overview", wired: true,
  question: "现在有没有事、要不要我动手？",
  sub: "一张运营态势图：校验、待批、运行、热层、资产，五路信号汇到同一个视野里。",
  view: renderOverviewV2,
};
export const approvalsPage: Page = {
  id: "approvals", group: "总览", label: "待批", icon: "check", wired: true,
  question: "哪几件事在等人点头、该谁点？", view: renderApprovals,
};
export const projectsPage: Page = {
  id: "projects", group: "工作", label: "项目", icon: "layers", wired: true,
  question: "谁在做什么、到几号、卡住找谁、进度到哪？",
  sub: "周期时间消耗按原文日期推算；交付进度没有真相源时明确留白。",
  view: renderProjects,
};
export const assetsGroupPage: Page = {
  id: "assets", group: "资产", label: "资产", icon: "box", wired: true,
  question: "手上有什么料、能搜到什么、哪些能沉淀成技能？",
  view: renderAssetGroup,
};
export const orgGroupPage: Page = {
  id: "org", group: "组织", label: "组织", icon: "users", wired: true,
  question: "谁在负责什么、业务域怎么划、它们怎么挂在一起？",
  view: renderOrgGroup,
};
export const recordsGroupPage: Page = {
  id: "records", group: "记录", label: "记录", icon: "clock", wired: true,
  question: "谁写下了什么、现在卡在哪、系统记了哪些行使？",
  view: renderRecordGroup,
};
export const runPage: Page = {
  id: "run", group: "运行", label: "运行", icon: "flow", wired: true,
  question: "谁在跑、跑什么、什么时候跑、怎么接进来？",
  view: renderRun,
};
export const dataPage: Page = {
  id: "data", group: "数据", label: "数据", icon: "layers", wired: true,
  question: "连了哪些数据、目录里有什么、谁能看哪一列、agent 拿什么去干活？",
  view: renderDataGroup,
};
export const systemPage: Page = {
  id: "system", group: "系统", label: "运行与连接", icon: "pulse", wired: true,
  question: "基础设施还健康吗、执行面和授权有没有缺口？",
  sub: "运行矩阵 / 网关 / 授权 / 数据流 / 热层集中在这里；正常时不打扰，异常时才要人看。",
  view: renderSystem,
};

export const PAGES: Page[] = [
  overviewPage, approvalsPage,
  projectsPage,
  orgGroupPage,
  runPage, dataPage,
  recordsGroupPage,
  assetsGroupPage,
  systemPage,
];

// 旧 hash 仍要能打开：这些页并进「系统」或「成员与 Agent」之后，书签和飞书里的旧链接不能断。
// 只改写落点，不改语义 —— 页面自己会按 tab 参数决定显示哪一段。
export const LEGACY: Record<string, string> = {
  runtime: "system", dataflow: "system", gateway: "system", hot: "system", grants: "system",
  // 应用 / 模型 / 作业 / 接入 → 运行
  apps: "run", models: "run", jobs: "run", intake: "run",
  // 成员 / 业务域 / 图谱 → 组织；原料 / 检索 / 沉淀 → 资产；时间线 / 卡点 / 审计 → 记录
  members: "org", domains: "org", graph: "org", roles: "org",
  assets: "assets", search: "assets", sediment: "assets",
  timeline: "records", cases: "records", audit: "records",
  // 上下文并进了「数据」页的第二个标签
  context: "data",
  // 注意：这里**不能**再有 `org: "overview"` —— 「组织」这一页自己就叫 org，
  // 再留一条旧映射会让它永远打不开（pageFor 先查 LEGACY）。
};

export function pageFor(id: string): Page {
  const to = LEGACY[id] ?? id;
  for (const p of PAGES) if (p.id === to) return p;
  return overviewPage;
}

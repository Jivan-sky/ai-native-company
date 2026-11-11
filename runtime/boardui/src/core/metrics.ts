import type { Kid } from "./types";
import { el } from "../core/dom";
import { openDrawer } from "../ui/drawer";
import { track } from "./usage";

// 指标合同：屏幕上的每个数字，都要能在这里查到它是怎么算的、从哪来、什么时候算的、哪里会错。
//
// 这张表和 `docs/METRICS.md` 是同一份内容的两个读者：人看文档，界面看这张表。
// 加一个上屏的数字之前先在这里加一行 —— 加不出来，就是不该上屏。

export type Metric = {
  id: string;
  label: string;
  formula: string;
  source: string;
  fresh: string;
  bias: string;
  owner: string;
  // 独立复核用的命令（能给的都给：读的人不该只信这个界面）
  check?: string;
};

export const METRICS: Metric[] = [
  {
    id: "bot-red", label: "bot 红",
    formula: "runtime.bots 里 state == \"fail\" 的个数",
    source: "/api/runtime", fresh: "每次刷新", bias: "`unwired` 不算红；网关没跑会让全部 bot 一次全红",
    owner: "运行",
  },
  {
    id: "pending", label: "待批",
    formula: "approvals.count（热层队列长度）",
    source: "/api/approvals", fresh: "每次刷新", bias: "同一条提案不重复入队（服务端幂等键）",
    owner: "审批",
  },
  {
    id: "projects", label: "项目",
    formula: "board.projects.length",
    source: "真相源投影", fresh: "每次投影", bias: "没写进 projects.md 的项目不计",
    owner: "组织",
  },
  {
    id: "checks", label: "校验处数",
    formula: "issues.fatal.length + issues.warn.length",
    source: "anc org check 同源", fresh: "每次投影", bias: "红档与非红档混在一个数里，点开才分得清",
    owner: "组织",
  },
  {
    id: "daily-done", label: "每日 done",
    formula: "每天 status == \"done\" 的 timeline 条数",
    source: "timeline/*.jsonl", fresh: "每次刷新",
    bias: "只说明「忙」，不说明「有用」；重试也会算一条",
    owner: "记录",
    check: 'cat timeline/*.jsonl | jq -r \'.at[0:10] + "\\t" + .status\' | sort | uniq -c',
  },
  {
    id: "done-rate", label: "达成率",
    formula: "done ÷ (done + failed)，按天",
    source: "timeline/*.jsonl", fresh: "每次刷新",
    bias: "`ok` 与认不出的词**不进**分子分母（单列）；一条大 case 卡三天不敏感",
    owner: "记录",
    check: "cat timeline/*.jsonl | jq -r '[.at[0:10], .status] | @tsv' | awk -F'\\t' '{if($2==\"done\")d[$1]++; else if($2==\"failed\")f[$1]++} END{for(k in d) printf \"%s done=%d failed=%d\\n\", k, d[k], f[k]}'",
  },
  {
    id: "audit-bands", label: "审计四档",
    formula: "按 result 分档：ok / denied / failed / 认不出的词",
    source: "audit/*.jsonl", fresh: "每次刷新", bias: "认不出的词单独一档，绝不并入成功",
    owner: "审计",
  },
  {
    id: "audit-scopes", label: "扫域分布",
    formula: "按目标落点分：域内 / 跨域 / 域外 / vault 内 / 判不出",
    source: "audit/*.jsonl + Scope", fresh: "每次刷新",
    bias: "**判不出 ≠ 不跨域**；行使者没配域时一律判不出",
    owner: "审计",
  },
  {
    id: "audit-total", label: "审计总条数",
    formula: "jsonl 行数（不含读不懂的行）",
    source: "audit/*.jsonl", fresh: "每次刷新", bias: "看板只铺最近 N 条，总数与屏上条数不同",
    owner: "审计",
  },
  {
    id: "tl-bands", label: "三档分布",
    formula: "按 band 分：已完成 / 运行中 / 卡点·失败 / 认不出；同一条里附流水条数与 case 数（同一份 timeline）",
    source: "timeline/*.jsonl", fresh: "每次刷新", bias: "`ok` 不在档位表里，会落进「认不出」",
    owner: "记录",
  },
  {
    id: "routing-hits", label: "去向命中",
    formula: "按 audit 里 write 记录的 object 顶层目录分组计数（去掉 …/ 与 <vault>/ 前缀）",
    source: "audit/*.jsonl + board.routing", fresh: "每次刷新",
    bias: "只数**记下来**的 write；没进 audit 的流入看不到，所以 0 不代表没发生",
    owner: "审计",
  },
  {
    id: "cases-red", label: "卡点数（红）",
    formula: "timeline 折叠后 band == \"red\" 的 case 数；按人分组时是同一条按 by 切",
    source: "timeline/*.jsonl", fresh: "每次刷新",
    bias: "红 = blocked / failed；一条 case 多行只算一次；没写 by 的会落进「没写谁」",
    owner: "记录",
  },
  {
    id: "catalog", label: "表 / 列 / 已拍板",
    formula: "目录快照里的表数、列数、classifications 条数",
    source: "/api/catalog", fresh: "每次 catalog scan", bias: "快照过期后仍是旧数；屏上有「扫于」时间",
    owner: "数据",
  },
];

const BY_ID = new Map(METRICS.map((m) => [m.id, m]));

export function metric(id: string): Metric | undefined {
  return BY_ID.get(id);
}

// 点开一个数字：把它怎么来的摊在抽屉里。
// 这不是「帮助」弹窗 —— 它是这个产品唯一想被人记住的那件事：每个数都能回源。
export function openContract(id: string): void {
  const m = BY_ID.get(id);
  if (!m) return;
  track("metric", id);
  const kv: Kid[] = [];
  const row = (k: string, v: Kid): void => {
    kv.push(el("dt", {}, k), el("dd", {}, v));
  };
  row("公式", el("span", { class: "mono" }, m.formula));
  row("来源", el("span", { class: "mono" }, m.source));
  row("新鲜度", m.fresh);
  row("已知偏差", m.bias);
  row("负责人", m.owner);
  if (m.check) row("自己复核", el("pre", { class: "file-content" }, m.check));
  openDrawer({
    eyebrow: "指标合同",
    title: m.label,
    sub: "这个数字怎么算出来的、从哪来、哪里会错。",
    body: [el("dl", {}, ...kv)],
  });
}

// 一个可点的数字：外面看是数，点开是它的合同。
// 用 button 而不是 div：键盘能 Tab 到、回车能开 —— 没有人会为了看一个数去用鼠标。
export function metricButton(id: string, value: string, cls = ""): HTMLElement {
  const b = el("button", {
    class: "metric" + (cls === "" ? "" : " " + cls),
    type: "button",
    title: "看这个数是怎么算的",
    "aria-label": "查看「" + (BY_ID.get(id)?.label ?? id) + "」的口径",
  }, value) as HTMLButtonElement;
  b.addEventListener("click", () => openContract(id));
  return b;
}

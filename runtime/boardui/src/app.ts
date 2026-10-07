// ANC 看板前端（浏览器侧）。
//
// 它只做一件事：把 `/api/board`（anc.board/v1 投影）与 `/api/issues`（真相源健康度）
// 摆成人能看的界面。
//
// 五条纪律：
//   1. 真相源里的字一律走 textContent，绝不 innerHTML —— 看板不许成为注入点；
//   2. 不猜、不补：哪块没有数据源就写清楚「还没接入」，不编假数据把界面填满；
//   3. 只读：这个页面没有任何写操作（后端也只放行 GET / HEAD）；
//   4. 一页只回答一个问题 —— 那句问题写在页头（Page.question），写不出来就该先想清楚再开页；
//   5. 数据源没接通的页面，导航上直接标「未接入」，不靠人点进去才发现。
//
// 构建：`npm run build`（esbuild 打成 app.js，随 anc 二进制一起嵌进去）。

type Company = { name: string; id: string; platform: string; language: string; timezone: string };
type Role = { role: string; title: string; mode: string; model: string; vault_scope: string[] };
type Member = {
  name: string; display_name: string; role: string;
  admin: boolean; disabled: boolean; model: string; domains: string[];
};
type DomainRow = {
  slug: string; name: string; what: string; who: string; who_label: string;
  data: string; sources: string; terms: string;
};
type ProjectRow = {
  slug: string; name: string; domain: string; owner: string; owner_label: string;
  period: string; source: string; charter: string;
};
type RoutingRow = { dir: string; summary: string };
type BoardView = {
  schema: string; generated_at: string; company: Company;
  roles: Role[]; members: Member[]; domains: DomainRow[];
  projects: ProjectRow[]; routing: RoutingRow[];
};
type Finding = { rule: string; level: string; where: string; msg: string };
type Issues = { ok: boolean; fatal: Finding[]; warn: Finding[]; error: string };

// 运行态观测（`/api/runtime`，schema = anc.runtime/v1）—— 它与 anc.board/v1 是**两份**
// 契约：一份讲真相源长什么样，一份讲运行态健不健康。别把两者混着用。
type RuntimeBot = { project: string; state: string; why: string };
type RuntimeView = {
  schema: string; wired: boolean; gateway: string; gateway_why?: string;
  bots: RuntimeBot[]; extras?: string[];
  handlers: string[]; handler_why?: string; error?: string;
};
// 数据流策略面（`/api/dataflow`，schema = anc.dataflow/v1）—— 「谁可以做什么、通道开着吗」。
// 入站只出**人数**，不出标识符：看板是观测面，不是凭据面（SPEC §2.3 / §6-3）。
type DataflowBot = {
  project: string; role: string; mode: string; model: string;
  tools: string[]; inbound: number; inbound_extra: number;
};
type DataflowView = {
  schema: string; wired: boolean;
  relay: { declared: boolean; timeout_secs: number; note: string };
  exec: {
    config_present: boolean; has_fingerprint: boolean;
    version?: string; inputs?: string; generated_at?: string;
    projects: string[]; note?: string;
  };
  bots: DataflowBot[]; gaps: string[]; missing: string[];
  error?: string;
};

// 原料清单（`/api/assets`，schema = anc.assets/v1）。这一页给的是**原料**，不是沉淀。
type AssetsFile = { path: string; bytes: number; modified: string };
type AssetsDir = {
  dir: string; summary: string; files: number; bytes: number;
  newest?: string; recent: AssetsFile[]; truncated: boolean;
};
type AssetsView = {
  schema: string; wired: boolean; dirs: AssetsDir[];
  charters: number; note: string; error?: string;
};

// 一次取数、全程共用：已接入的页吃的是同一份投影，切页不重新打网络（按「刷新」才重取）。
type Data = {
  board: BoardView | null; issues: Issues;
  runtime: RuntimeView | null; runtimeErr: string;
  dataflow: DataflowView | null; dataflowErr: string;
  assets: AssetsView | null; assetsErr: string;
  err: string;
};

type Attrs = Record<string, string>;
type Kid = Node | string;

const root = document.getElementById("app");

// ---------- 画界面的最小零件 ----------

function el(tag: string, attrs: Attrs = {}, ...kids: Kid[]): HTMLElement {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") node.className = v;
    else node.setAttribute(k, v);
  }
  for (const kid of kids) node.append(kid);
  return node;
}

const dash = (v: string | null | undefined): string => {
  const s = (v ?? "").trim();
  return s === "" ? "—" : s;
};

const chip = (label: string, mono = false): HTMLElement =>
  el("span", { class: mono ? "chip mono" : "chip" }, label);

function card(title: string, badge: string, rows: Array<[string, Kid]>): HTMLElement {
  const head = el("h3", {}, title);
  if (badge !== "") head.append(el("span", { class: "chip mono" }, badge));
  const dl = el("dl");
  for (const [k, v] of rows) dl.append(el("dt", {}, k), el("dd", {}, v));
  return el("div", { class: "card" }, head, dl);
}

function table(headers: string[], rows: Kid[][]): HTMLElement {
  const head = el("tr");
  for (const h of headers) head.append(el("th", {}, h));
  const body = el("tbody");
  for (const r of rows) {
    const tr = el("tr");
    for (const c of r) tr.append(el("td", {}, c));
    body.append(tr);
  }
  // 套一层滚动容器：窄屏（比如嵌在侧栏里）表格不该把整页撑破。
  return el("div", { class: "tw" }, el("table", {}, el("thead", {}, head), body));
}

function section(title: string, hint: string, ...body: Kid[]): HTMLElement {
  const h = el("h2", {}, title);
  if (hint !== "") h.append(el("span", { class: "hint" }, hint));
  return el("section", { class: "glass" }, h, ...body);
}

function banner(kind: "bad" | "warn", title: string, items: Kid[]): HTMLElement {
  const ul = el("ul");
  for (const i of items) ul.append(el("li", {}, i));
  return el("div", { class: "banner " + kind }, el("h3", {}, title), ul);
}

const emptyNote = (s: string): HTMLElement => el("p", { class: "empty" }, s);

// 「本页判断不了 / 不加戏」这类否定断言，单独着色 —— 它是诚实的一部分，不该藏起来。
const cannot = (s: string): HTMLElement => el("span", { class: "cannot" }, s);

// 真源指针只把 http(s) 当链接渲染；别的写法（含 javascript: 之类）一律当纯文字。
function sourceCell(v: string): Kid {
  const s = (v ?? "").trim();
  if (s === "") return "—";
  if (!/^https?:\/\//i.test(s)) return el("span", { class: "mono" }, s);
  return el("a", { href: s, target: "_blank", rel: "noreferrer noopener" }, s);
}

function findingLine(f: Finding): Kid {
  const where = (f.where ?? "").trim();
  return el("span", {},
    el("span", { class: "mono" }, f.rule), " ",
    where === "" ? f.msg : where + "：" + f.msg);
}

function localTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? dash(iso) : d.toLocaleString();
}
// 体积给人看，不给机器看（机器那份在接口里，是字节数）。
function humanBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "—";
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + " MB";
  return (n / 1024 / 1024 / 1024).toFixed(1) + " GB";
}

function statusDot(i: Issues): string {
  if (i.error !== "" || i.fatal.length > 0) return "dot bad";
  if (i.warn.length > 0) return "dot warn";
  return "dot ok";
}

function statusText(i: Issues): string {
  if (i.error !== "") return "真相源读不了";
  if (i.fatal.length > 0) return "红档 " + i.fatal.length + " 条";
  if (i.warn.length > 0) return "非红档 " + i.warn.length + " 条";
  return "校验干净";
}

// ---------- 取数 ----------

type Fetched<T> = { status: number; body: T | null; err: string };

async function getJSON<T>(url: string): Promise<Fetched<T>> {
  try {
    const res = await fetch(url, { headers: { accept: "application/json" } });
    const raw = await res.text();
    try {
      return { status: res.status, body: JSON.parse(raw) as T, err: "" };
    } catch {
      return { status: res.status, body: null, err: "返回的不是 JSON（HTTP " + res.status + "）" };
    }
  } catch (e) {
    return { status: 0, body: null, err: "连不上看板服务：" + (e instanceof Error ? e.message : String(e)) };
  }
}

const emptyIssues = (err: string): Issues => ({ ok: false, fatal: [], warn: [], error: err });

// 「读到了一份能吃的契约」= body 在、schema 是非空字符串。四份契约（board / runtime /
// dataflow / assets）各判各的 —— 一份坏了不该让别的页跟着空着。
function contract<T extends { schema?: string }>(res: Fetched<T>, label: string): { v: T | null; err: string } {
  const body = res.body;
  if (body && typeof body.schema === "string" && body.schema !== "") return { v: body, err: "" };
  return {
    v: null,
    err: res.err !== "" ? res.err : label + "读取失败（HTTP " + res.status + "）",
  };
}

async function fetchData(): Promise<Data> {
  const [boardRes, issuesRes, runtimeRes, dataflowRes, assetsRes] = await Promise.all([
    getJSON<BoardView & { error?: string }>("/api/board"),
    getJSON<Issues>("/api/issues"),
    getJSON<RuntimeView>("/api/runtime"),
    getJSON<DataflowView>("/api/dataflow"),
    getJSON<AssetsView>("/api/assets"),
  ]);
  const issues = issuesRes.body ?? emptyIssues(issuesRes.err);
  const rt = contract(runtimeRes, "运行态");
  const df = contract(dataflowRes, "数据流");
  const as = contract(assetsRes, "原料清单");
  const schema = boardRes.body?.schema;
  if (boardRes.body && typeof schema === "string" && schema !== "") {
    return {
      board: boardRes.body, issues, err: "",
      runtime: rt.v, runtimeErr: rt.err,
      dataflow: df.v, dataflowErr: df.err,
      assets: as.v, assetsErr: as.err,
    };
  }
  const why = boardRes.body?.error ?? boardRes.err ?? ("看板数据读取失败（HTTP " + boardRes.status + "）");
  return {
    board: null, issues, err: why,
    runtime: rt.v, runtimeErr: rt.err,
    dataflow: df.v, dataflowErr: df.err,
    assets: as.v, assetsErr: as.err,
  };
}

// ---------- 可复用的块 ----------

function noBoard(d: Data): Kid {
  return banner("bad", "读不到投影", [
    el("span", {}, d.err === "" ? "服务返回的不是 anc.board/v1" : d.err),
    el("br"),
    el("span", { class: "mono" }, "组织 / 项目 两页都吃这份投影；先修它。"),
  ]);
}

function alertBanners(i: Issues): Kid[] {
  const out: Kid[] = [];
  if (i.fatal.length > 0) {
    out.push(banner("bad", "红档 " + i.fatal.length + " 条 —— 拦住落盘，先修它", i.fatal.map(findingLine)));
  } else if (i.error !== "") {
    out.push(banner("bad", "校验状态读不了", [el("span", {}, i.error)]));
  }
  if (i.warn.length > 0) {
    out.push(banner("warn", "非红档 " + i.warn.length + " 条（不拦，但请过目）", i.warn.map(findingLine)));
  }
  return out;
}

function statCard(n: string, k: string, sub: string): HTMLElement {
  return el("div", { class: "card stat" },
    el("div", { class: "n" }, n), el("div", { class: "k" }, k), el("div", { class: "sub" }, sub));
}

function statSection(v: BoardView): Kid {
  const active = v.members.filter((m) => !m.disabled).length;
  return section("规模", "全部由真相源现算，不缓存、不猜",
    el("div", { class: "grid stats" },
      statCard(String(v.domains.length), "业务域", "一块业务一行"),
      statCard(String(v.projects.length), "项目", "在做的事"),
      statCard(String(v.roles.length), "角色", "岗位"),
      statCard(active + "/" + v.members.length, "成员", "启用 / 全部"),
      statCard(String(v.routing.length), "路由目录", "顶层数据目录")));
}

function linkTo(id: string): Kid {
  const p = PAGES.find((x) => x.id === id);
  return el("a", { href: "#/" + id }, p ? p.label : id);
}

function notWiredIndex(): Kid {
  const rows: Kid[][] = [
    [linkTo("dataflow"), "谁授权了谁、通道开着吗、有没有越权？", "通道表 / 授权表 / 审计流水"],
    [linkTo("assets"), "沉淀了什么、哪些能复用？", "agent 每日产出"],
  ];
  return section("两块还没接数据源", "各自一页，如实写明缺什么 —— 不编数据填界面",
    table(["页面", "将回答的问题", "缺的数据源"], rows));
}

function domainSection(v: BoardView): Kid {
  if (v.domains.length === 0) {
    return section("业务域（罗盘）", "",
      emptyNote("还没划域（domains.md 里没有数据行）—— agent 少一条判断口径的依据。"));
  }
  const cards = v.domains.map((d) => card(dash(d.name), dash(d.slug), [
    ["是什么", dash(d.what)],
    ["谁在做", dash(d.who_label)],
    ["数据在哪", dash(d.data)],
    ["原件来源", dash(d.sources)],
    ["口径 / 术语", dash(d.terms)],
  ]));
  return section("业务域（罗盘）", "一块业务一行 —— agent 靠它回答「这是哪块业务、数据在哪、卡住找谁」",
    el("div", { class: "grid" }, ...cards));
}

function projectSection(v: BoardView): Kid {
  if (v.projects.length === 0) {
    return section("项目", "", emptyNote("项目表还是空的（projects.md 没有数据行）—— 由 agent 从立项书抽取后填。"));
  }
  const rows = v.projects.map((p): Kid[] => [
    el("span", {}, dash(p.name), " ", el("span", { class: "chip mono" }, dash(p.slug))),
    dash(p.domain),
    dash(p.owner_label),
    dash(p.period),
    sourceCell(p.source),
    p.charter === "" ? "还没有" : el("span", { class: "mono" }, p.charter),
  ]);
  return section("项目", "一行一个项目；周期是原文照搬，ANC 不解读",
    table(["项目", "挂哪块业务", "卡住找谁", "周期（原文）", "真源", "立项书副本"], rows));
}

function roleSection(v: BoardView): Kid {
  const byRole = new Map<string, Member[]>();
  for (const m of v.members) {
    const list = byRole.get(m.role);
    if (list) list.push(m);
    else byRole.set(m.role, [m]);
  }
  if (v.roles.length === 0) {
    return section("角色与成员", "", emptyNote("还没有角色（roles/ 为空）。"));
  }
  const cards = v.roles.map((r) => {
    const people = (byRole.get(r.role) ?? []).map((m) =>
      dash(m.display_name) + (m.disabled ? "（已停用）" : "") + (m.admin ? "（管理员）" : "")).join("、");
    return card(dash(r.title), r.role, [
      ["模型", dash(r.model)],
      ["模式", dash(r.mode)],
      ["主力目录", r.vault_scope.length > 0 ? r.vault_scope.join("、") : "—"],
      ["成员", people === "" ? "暂无人" : people],
    ]);
  });
  return section("角色与成员", "人名由 members/ 现算 —— 表里只维护岗位，人事变动不用改表",
    el("div", { class: "grid" }, ...cards));
}

function routingSection(v: BoardView): Kid {
  if (v.routing.length === 0) {
    return section("数据路由", "", emptyNote("还没有顶层数据目录。"));
  }
  const rows = v.routing.map((r): Kid[] => [el("span", { class: "mono" }, dash(r.dir)), dash(r.summary)]);
  return section("数据路由", "顶层数据目录 → 说明（persona 段 3 用的就是这张表）",
    table(["目录", "说明"], rows));
}

// ---------- 页面 ----------
//
// 一页一个 Page：`id` 是 hash 路由，`question` 是这一页**唯一**要回答的问题。
// 加一页＝加一条；先问自己那句 question 写不写得出来，写不出来说明还没想清楚。

type Page = {
  id: string;
  label: string;
  question: string;
  wired: boolean;
  view: (d: Data) => Kid[];
};

// 总览 —— 读者：所有人 / 老板。要的是「30 秒看完有没有事」，不是名词表。
function renderOverview(d: Data): Kid[] {
  const out: Kid[] = [];
  if (d.err !== "") out.push(banner("bad", "读不到投影", [el("span", {}, d.err)]));
  out.push(...alertBanners(d.issues));
  if (d.board) out.push(statSection(d.board));
  const clean = d.err === "" && d.issues.error === "" && d.issues.fatal.length === 0 && d.issues.warn.length === 0;
  if (clean) out.push(banner("warn", "没有发现", [
    el("span", {}, "校验零红档零非红档 —— 但这只说明"),
    el("strong", {}, "真相源格式没问题"),
    el("span", {}, "，不说明 agent 在跑。那件事要看「运行态」。"),
  ]));
  out.push(notWiredIndex());
  return out;
}

// 组织 —— 读者：管理者 / FDE。组织长什么样、边界在哪。
function renderOrg(d: Data): Kid[] {
  const v = d.board;
  if (!v) return [noBoard(d)];
  return [domainSection(v), roleSection(v), routingSection(v)];
}

// 项目 —— 读者：跟进的人 / 老板。谁在做什么、到几号、卡住找谁。
function renderProjects(d: Data): Kid[] {
  const v = d.board;
  if (!v) return [noBoard(d)];
  return [projectSection(v), progressGap()];
}

function progressGap(): Kid {
  return section("进度：口径还没定", "所以这一页不显示进度 —— 宁缺，不编",
    emptyNote("「进度」是活的、每天变：写进 git 真相源＝每天刷 diff、人手维护必腐烂。" +
      "候选：① 表里留一个指针、渲染时聚合（倾向）；② 完全交给 agent 在数据目录里写；③ 另开一张动态表、不进 git。" +
      "拍板前这里空着 —— 见 SPEC §13 Q15 与 GitHub #36。"));
}

// 运行态三色口径（拍板）：绿 = 近期真回过话；黄 = 运行中 / 这个窗口没观测到；红 = 卡点，需人介入。
const RUN_STATE: Record<string, { dot: string; label: string }> = {
  ok: { dot: "dot ok", label: "绿 · 近期回过话" },
  warn: { dot: "dot warn", label: "黄 · 运行中 / 未观测" },
  fail: { dot: "dot bad", label: "红 · 卡点，需介入" },
};
const runState = (s: string) => RUN_STATE[s] ?? { dot: "dot", label: dash(s) };

// 带颜色的状态点：点 + 文字放进一个 inline-flex，点才有宽度（.dot 是固定宽高）。
const dotLine = (cls: string, ...kids: Kid[]): HTMLElement =>
  el("span", { class: "st" }, el("span", { class: cls }), ...kids);

// 运行态 —— 读者：运维 / FDE。数据源是防假绿探针（与 `anc probe` **同一份判据**，只读磁盘）。
//
// 它的纪律：判活靠**真拨 socket**、判定靠**真回过话**，都不看「进程在不在」——
// 进程活着 / engine started 打着绿字 / 却一条也回不了，这个坑真踩过两次。
// 「现在这一秒能不能回话」的裁判权在**人**：这一页只观测、只报红，不做任何自动处置。
function renderRuntime(d: Data): Kid[] {
  const r = d.runtime;
  if (!r) {
    return [banner("bad", "读不到运行态", [
      el("span", {}, d.runtimeErr === "" ? "服务没回 anc.runtime/v1" : d.runtimeErr),
    ])];
  }
  if (!r.wired) {
    return [banner("warn", "运行态还没接上", [
      el("span", {}, dash(r.error)),
      el("br"),
      el("span", { class: "mono" }, "anc board serve <vault> --data <data 目录>"),
    ])];
  }

  const out: Kid[] = [];
  if ((r.error ?? "") !== "") out.push(banner("bad", "探针跑不动", [el("span", {}, dash(r.error))]));

  const up = r.gateway === "up";
  out.push(section("gateway", "判活 = 真拨一次 socket，不看文件在不在（残留文件会把「挂了」看成「在跑」）",
    el("p", { class: "empty" },
      dotLine(up ? "dot ok" : "dot bad",
        up ? " 在跑（socket 拨得通）" : " 没在跑：" + dash(r.gateway_why)))));

  const rows: Kid[][] = r.bots.map((b) => {
    const st = runState(b.state);
    return [el("span", { class: st.dot }), el("span", { class: "mono" }, dash(b.project)), st.label, b.why];
  });
  out.push(section("每个 bot 能不能回话", "只读磁盘事实（socket + 会话记录），不烧 token",
    r.bots.length === 0
      ? emptyNote("config 里一个 project 都没有 —— 没有可观测的对象。")
      : table(["", "bot", "状态", "依据"], rows)));

  out.push(section("出事了交给谁", "探针不做自动处置 —— 谁去处理、怎么处理，由人定",
    el("p", { class: "empty" },
      r.handlers.length > 0
        ? el("span", {}, "报红交给：", el("strong", {}, r.handlers.join("、")), "（公司 admins）")
        : cannot(dash(r.handler_why)))));

  const extras = r.extras ?? [];
  if (extras.length > 0) {
    out.push(banner("warn", "配置外的残留（" + extras.length + " 条，不是运行态问题，但该清）",
      extras.map((e) => el("span", {}, e))));
  }

  out.push(section("这一页不做什么", "说清楚边界，比多画几个卡片有用",
    el("p", { class: "empty" },
      "不发消息去试（那要烧 token，裁判权也留给人）；不起服务、不重启进程、不清残留 —— ",
      "这里全是只读观测。「现在能不能回话」由人下判断，这一页只保证不给他一个假的绿灯。")));
  return out;
}

// 数据流 —— 读者：管理者 / 老板。这一页现在只有**策略面**：
//   ① 执行面（gateway 实际吃的那份 config.toml）里通道的口径 —— 上游默认开着，所以「没写」也是事实；
//   ② 真相源算出来的「每个 bot 手里有什么」。
// 事件面（谁尝试连了哪、有没有越权尝试）没有数据源，所以在页尾如实列缺 —— 不画通道图充数。
function renderDataflow(d: Data): Kid[] {
  const v = d.dataflow;
  if (!v) {
    return [banner("bad", "读不到数据流观测", [
      el("span", {}, d.dataflowErr === "" ? "服务没回 anc.dataflow/v1" : d.dataflowErr),
    ])];
  }
  const out: Kid[] = [];
  if ((v.error ?? "") !== "") out.push(banner("bad", "真相源读不动", [el("span", {}, dash(v.error))]));

  const rl = v.relay;
  const open = !rl.declared || rl.timeout_secs !== 0;
  out.push(section("bot 之间的通道（relay）",
    "读的是**执行面** —— 我们打算让它吃什么不算数，它真吃着什么才算",
    el("p", { class: "empty" }, dotLine(open ? "dot bad" : "dot ok",
      open ? " 通道开着" : " 通道关着（v1 口径：机制保留、默认零绑定）")),
    el("p", { class: "empty" }, dash(rl.note))));

  if (v.gaps.length > 0) {
    out.push(section("结构性风险", "与 `anc render --check` 同一套判据（渲染器里的 SecurityGaps）",
      el("ul", {}, ...v.gaps.map((g) => el("li", {}, g)))));
  } else if (v.exec.config_present) {
    out.push(section("结构性风险", "与 `anc render --check` 同一套判据（渲染器里的 SecurityGaps）",
      el("p", { class: "empty" },
        "该显式声明的安全段都在，这条判据没发现结构性缺口。",
        cannot(" —— 这只说明 config 写对了，不说明没人越权（那要有事件面）。"))));
  }

  const rows: Kid[][] = v.bots.map((b) => [
    el("span", { class: "mono" }, b.project),
    dash(b.role),
    el("span", { class: "mono" }, dash(b.mode)),
    dash(b.model),
    b.tools.length === 0 ? cannot("空（没预授权）") : el("span", { class: "mono" }, b.tools.join(", ")),
    b.inbound === 0
      ? cannot("没配")
      : String(b.inbound) + " 人" + (b.inbound_extra > 0 ? "（另放行 " + b.inbound_extra + "）" : ""),
  ]);
  out.push(section("每个 bot 手里有什么", "真相源算出来的（渲染器的输入）—— 入站只出人数，不出标识符",
    v.bots.length === 0
      ? emptyNote("没有启用的成员 —— 没有可授权的对象。")
      : table(["bot", "角色", "工具档", "模型", "harness 白名单", "入站授权"], rows)));

  out.push(execSection(v));
  out.push(section("这一页现在缺什么", "如实列 —— 缺的部分不许用「图好看」补上",
    v.missing.length === 0
      ? emptyNote("没有已知缺口。")
      : el("ul", {}, ...v.missing.map((m) => el("li", {}, m)))));
  return out;
}

// 执行面那一块：这份 config 是不是渲染产物、按哪份真相源生成的、里头有哪几个 project。
function execSection(v: DataflowView): Kid {
  const e = v.exec;
  if (!e.config_present) {
    return section("执行面", "gateway 实际吃的那份 config.toml",
      el("p", { class: "empty" }, cannot(dash(e.note))));
  }
  const rows: Array<[string, Kid]> = [
    ["指纹", e.has_fingerprint
      ? el("span", { class: "mono" }, (e.version ?? "") + " · inputs=" + (e.inputs ?? ""))
      : cannot("没有 anc 指纹 —— 手写或他源配置")],
    ["生成于", e.has_fingerprint ? localTime(e.generated_at ?? "") : "—"],
    ["project", e.projects.length === 0
      ? cannot("一个都没扫到")
      : el("span", { class: "mono" }, e.projects.join(", "))],
  ];
  return section("执行面", "gateway 实际吃的那份 config.toml —— 策略面说得再好，也得它真吃上了才算",
    card("这份 config 是谁", "", rows),
    e.note ? el("p", { class: "empty" }, cannot(dash(e.note))) : "");
}

// 原料 —— 读者：所有人。这一页给的是**原料**：数据目录里现在有什么、多久没动过。
// 「有目录 / 有文件」离「有资产」差着一整层，所以标题、口径、页脚都写「原料」，不写「沉淀」。
function renderAssets(d: Data): Kid[] {
  const v = d.assets;
  if (!v) {
    return [banner("bad", "读不到原料清单", [
      el("span", {}, d.assetsErr === "" ? "服务没回 anc.assets/v1" : d.assetsErr),
    ])];
  }
  const out: Kid[] = [];
  if ((v.error ?? "") !== "") out.push(banner("bad", "真相源读不动", [el("span", {}, dash(v.error))]));
  out.push(banner("warn", "这是原料，不是沉淀", [el("span", {}, dash(v.note))]));

  const total = v.dirs.reduce((n, x) => n + x.files, 0);
  out.push(section("规模", "真相源里那张「数据路由」表的一行一行，加上它底下现在有多少东西",
    el("div", { class: "grid stats" },
      statCard(String(v.dirs.length), "数据目录", "顶层"),
      statCard(String(total), "原料文件", "全部加起来"),
      statCard(String(v.charters), "立项书副本", "真源在客户侧"))));

  const dirRows: Kid[][] = v.dirs.map((x) => [
    el("span", { class: "mono" }, x.dir),
    dash(x.summary),
    String(x.files) + (x.truncated ? "（只数到这里）" : ""),
    humanBytes(x.bytes),
    x.newest ? localTime(x.newest) : cannot("没有文件"),
  ]);
  out.push(section("每个目录里有什么", "空目录照报 0 —— 「还没开始沉淀」是个真实状态，不是错误",
    v.dirs.length === 0
      ? emptyNote("真相源里还没有数据目录（`anc org init` 之后是一个都没有）。")
      : table(["目录", "说明（真相源）", "文件", "体积", "最近变动"], dirRows)));

  const flat: Array<{ path: string; dir: string; bytes: number; modified: string }> = [];
  for (const x of v.dirs) for (const f of x.recent) flat.push({ path: f.path, dir: x.dir, bytes: f.bytes, modified: f.modified });
  flat.sort((a, b) => (a.modified === b.modified ? (a.path < b.path ? -1 : 1) : (a.modified < b.modified ? 1 : -1)));
  const top = flat.slice(0, 10);
  out.push(section("最近动的文件", "每个目录各取最近 5 个，再合起来取前 10 —— 只列相对路径",
    top.length === 0
      ? emptyNote("所有数据目录都是空的。")
      : table(["文件", "目录", "体积", "时间"],
        top.map((f) => [el("span", { class: "mono" }, f.path), f.dir, humanBytes(f.bytes), localTime(f.modified)]))));

  out.push(section("这一页不做什么", "说清楚边界，比多画几个卡片有用",
    el("p", { class: "empty" },
      "不把「有文件」说成「有资产」；不给产出打「能不能复用」的分（那要有判据，见议题 #26 / #27）；",
      "只读 —— 没有任何写入口，也不动这些文件。")));
  return out;
}

const overviewPage: Page = {
  id: "overview", label: "总览", wired: true,
  question: "现在有没有事？", view: renderOverview,
};
const orgPage: Page = {
  id: "org", label: "组织", wired: true,
  question: "公司长什么样、边界在哪？", view: renderOrg,
};
const projectsPage: Page = {
  id: "projects", label: "项目", wired: true,
  question: "谁在做什么、到几号、卡住找谁？", view: renderProjects,
};
const runtimePage: Page = {
  id: "runtime", label: "运行态", wired: true,
  question: "每个 bot 活着吗、真的能回话吗？", view: renderRuntime,
};
const dataflowPage: Page = {
  id: "dataflow", label: "数据流", wired: true,
  question: "谁可以做什么、通道开着吗？", view: renderDataflow,
};
// 页名还叫「沉淀」（那是这一块最终要回答的），但这一页现在只答得了「原料有哪些」——
// 所以问题句按**现在真答得了**的写，不按以后的写。口径差在哪，页内写清了。
const assetsPage: Page = {
  id: "assets", label: "沉淀", wired: true,
  question: "原料有哪些、多久没动了？", view: renderAssets,
};

const PAGES: Page[] = [overviewPage, orgPage, projectsPage, runtimePage, dataflowPage, assetsPage];

function pageFor(id: string): Page {
  for (const p of PAGES) if (p.id === id) return p;
  return overviewPage;
}

function currentId(): string {
  return location.hash.replace(/^#\/?/, "").trim();
}

// ---------- 外壳与路由 ----------

function refreshButton(): HTMLElement {
  const b = el("button", { class: "refresh" }, "刷新");
  b.addEventListener("click", () => { void boot(true); });
  return b;
}

function topbar(d: Data | null, page: Page): HTMLElement {
  const i = d ? d.issues : null;
  const meta = el("div", { class: "meta" },
    i ? el("span", { class: "chip" }, el("span", { class: statusDot(i) }), statusText(i)) : "",
    d && d.board ? chip("生成 " + localTime(d.board.generated_at)) : "",
    d && d.board ? chip("schema " + d.board.schema, true) : "",
    refreshButton());
  return el("header", { class: "top glass" },
    el("div", { class: "q" }, el("h1", {}, page.label), el("p", { class: "ask" }, page.question)),
    el("div", { class: "spacer" }),
    meta);
}

function rail(page: Page, d: Data | null): HTMLElement {
  const c = d && d.board ? d.board.company : null;
  const brand = el("div", { class: "brandbox" },
    el("div", { class: "mark" }, "ANC"),
    el("div", { class: "co" }, c ? dash(c.name) : "还没读到投影"),
    el("div", { class: "sub" },
      c ? dash(c.platform) + " · " + dash(c.language) + " · " + dash(c.timezone) : "—"));
  const tabs = el("nav", { class: "tabs" });
  for (const p of PAGES) {
    tabs.append(el("a", { class: p.id === page.id ? "tab active" : "tab", href: "#/" + p.id },
      el("span", { class: "lab" }, p.label),
      el("span", { class: p.wired ? "res on" : "res off" }, p.wired ? "已接入" : "未接入")));
  }
  return el("aside", { class: "rail glass" }, brand, tabs);
}

function footer(): HTMLElement {
  return el("footer", {},
    el("div", {}, el("strong", {}, "本页只读"),
      "：数据是真相源（vault）的实时投影，没有任何写入口；点「刷新」重取。"),
    el("div", {}, el("strong", {}, "校验发现"), " 与 ", el("span", { class: "mono" }, "anc org check"),
      " 同源：红档拦住落盘，非红档只回显；完整清单在「总览」。"),
    el("div", {}, el("strong", {}, "分页口径"),
      "：一页回答一个问题。还没接数据源的页如实写明缺什么、依赖哪个议题，不编数据。"));
}

function shell(): void {
  if (!root) return;
  const page = pageFor(currentId());
  const d = cache;
  const col = el("div", { class: "col" });
  col.append(topbar(d, page));
  if (!d) {
    col.append(el("div", { class: "glass pad loading" }, "正在读取真相源…"));
  } else {
    for (const kid of page.view(d)) col.append(kid);
    col.append(footer());
  }
  document.title = page.label + " · " + (d && d.board ? dash(d.board.company.name) : "ANC 看板");
  root.replaceChildren(el("div", { class: "shell" }, rail(page, d), col));
}

let cache: Data | null = null;

async function boot(force = false): Promise<void> {
  if (force) cache = null;
  if (cache === null) {
    shell();
    cache = await fetchData();
  }
  shell();
}

window.addEventListener("hashchange", () => { shell(); });

void boot();

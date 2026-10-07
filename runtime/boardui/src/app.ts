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

// 一次取数、全程共用：已接入的页吃的是同一份投影，切页不重新打网络（按「刷新」才重取）。
type Data = { board: BoardView | null; issues: Issues; err: string };

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

async function fetchData(): Promise<Data> {
  const [boardRes, issuesRes] = await Promise.all([
    getJSON<BoardView & { error?: string }>("/api/board"),
    getJSON<Issues>("/api/issues"),
  ]);
  const issues = issuesRes.body ?? emptyIssues(issuesRes.err);
  const schema = boardRes.body?.schema;
  if (boardRes.body && typeof schema === "string" && schema !== "") {
    return { board: boardRes.body, issues, err: "" };
  }
  const why = boardRes.body?.error ?? boardRes.err ?? ("看板数据读取失败（HTTP " + boardRes.status + "）");
  return { board: null, issues, err: why };
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
    [linkTo("runtime"), "每个 bot 活着吗、真的能回话吗？", "防假绿探针（阶段 C）"],
    [linkTo("dataflow"), "谁授权了谁、通道开着吗、有没有越权？", "通道表 / 授权表 / 审计流水"],
    [linkTo("assets"), "沉淀了什么、哪些能复用？", "agent 每日产出"],
  ];
  return section("三块还没接数据源", "各自一页，如实写明缺什么 —— 不编数据填界面",
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

// 运行态 —— 读者：运维 / FDE。数据源是阶段 C 的探针，还没有。
function renderRuntime(d: Data): Kid[] {
  const roles = d.board ? d.board.roles.length : 0;
  return [
    section("还没接入", "这一页不编数据 —— 把缺口写清楚，比画一个假的绿灯有用",
      el("div", { class: "grid" },
        card("这一页要什么数据源", "", [
          ["是什么", "防假绿探针：服务在册（launchd / systemd）+ 进程存在 + 日志里的功能级就绪标志，三重交叉"],
          ["为什么", "进程活 ≠ 能回话。只看进程会出假绿"],
          ["依赖", el("span", { class: "mono" }, "GitHub #17、#23；阶段 C")],
        ]),
        card("现在能说的 / 不能说的", "", [
          ["真源里定义了角色", roles + " 个"],
          ["它们是不是活着", cannot("判断不了 —— 不把「定义了」当成「在跑」")],
        ]))),
  ];
}

// 数据流 —— 读者：管理者 / 老板。数据源是授权表 + 审计，代码零实现。
function renderDataflow(d: Data): Kid[] {
  const dirs = d.board ? d.board.routing.length : 0;
  return [
    section("还没接入", "这一页不编数据 —— 把缺口写清楚，比画一个假的通道图有用",
      el("div", { class: "grid" },
        card("这一页要什么数据源", "", [
          ["是什么", "通道表 + 授权表（grant）+ 审计流水：谁授权给谁、TTL 到没到期、降级后还剩什么"],
          ["依赖", el("span", { class: "mono" }, "GitHub #32、#33、#34、#35")],
        ]),
        card("现在能说的 / 不能说的", "", [
          ["真源里的静态归属", dirs + " 个顶层数据目录（在「组织」页）"],
          ["那是不是授权", cannot("不是 —— 岗位定义 ≠ 授权，本页不加戏")],
        ]))),
  ];
}

// 沉淀 —— 读者：所有人。数据源是 agent 每日产出，还没有。
function renderAssets(_d: Data): Kid[] {
  return [
    section("还没接入", "这一页不编数据 —— 把缺口写清楚，比列一堆空目录有用",
      el("div", { class: "grid" },
        card("这一页要什么数据源", "", [
          ["是什么", "沉淀层：agent 每天产出的条目、来源、还能不能复用"],
          ["依赖", el("span", { class: "mono" }, "GitHub #26、#27")],
        ]),
        card("现在能说的 / 不能说的", "", [
          ["真源里有目录表", "顶层数据目录的名字与说明（在「组织」页）"],
          ["目录里有什么", cannot("不知道 —— 目录 ≠ 沉淀，不把「有目录」当成「有资产」")],
        ]))),
  ];
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
  id: "runtime", label: "运行态", wired: false,
  question: "每个 bot 活着吗、真的能回话吗？", view: renderRuntime,
};
const dataflowPage: Page = {
  id: "dataflow", label: "数据流", wired: false,
  question: "谁授权了谁、通道开着吗、有没有越权？", view: renderDataflow,
};
const assetsPage: Page = {
  id: "assets", label: "沉淀", wired: false,
  question: "沉淀了什么、哪些能复用？", view: renderAssets,
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

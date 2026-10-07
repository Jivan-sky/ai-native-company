// ANC 看板前端（浏览器侧）。
//
// 它只做一件事：把 `/api/board`（anc.board/v1 投影）与 `/api/issues`（真相源健康度）
// 摆成人能看的界面。
//
// 三条纪律：
//   1. 真相源里的字一律走 textContent，绝不 innerHTML —— 看板不许成为注入点；
//   2. 不猜、不补：哪块没有数据源就写清楚「还没接入」，不编假数据把界面填满；
//   3. 只读：这个页面没有任何写操作（后端也只放行 GET / HEAD）。
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

type Attrs = Record<string, string>;
type Kid = Node | string;

const root = document.getElementById("app");

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
  return el("table", {}, el("thead", {}, head), body);
}

function section(title: string, hint: string, ...body: Kid[]): HTMLElement {
  const h = el("h2", {}, title);
  if (hint !== "") h.append(el("span", { class: "hint" }, hint));
  return el("section", { class: "glass" }, h, ...body);
}

function banner(kind: "bad" | "warn", title: string, items: Kid[]): HTMLElement {
  const ul = el("ul");
  for (const i of items) ul.append(el("li", {}, i));
  return el("div", { class: `banner ${kind}` }, el("h3", {}, title), ul);
}

const emptyNote = (s: string): HTMLElement => el("p", { class: "empty" }, s);

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
    where === "" ? f.msg : `${where}：${f.msg}`);
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
  if (i.fatal.length > 0) return `红档 ${i.fatal.length} 条`;
  if (i.warn.length > 0) return `非红档 ${i.warn.length} 条`;
  return "校验干净";
}

function header(v: BoardView | null, i: Issues): HTMLElement {
  const c = v?.company;
  const brand = el("div", { class: "brand" },
    el("h1", {}, c ? dash(c.name) : "ANC 看板"),
    el("div", { class: "sub" }, c
      ? `${dash(c.platform)} · ${dash(c.language)} · ${dash(c.timezone)} · id=${dash(c.id)}`
      : "还没有读到投影"));
  const refresh = el("button", { class: "refresh" }, "刷新");
  refresh.addEventListener("click", () => { void load(); });
  const meta = el("div", { class: "meta" },
    el("span", { class: "chip" }, el("span", { class: statusDot(i) }), statusText(i)),
    v ? chip(`schema ${v.schema}`, true) : "",
    v ? chip(`生成 ${localTime(v.generated_at)}`) : "",
    refresh);
  return el("header", { class: "top glass" }, brand, el("div", { class: "spacer" }), meta);
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
      `${dash(m.display_name)}${m.disabled ? "（已停用）" : ""}${m.admin ? "（管理员）" : ""}`).join("、");
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

function footer(): HTMLElement {
  return el("footer", {},
    el("div", {}, el("strong", {}, "本页只读"), "：数据是真相源（vault）的实时投影，没有任何写入口；刷新即最新。"),
    el("div", {}, el("strong", {}, "还没接入"), "：运行态（哪个 bot 在跑）、数据流管控（跨域 / 通道授权）、"
      + "沉淀区（agent 每天产出的可用资产）—— 这三块的数据源是阶段 C 的探针，现在还没有，所以这里不显示（也不编）。"),
    el("div", {}, el("strong", {}, "校验发现"), " 与 ", el("span", { class: "mono" }, "anc org check"),
      " 同源：红档拦住落盘，非红档只回显。"));
}

function compose(v: BoardView | null, i: Issues, err: string): Kid[] {
  const out: Kid[] = [header(v, i)];
  if (err !== "") out.push(banner("bad", "读不到投影", [el("span", {}, err)]));
  if (i.fatal.length > 0) {
    out.push(banner("bad", `红档 ${i.fatal.length} 条 —— 拦住落盘，先修它`, i.fatal.map(findingLine)));
  } else if (i.error !== "") {
    out.push(banner("bad", "校验状态读不了", [el("span", {}, i.error)]));
  }
  if (i.warn.length > 0) {
    out.push(banner("warn", `非红档 ${i.warn.length} 条（不拦，但请过目）`, i.warn.map(findingLine)));
  }
  if (v) {
    out.push(domainSection(v), projectSection(v), roleSection(v), routingSection(v));
  }
  out.push(footer());
  return out;
}

async function getJSON<T>(url: string): Promise<{ status: number; body: T | null; err: string }> {
  try {
    const res = await fetch(url, { headers: { accept: "application/json" } });
    const raw = await res.text();
    try {
      return { status: res.status, body: JSON.parse(raw) as T, err: "" };
    } catch {
      return { status: res.status, body: null, err: `返回的不是 JSON（HTTP ${res.status}）` };
    }
  } catch (e) {
    return { status: 0, body: null, err: `连不上看板服务：${e instanceof Error ? e.message : String(e)}` };
  }
}

const emptyIssues = (err: string): Issues => ({ ok: false, fatal: [], warn: [], error: err });

async function load(): Promise<void> {
  if (!root) return;
  root.replaceChildren(el("div", { class: "loading" }, "正在读取真相源…"));

  const [boardRes, issuesRes] = await Promise.all([
    getJSON<BoardView & { error?: string }>("/api/board"),
    getJSON<Issues>("/api/issues"),
  ]);

  const issues = issuesRes.body ?? emptyIssues(issuesRes.err);
  const schema = boardRes.body?.schema;
  if (boardRes.body && typeof schema === "string" && schema !== "") {
    root.replaceChildren(...compose(boardRes.body, issues, ""));
    return;
  }
  const why = boardRes.body?.error ?? boardRes.err ?? `看板数据读取失败（HTTP ${boardRes.status}）`;
  root.replaceChildren(...compose(null, issues, why));
}

void load();

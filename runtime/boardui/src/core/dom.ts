import type { Attrs, Kid } from "./types";

export const root = document.getElementById("app");
// ---------- 画界面的最小零件 ----------

export function el(tag: string, attrs: Attrs = {}, ...kids: Kid[]): HTMLElement {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") node.className = v;
    else node.setAttribute(k, v);
  }
  for (const kid of kids) node.append(kid);
  return node;
}

export const dash = (v: string | null | undefined): string => {
  const s = (v ?? "").trim();
  return s === "" ? "—" : s;
};

export const chip = (label: string, mono = false): HTMLElement =>
  el("span", { class: mono ? "chip mono" : "chip" }, label);

export function card(title: string, badge: string, rows: Array<[string, Kid]>): HTMLElement {
  const head = el("h3", {}, title);
  if (badge !== "") head.append(el("span", { class: "chip mono" }, badge));
  const dl = el("dl");
  for (const [k, v] of rows) dl.append(el("dt", {}, k), el("dd", {}, v));
  return el("div", { class: "card" }, head, dl);
}

export function table(headers: string[], rows: Kid[][]): HTMLElement {
  const head = el("tr");
  for (const h of headers) head.append(el("th", {}, h));
  const body = el("tbody");
  for (const r of rows) {
    const tr = el("tr");
    // 每个格子带上表头当作 data-label：窄屏把表拆成一行一行的「标签：值」，
    // 没有它，拆开以后那一列是谁的值就没人知道了。
    r.forEach((c, i) => {
      const td = el("td", {}, c);
      if (headers[i] !== undefined) td.dataset.label = headers[i];
      tr.append(td);
    });
    body.append(tr);
  }
  // 套一层滚动容器：窄屏（比如嵌在侧栏里）表格不该把整页撑破。
  // .vt 只管度量（5 档间距/字号、等宽数字），底色仍走主题变量。
  return el("div", { class: "tw vt" }, el("table", {}, el("thead", {}, head), body));
}

// hint 现在允许是节点：有些「标题右边那一行」本身就是可点的数字（比如审计总条数）。
export function section(title: string, hint: Kid, ...body: Kid[]): HTMLElement {
  const h = el("h2", {}, title);
  if (hint !== "" && hint !== null && hint !== undefined) h.append(el("span", { class: "hint" }, hint));
  return el("section", { class: "sec" }, h, ...body);
}

export function banner(kind: "bad" | "warn", title: string, items: Kid[]): HTMLElement {
  const ul = el("ul");
  for (const i of items) {
    // 调用方用 <br> 分隔「标题句 / 补充句」；它不该变成一个空的项目符号。
    if (i instanceof HTMLElement && i.tagName === "BR") continue;
    ul.append(el("li", {}, i));
  }
  return el("div", { class: "banner " + kind }, el("h3", {}, title), ul);
}

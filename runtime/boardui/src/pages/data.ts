import type { CatalogView, Data, DSRow, Kid } from "../core/types";
import { banner, chip, el, section, table } from "../core/dom";
import { emptyNote } from "../ui/blocks";

// 「数据」页：数据源 → 目录 → 分级/策略；右边可以直接跑一条只读查询。
//
// 查询票由看板自己签（`/api/chat/session` 顺带返回 query_token），主体是 `board-ui`；
// 策略仍然按列判、按行过滤，和 CLI 走同一个执行器。

function dsCard(d: DSRow, active: boolean): HTMLElement {
  const meta = [
    d.type,
    `${d.tables ?? 0} 表 / ${d.columns ?? 0} 列`,
    d.classified ? `已拍板 ${d.classified}` : "未分级",
    d.has_policy ? "有策略" : "无策略（全拒）",
  ].join(" · ");
  return el("a", {
    class: "ds-card" + (active ? " active" : ""),
    href: `#data?ds=${encodeURIComponent(d.name)}`,
  },
    el("div", { class: "ds-name" }, d.name, " ", d.read_only ? chip("只读", true) : chip("可写", true)),
    el("div", { class: "ds-meta" }, meta),
  );
}

function catalogTable(v: CatalogView): Kid {
  if (!v.tables || v.tables.length === 0) return emptyNote("目录是空的：先跑 `anc catalog scan`。");
  return el("div", {},
    ...v.tables.map((t) => el("div", { class: "cat-table" },
      el("div", { class: "cat-head" }, t.name, t.comment ? el("span", { class: "empty" }, " — " + t.comment) : ""),
      table(["列", "类型", "可空", "主键", "分级"],
        t.columns.map((c) => {
          const cls = v.classifications?.[`${t.name}.${c.name}`];
          return [
            el("span", { class: "mono" }, c.name),
            el("span", { class: "mono" }, c.type),
            c.nullable ? "NULL" : "NOT NULL",
            c.is_pk ? "PK" : "—",
            cls ? chip(cls.level, true) : el("span", { class: "empty" }, "—"),
          ];
        })),
    )),
  );
}

function policyBlock(v: CatalogView): Kid {
  const p = v.policy;
  if (!p) return emptyNote("没有策略文件：默认全部拒绝（fail-closed）。");
  const rules = (p.rules ?? []).map((r) =>
    el("li", {}, `${r.subject} → ${r.table}.${r.columns.join(",")}：${r.action}`));
  const rows = (p.row_filters ?? []).map((f) => el("li", {}, `行过滤 ${f.table}: ${f.where}`));
  return el("div", {},
    el("div", { class: "empty" }, `default=${p.default} · derived=${p.derived || "deny"}`),
    el("ul", {}, ...rules, ...rows));
}

export function renderData(d: Data): Kid[] {
  const v = d.ds;
  if (!v) {
    return [banner("bad", "读不到数据源", [el("span", {}, d.dsErr || "服务没回 anc.datasources/v1")])];
  }
  if (!v.wired) {
    return [banner("warn", "数据源目录没接上", [el("span", {}, v.why ?? "")])];
  }
  const out: Kid[] = [];
  const active = d.cat?.datasource ?? "";
  out.push(section("数据源", "只连不搬：这里只有定义与目录，数据仍在客户库里",
    v.datasources.length === 0
      ? emptyNote("还没有数据源：在 `<vault>/../datasources/` 放一份定义。")
      : el("div", { class: "ds-list" }, ...v.datasources.map((x) => dsCard(x, x.name === active)))));

  if (d.cat && d.cat.wired) {
    out.push(section(`目录：${d.cat.datasource}`,
      `指纹 ${d.cat.fingerprint ?? "—"} · 扫于 ${d.cat.scanned_at ?? "—"}`,
      catalogTable(d.cat)));
    out.push(section("策略", "目录与执行读同一份策略", policyBlock(d.cat)));
    out.push(section("查询", "走同一道闸：列级策略 + 行过滤 + 额度都生效",
      el("div", { id: "data-query", class: "query-box" },
        el("textarea", { class: "query-input", rows: "2", placeholder: "SELECT …（只读）" }),
        el("button", { class: "query-run" }, "运行"),
        el("div", { class: "query-out" }, ""),
      )));
  } else if (d.cat) {
    out.push(banner("warn", "这个数据源还没有目录", [el("span", {}, d.cat.why ?? "")]));
  }
  return out;
}

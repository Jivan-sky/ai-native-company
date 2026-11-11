import type { CatalogView, Data, DSRow, Kid } from "../core/types";
import { banner, chip, el, section, table } from "../core/dom";
import { localTime, shortTime } from "../core/format";
import { emptyNote } from "../ui/blocks";
import { currentRole, visible } from "../core/roles";

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

function fileBrowser(d: Data): Kid {
  const snap = d.fileSnap;
  const list = d.fileList;
  if (!snap || !snap.wired) {
    return banner("warn", "这个数据源还没有快照", [el("span", {}, snap?.why ?? "先跑 `anc file scan`")]);
  }
  const rows: Kid[][] = [];
  for (const e of list?.entries ?? []) {
    const label = e.dir ? e.name + "/" : e.name;
    const href = e.dir
      ? `#data?ds=${encodeURIComponent(snap.datasource)}&p=${encodeURIComponent(e.path)}`
      : `#data?ds=${encodeURIComponent(snap.datasource)}&p=${encodeURIComponent(d.filePath)}`;
    const cell = el("a", { class: "mono", href, "data-file": e.dir ? "" : e.path }, label);
    rows.push([cell, e.dir ? "目录" : `${e.size} B`,
      el("span", { class: "mono nw", title: localTime(e.mtime ?? "") }, shortTime(e.mtime ?? ""))]);
  }
  const crumb = el("div", { class: "empty" }, `路径：${d.filePath === "" ? "/" : "/" + d.filePath}`);
  return el("div", {},
    el("div", { class: "empty" },
      "扫于 " + ((snap.scanned_at ?? "") === "" ? "—" : shortTime(snap.scanned_at ?? "")) +
      " · 指纹 " + (snap.fingerprint ?? "").slice(0, 12) +
      ` · ${snap.files.length} 个文件 / ${snap.dirs.length} 个目录`),
    crumb,
    (d.fileErr ?? "") !== ""
      ? banner("warn", "列目录失败", [el("span", {}, d.fileErr)])
      : el("div", {},
          table(["名字", "大小/类型", "修改时间"], rows),
          (list?.hidden ?? 0) > 0
            ? el("div", { class: "empty" }, `另有 ${list?.hidden} 项被策略隐藏（不是不存在）`)
            : ""),
    el("pre", { id: "file-content", class: "file-content" }, "点一个文件看内容"),
  );
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
  const active = d.cat?.datasource ?? d.fileSnap?.datasource ?? "";
  out.push(section("数据源", "",
    v.datasources.length === 0
      ? emptyNote("还没有数据源：在 `<vault>/../datasources/` 放一份定义。")
      : el("div", { class: "ds-list" }, ...v.datasources.map((x) => dsCard(x, x.name === active)))));

  if (active !== "" && v.datasources.some((x) => x.name === active && x.type === "file")) {
    out.push(section(`文件目录：${active}`, "只读元数据 · 文件内容按策略放行", fileBrowser(d)));
    return out;
  }

  if (d.cat && d.cat.wired) {
    out.push(section(`目录：${d.cat.datasource}`,
      "扫于 " + ((d.cat.scanned_at ?? "") === "" ? "—" : shortTime(d.cat.scanned_at ?? "")) +
        " · 指纹 " + (d.cat.fingerprint ?? "").slice(0, 12),
      catalogTable(d.cat)));
    // 段落级门禁：成员能看目录（哪些表、哪几列），看不到策略规则本身。
    if (visible(currentRole(), "data.policy")) out.push(section("策略", "", policyBlock(d.cat)));
    out.push(section("查询", "只读 · 列策略 + 行过滤仍然生效",
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

import type { Data, Kid, ModelRow } from "../core/types";
import { banner, chip, el, section, table } from "../core/dom";
import { emptyNote } from "../ui/blocks";

// 「模型」页：注册表 + 谁绑了它。**密钥只以名字与「配没配」出现** —— 连尾巴四位都不给。

function rowCell(m: ModelRow): Kid[] {
  return [
    el("span", { class: "mono nw" }, m.id),
    el("span", { class: "mono nw" }, m.provider),
    el("span", { class: "mono nw" }, m.model),
    el("span", { class: "mono" }, m.secret),
    m.configured ? chip("已配", true) : chip("未配", true),
    chip(m.status === "disabled" ? "停用" : "启用", true),
    (m.apps ?? []).length === 0
      ? el("span", { class: "empty" }, "—")
      : el("span", {}, (m.apps ?? []).join("、")),
  ];
}

export function renderModels(d: Data): Kid[] {
  const v = d.models;
  if (!v) {
    return [banner("bad", "读不到模型注册表", [
      el("span", {}, d.modelsErr === "" ? "服务没回 anc.models/v1" : d.modelsErr),
    ])];
  }
  if (!v.wired) {
    return [banner("warn", "注册表没接上", [el("span", {}, v.why ?? "")])];
  }
  const out: Kid[] = [];
  out.push(section("注册表", `共 ${v.entries.length} 条`,
    v.entries.length === 0
      ? emptyNote("注册表是空的：写一份 registry.json 放在 vault 旁边。")
      : table(["注册名", "提供方", "模型", "密钥（名字）", "密钥状态", "状态", "被谁绑定"],
          v.entries.map((m) => rowCell(m)))));

  const missing = v.entries.filter((m) => !m.configured).length;
  if (missing > 0) {
    out.push(banner("warn", `有 ${missing} 条还没配密钥`, [
      el("span", {}, "它们被调用时会明确失败 —— 不会静默用别的 key 顶上。"),
    ]));
  }
  return out;
}

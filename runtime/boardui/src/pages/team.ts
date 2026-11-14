import type { Kid } from "../core/types";
import { el, section } from "../core/dom";
import { PAGES } from "../router";
import { ROLES, allowed, currentRole, setRole, type Role } from "../core/roles";

// 设置 / 团队 —— 回答一个到现在都没答案的问题：**「超管到底能做什么，管理员呢？」**
//
// 这一页不新造权限：角色表、页面可见性、当前身份全部读现成的（core/roles.ts）。
// 图是**算出来的**（对每一页问 allowed(role, page)），不是手写的表 ——
// 手写的那份三个月后一定和代码不一致，而"角色能干什么"恰恰是最不能漂的东西。
//
// 后端还没有鉴权（角色存在这台浏览器里）。这句话必须写在这一页上，
// 否则读者会把这张表当成真实权限 —— 那是比没有权限更危险的错觉。

export function renderTeam(): Kid[] {
  const out: Kid[] = [];
  const me = currentRole();

  out.push(section("当前身份", "只决定这台浏览器怎么显示，不改变任何真实权限",
    el("p", { class: "quiet" },
      "看板现在没有登录。左上角那个下拉只影响**你看到哪几页**——它不发给后端，也不拦任何接口。",
      "真身份接上之后，这张表不用重写：把「从下拉取」换成「从会话取」，判据本身不动。"),
    el("div", { class: "roles" },
      ...ROLES.map((r) => {
        const on = r.id === me;
        const b = el("button", {
          class: "role-card" + (on ? " on" : ""), type: "button",
          "aria-pressed": on ? "true" : "false",
        },
          el("b", {}, r.label),
          el("span", { class: "note" }, r.note),
          on ? el("span", { class: "badge" }, "当前") : "");
        b.addEventListener("click", () => { setRole(r.id as Role); location.reload(); });
        return b;
      }))));

  // 权限矩阵：行是页面，列是四个角色。格子是算出来的。
  const head = el("tr", {},
    el("th", {}, "页面"),
    ...ROLES.map((r) => el("th", { class: "c" }, r.label)));
  const rows = PAGES.map((p) => el("tr", {},
    el("td", {}, p.group === p.label ? p.label : p.group + " · " + p.label),
    ...ROLES.map((r) => el("td", { class: "c" },
      allowed(r.id as Role, p.id)
        ? el("span", { class: "yes", title: "能看" }, "✓")
        : el("span", { class: "no", title: "看不到" }, "—")))));
  out.push(section("谁能看哪一页", "对每一页实时算的，不是手写的表",
    el("table", { class: "matrix" }, el("thead", {}, head), el("tbody", {}, ...rows))));

  // 角色说明：从 ROLES 读，不另写一份
  out.push(section("四个角色分别是什么", "改这里等于改权限，所以它只有一处定义",
    el("div", { class: "v2" },
      el("div", { class: "card" },
        ...ROLES.map((r) => el("div", { class: "row" },
          el("div", { class: "what" }, el("b", {}, r.label)),
          el("div", { class: "meta" }, r.note)))))));

  out.push(section("这一页还缺什么", "如实写，不藏",
    el("ul", { class: "gap-list" },
      el("li", {}, "角色还是这台浏览器的事 —— 换台机器就变了。等真身份。"),
      el("li", {}, "「谁能干什么动作」（签发授权、撤回、改配置）现在只写在能力表里，还没有界面。"),
      el("li", {}, "成员的增删、岗位调整还没有入口，只能改 vault 里的文件。"))));

  return out;
}

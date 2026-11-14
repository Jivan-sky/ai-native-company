import type { Kid } from "../core/types";
import { el } from "../core/dom";
import { reload } from "../core/refresh";

// 三种「没有内容」的状态，共用一套骨架：
//
//   empty    现在是空的（正常状态，只是还没发生）
//   unknown  读不到（不是空，是看不见 —— 这两者混在一起是最坏的一种假绿）
//   denied   没有权限（不是空，是不给看）
//
// 每种都必须回答两件事：**这是什么状态**、**下一步做什么 / 找谁**。
// 只写「暂无数据」不算话术 —— 它跟「坏了」「没权限」长得一模一样，读者分不出来。

export type StateKind = "empty" | "unknown" | "denied";

const WORD: Record<StateKind, string> = {
  empty: "现在是空的",
  unknown: "读不到",
  denied: "没有权限",
};

export function stateBlock(kind: StateKind, title: Kid | string, next: Kid | string): HTMLElement {
  return el("div", { class: "state state-" + kind },
    el("span", { class: "state-word" }, WORD[kind]),
    el("div", { class: "state-body" },
      el("div", { class: "state-title" }, title),
      el("div", { class: "state-next" }, next)));
}

export const emptyState = (title: Kid | string, next: Kid | string): HTMLElement =>
  stateBlock("empty", title, next);
// unknownState 现在自带一个「重试」—— 17 处「答不了」里只有 2 处给了下一步，
// 读者卡在"读不到"上没路可走。改在这一处，等于一次给 17 个错误态补上动作。
//
// 重试 = 静默重新取数（不清空现有内容、不整页重绘），失败就还是这个状态。
export const unknownState = (title: Kid | string, next: Kid | string): HTMLElement => {
  const box = stateBlock("unknown", title, next);
  const body = box.querySelector(".state-next");
  if (body) {
    const again = el("button", { class: "state-retry", type: "button" }, "重试");
    again.addEventListener("click", () => {
      again.setAttribute("disabled", "disabled");
      again.textContent = "重试中…";
      reload(true);
      setTimeout(() => { again.removeAttribute("disabled"); again.textContent = "重试"; }, 1200);
    });
    body.append(el("div", { class: "state-actions" }, again));
  }
  return box;
};
export const deniedState = (title: Kid | string, next: Kid | string): HTMLElement =>
  stateBlock("denied", title, next);

// 一条命令的写法：三态里的「下一步」常常就是一行命令，统一等宽，免得每页各写一遍。
export const cmd = (s: string): HTMLElement => el("span", { class: "mono" }, s);

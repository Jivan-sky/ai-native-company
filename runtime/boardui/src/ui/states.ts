import type { Kid } from "../core/types";
import { el } from "../core/dom";

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
export const unknownState = (title: Kid | string, next: Kid | string): HTMLElement =>
  stateBlock("unknown", title, next);
export const deniedState = (title: Kid | string, next: Kid | string): HTMLElement =>
  stateBlock("denied", title, next);

// 一条命令的写法：三态里的「下一步」常常就是一行命令，统一等宽，免得每页各写一遍。
export const cmd = (s: string): HTMLElement => el("span", { class: "mono" }, s);

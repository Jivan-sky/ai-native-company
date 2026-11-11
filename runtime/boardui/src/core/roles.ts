// 身份与「谁能看哪一页」。
//
// 这是**框架版**：角色存在这台浏览器的 localStorage 里，页面按角色显示或挡住，
// 后端**没有**加鉴权 —— 谁都能把自己切成超管。真身份接上之后，
// 这张表不用重写：只把 `currentRole()` 换成从会话里取，判据本身不动。
//
// 为什么先把这张表当数据写出来：权限规则一散进 `if (role === ...)` 里，
// 就再也没人能一眼回答「成员到底能看哪几页」。

export type Role = "super" | "admin" | "lead" | "member";

export const ROLES: { id: Role; label: string; note: string }[] = [
  { id: "super", label: "超管", note: "看全部，管所有成员与权限" },
  { id: "admin", label: "管理员", note: "管一块业务，定下级的权限" },
  { id: "lead", label: "团队负责人", note: "看自己团队，与下级卡在哪" },
  { id: "member", label: "成员", note: "看自己的活和自己那摊数据" },
];

const ROLE_IDS: Role[] = ["super", "admin", "lead", "member"];
const KEY = "anc.role";

// 每一页：谁能进。没列出来的页 = 所有人可见。
// 顺序即层级：`super` 能看 `admin` 能看的全部，`admin` 能看 `lead` 能看的全部。
const GATED: Record<string, Role[]> = {
  // 控制面：谁能动别人的权限、谁的机器在跑什么 —— 只给管理岗
  members: ["super", "admin", "lead"],
  domains: ["super", "admin"],
  // 运行页：负责人能进，但他只装得下「作业」这一个标签（见下面的标签级门）。
  // 其余三个标签（应用 / 模型 / 接入）是管理动作，仍是管理员以上。
  run: ["super", "admin", "lead"],
  apps: ["super", "admin"],
  models: ["super", "admin"],
  intake: ["super", "admin"],
  system: ["super", "admin"],
  // 审计与沉淀：决策记录与可提炼资产，只有能拍板的人看
  audit: ["super", "admin"],
  sediment: ["super", "admin"],
  // 卡点：上级看下级卡在哪（成员看不到，自己的卡点走待批 / 时间线）
  cases: ["super", "admin", "lead"],
};

export function allowed(role: Role, pageId: string): boolean {
  const gate = GATED[pageId];
  return gate === undefined ? true : gate.includes(role);
}

// 页内**段落**级可见性。整页能进、但其中一段只给管理岗 —— 这才是「精密」发生的地方。
//
// 注意：真做到「张三是成员，只能看自己那 3 条」需要真身份；
// 这一层现在只能按角色切段落。差的那一段记在 PLAN 的风险表里，不假装有。
const SECTIONS: Record<string, Role[]> = {
  // 策略规则暴露「谁被 mask 了什么」—— 那是管理动作，不是成员的信息
  "data.policy": ["super", "admin"],
  // 效率曲线：先只给管事的看，等口径稳了再放开
  "overview.efficiency": ["super", "admin", "lead"],
};

export function visible(role: Role, section: string): boolean {
  const gate = SECTIONS[section];
  return gate === undefined ? true : gate.includes(role);
}

export function roleLabel(role: Role): string {
  return ROLES.find((r) => r.id === role)?.label ?? role;
}

export function currentRole(): Role {
  let raw = "";
  try { raw = localStorage.getItem(KEY) ?? ""; } catch { /* 隐私模式 */ }
  return (ROLE_IDS as string[]).includes(raw) ? (raw as Role) : "super";
}

export function setRole(role: Role): void {
  try { localStorage.setItem(KEY, role); } catch { /* 隐私模式不记 */ }
}

// 这一页被挡住时给的话。不解释体系、不画大饼，只说两件事：你看不到、找谁。
export function deniedReason(role: Role, pageLabel: string): string {
  const gate = roleLabel(role);
  return `「${pageLabel}」对${gate}不开放。要看这一页，找你的上级或超管。`;
}

// 负责人的「自己那一摊」。演示身份没有真组织关系，所以域由他自己选（存在本机）。
// 真身份接上后，这里换成从会话里取 —— 判据不动。
const DOMAIN_KEY = "anc.domain";

export function currentDomain(): string {
  try { return localStorage.getItem(DOMAIN_KEY) ?? ""; } catch { return ""; }
}

export function setDomain(slug: string): void {
  try { localStorage.setItem(DOMAIN_KEY, slug); } catch { /* 隐私模式不记 */ }
}

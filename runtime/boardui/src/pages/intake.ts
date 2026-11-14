import type { Data, JobRow, Kid } from "../core/types";
import { el, section, table } from "../core/dom";
import { localTime, shortTime } from "../core/format";
import { emptyState, unknownState, cmd } from "../ui/states";
import { postJSON } from "../core/api";
import { reload } from "../core/refresh";

// 「接入」：把一个脚本或一个外部接口接进来 —— 什么时候跑、打到哪、用哪个密钥、写到哪个文件。
//
// 这一页按**生产变更**做，不按「开关」做。所以它做三件事：
//   1 看清已接入的东西（请求什么、用哪个密钥名、写到哪里、下次什么时候跑）
//   2 组装一份 spec，并把「这会带来什么」先演一遍（dry-run）
//   3 把生效与停用**留给命令行** —— 看板这边没有写口，这是有意的
//
// 密钥只以**名字**出现：spec 里本来就只有一个键名，值在 secrets.env 里，从不经过这里。

type Draft = {
  name: string;
  title: string;
  kind: "http" | "app";
  every: string;
  at: string;
  tz: string;
  timeout: string;
  method: string;
  url: string;
  secret: string;
  scheme: string;
  app: string;
  writePath: string;
};

const EMPTY: Draft = {
  name: "", title: "", kind: "http",
  every: "24h", at: "09:00", tz: "Asia/Shanghai", timeout: "60s",
  method: "GET", url: "", secret: "", scheme: "bearer",
  app: "", writePath: "reports/",
};

// 组装成真的 anc.job/v1。字段名与 internal/job.Spec 一致 —— 不是「像」，是同一个。
// 未知字段在 CLI 那边会**报错**（json.DisallowUnknownFields），所以这里宁缺毋滥。
function toSpec(d: Draft): Record<string, unknown> {
  const spec: Record<string, unknown> = {
    schema: "anc.job/v1",
    name: d.name.trim(),
    title: d.title.trim(),
    schedule: { every: d.every.trim(), at: d.at.trim(), tz: d.tz.trim() },
    timeout: d.timeout.trim(),
  };
  if (d.kind === "app") {
    spec.app = { name: d.app.trim() };
  } else {
    const http: Record<string, unknown> = { method: d.method.trim().toUpperCase(), url: d.url.trim() };
    const secret = d.secret.trim();
    if (secret !== "") http.auth = { secret, scheme: d.scheme.trim() };
    spec.http = http;
  }
  const wp = d.writePath.trim();
  if (wp !== "") spec.write = { path: wp };
  else spec.write = {};
  return spec;
}

// 只留主机名：完整 URL 又长又带 query，一行里塞不下；全文挂在 title 上。
function hostOf(url: string): string {
  const s = url.replace(/^https?:\/\//, "");
  const i = s.indexOf("/");
  return i < 0 ? s : s.slice(0, i);
}

// 预演：把它跑起来会发生什么，用一句话一条说清。
// 这不是「预测」—— 全部由表单与已有作业推出，没有一条是编的。
function preview(d: Draft, existing: JobRow[]): Kid[] {
  const out: Kid[] = [];
  const name = d.name.trim();
  const dup = existing.find((j) => j.name === name);
  out.push(el("li", {}, name === ""
    ? "还没填名字 —— spec 没有名字，CLI 会拒收。"
    : dup
      ? "名字与已接入的「" + dup.name + "」**撞了**：同名 spec 会被覆盖，先改名。"
      : el("span", {}, "新作业：", el("b", {}, name))));
  const every = d.every.trim();
  const at = d.at.trim();
  out.push(el("li", {}, "每 " + (every === "" ? "（没填）" : every) +
    (at === "" ? "" : " 的 " + at) + "（" + (d.tz.trim() || "没写时区") + "）跑一次"));
  if (d.kind === "http") {
    out.push(el("li", {}, "请求 ", el("span", { class: "mono" }, d.method.trim().toUpperCase() + " " + (d.url.trim() || "（还没填 URL）"))));
    out.push(el("li", {}, d.secret.trim() === ""
      ? "不带凭据 —— 公开接口才这样配。"
      : el("span", {}, "用密钥名 ", cmd(d.secret.trim()), "（值在 secrets.env，不经过看板）")));
  } else {
    out.push(el("li", {}, "跑已审批的应用 ", el("b", {}, d.app.trim() || "（还没选）"),
      " —— 应用的审批与沙箱由 anc app 管，这一页只负责排期。"));
  }
  out.push(el("li", {}, "超时 " + (d.timeout.trim() || "默认 60s"),
    d.writePath.trim() === "" ? "；不落盘（write 为空）" : "；写到 " + d.writePath.trim()));
  return out;
}

type Handlers = { onInput: () => void; onKind: () => void };

// 停用不是删除：spec 留着，enabled 改 false —— 删掉就看不见「曾经接过什么」。
// 一行两条：名字 + 打到哪；下面一行是计划、密钥名、写到哪。
// 用行不用表 —— 这一页在编辑式版面里只有 744px，六列表格必然横向溢出。
function jobRow(j: JobRow): HTMLElement {
  const target = j.kind === "app" ? "应用" : (j.method || "GET") + " " + hostOf(j.url ?? "");
  const meta = [
    (j.every || "—") + (j.at ? " @" + j.at : ""),
    j.kind === "app"
      ? "凭据由应用管"
      : (j.secret ?? "") === "" ? "无凭据" : "密钥名 " + (j.secret ?? ""),
    (j.write_path ?? "") === "" ? "不落盘" : "写到 " + (j.write_path ?? ""),
  ].join(" · ");
  return el("div", { class: "row" },
    el("div", { class: "what" },
      el("b", {}, j.name), " ",
      el("span", { class: "mono", title: j.url ?? "" }, target)),
    el("div", { class: "meta" }, meta),
    stopButton(j.name, j.enabled ?? true));
}

function stopButton(name: string, enabled: boolean): Kid {
  if (!enabled) return el("span", { class: "pill" }, "已停用");
  const b = el("button", { class: "act reject", type: "button", title: "停用这一条（保留 spec）" }, "停用");
  b.addEventListener("click", () => {
    void (async () => {
      b.setAttribute("disabled", "disabled");
      const res = await postJSON<{ ok?: boolean; error?: string; problems?: string[] }>("/api/intake/stop", { name });
      const bad = res.body?.error ?? (res.body?.problems ?? [])[0] ?? (res.status === 200 ? "" : "HTTP " + res.status);
      if (bad !== "") { b.removeAttribute("disabled"); b.textContent = "停用失败"; b.title = bad; return; }
      reload(true);
    })();
  });
  return b;
}

function form(d: Draft, h: Handlers): HTMLElement {
  const field = (label: string, node: HTMLElement): HTMLElement =>
    el("label", { class: "ifield" }, el("span", {}, label), node);
  // kind 是联合类型，不走这个通用输入（它有自己的 select）
  const text = (key: Exclude<keyof Draft, "kind">, ph: string, cls = ""): HTMLElement => {
    const input = el("input", { class: cls, type: "text", value: d[key], placeholder: ph }) as HTMLInputElement;
    input.addEventListener("input", () => { d[key] = input.value; h.onInput(); });
    return input;
  };
  const kindSel = el("select", { class: "ifield-sel" }) as HTMLSelectElement;
  for (const k of ["http", "app"] as const) {
    const o = el("option", { value: k }, k === "http" ? "外部接口（HTTP）" : "已审批的应用") as HTMLOptionElement;
    if (d.kind === k) o.selected = true;
    kindSel.append(o);
  }
  // 换类型会换掉半张表单 —— 这一处重画，其它字段不重画（重画会丢焦点）。
  kindSel.addEventListener("change", () => { d.kind = kindSel.value === "app" ? "app" : "http"; h.onKind(); });

  const rows: Kid[] = [
    field("名字", text("name", "issue-sweep-3（小写、短横线）")),
    field("一句话", text("title", "每日 issue 巡检")),
    field("类型", kindSel),
    field("每隔", text("every", "24h")),
    field("每天几点", text("at", "09:00")),
    field("时区", text("tz", "Asia/Shanghai")),
    field("超时", text("timeout", "60s")),
  ];
  if (d.kind === "http") {
    rows.push(
      field("方法", text("method", "GET")),
      field("URL", text("url", "https://api.example.com/items")),
      field("密钥名", text("secret", "GITHUB_TOKEN（只写名字）")),
      field("写法", text("scheme", "bearer / token / header:Name")),
    );
  } else {
    rows.push(field("应用名", text("app", "hello-model")));
  }
  rows.push(field("写到哪", text("writePath", "reports/issues-{{date}}.md")));
  return el("div", { class: "iform" }, ...rows);
}

export function renderIntake(d: Data): Kid[] {
  const v = d.jobs;
  if (!v) {
    return [unknownState("读不到作业目录。", "服务没回 anc.jobs/v1 —— 这一页的空不是「什么都没接」。")];
  }
  if (!v.wired) {
    return [unknownState("作业目录还没接上。", v.why ?? "")];
  }
  const out: Kid[] = [];

  // 1. 已接入：接了什么、打到哪、用哪个密钥名、写到哪。
  // 用「一行两条」而不是表格：这一页在编辑式版面里只有 744px 宽，
  // 六列表格必然横向溢出，而横着滑的表格没人滑第二次。
  out.push(section("已接入", "共 " + v.jobs.length + " 条",
    v.jobs.length === 0
      ? emptyState("还没有接任何东西。", el("span", {}, "在下面的表单里组装一份 spec，或 ", cmd("anc job ls"), " 看目录。"))
      : el("div", { class: "v2" },
          el("div", { class: "card" }, ...v.jobs.map((j) => jobRow(j))))));

  // 2. 组装 + 预演。表单与预演必须并排：改一个字段就该立刻看到它会做什么。
  const draft: Draft = { ...EMPTY };
  const specBox = el("pre", { class: "file-content" }, "");
  const prevList = el("ul", { class: "dryrun" });
  const cmdLine = el("pre", { class: "file-content" });
  const formBox = el("div", {});
  const renderForm = (): void => {
    formBox.replaceChildren(form(draft, { onInput: () => update(), onKind: () => { renderForm(); update(); } }));
  };
  const update = (): void => {
    const spec = toSpec(draft);
    specBox.textContent = JSON.stringify(spec, null, 2);
    prevList.replaceChildren(...preview(draft, v.jobs));
    const name = draft.name.trim() || "<name>";
    cmdLine.textContent =
      "把上面的 JSON 存成 <vault>/../jobs/" + name + ".json\n然后：anc job tick <vault>";
  };
  renderForm();
  update();

  // 服务端预演 / 生效：本地那份是即时反馈，**权威的是服务端算的这份** ——
  // 落盘前必须让它过一遍，因为它还要做「读回来验一遍」和「备份旧版」。
  const srvBox = el("div", { class: "srv-res" });
  const applyBtn = el("button", {
    class: "act approve", type: "button", "aria-label": "把这份 spec 写到作业目录",
    title: "先服务端预演通过，才允许生效",
  }, "生效（写盘）") as HTMLButtonElement;
  applyBtn.setAttribute("disabled", "disabled");
  const previewBtn = el("button", { class: "act", type: "button" }, "服务端预演");

  const paint = (title: string, facts?: string[], changes?: string[], problems?: string[]): void => {
    srvBox.replaceChildren(
      el("h4", {}, title),
      ...(problems ?? []).map((p) => el("p", { class: "srv-bad" }, "· " + p)),
      ...(facts ?? []).map((f) => el("p", { class: "quiet" }, "· " + f)),
      ...(changes ?? []).map((c) => el("p", { class: "mono srv-diff" }, c)),
    );
  };

  previewBtn.addEventListener("click", () => {
    void (async () => {
      previewBtn.setAttribute("disabled", "disabled");
      paint("服务端预演中…");
      const res = await postJSON<{ ok?: boolean; facts?: string[]; changes?: string[]; problems?: string[]; path?: string; diff?: string; error?: string }>(
        "/api/intake/preview", { spec: toSpec(draft) });
      previewBtn.removeAttribute("disabled");
      const b = res.body;
      const bad = [...(b?.problems ?? []), ...(b?.error ? [b.error] : [])];
      if (res.status !== 200) bad.push("HTTP " + res.status);
      paint(bad.length > 0 ? "预演不通过" : "预演通过 · " + (b?.diff === "new" ? "新建" : "会替换同名 spec"),
        b?.facts, b?.changes, bad);
      if (bad.length === 0) applyBtn.removeAttribute("disabled");
      else applyBtn.setAttribute("disabled", "disabled");
    })();
  });

  applyBtn.addEventListener("click", () => {
    void (async () => {
      applyBtn.setAttribute("disabled", "disabled");
      const res = await postJSON<{ ok?: boolean; path?: string; changes?: string[]; problems?: string[]; error?: string }>(
        "/api/intake/apply", { spec: toSpec(draft) });
      const b = res.body;
      const bad = [...(b?.problems ?? []), ...(b?.error ? [b.error] : [])];
      if (bad.length > 0 || !b?.ok) { paint("生效失败（什么都没写）", undefined, undefined, bad); return; }
      paint("已落盘 · " + (b?.path ?? ""), b?.changes);
      reload(true);
    })();
  });
  out.push(section("组装一个", "改一个字段，右边立刻跟着变",
    el("div", { class: "intake-grid" },
      el("div", {}, formBox, el("div", { class: "intake-acts" }, previewBtn, applyBtn), srvBox),
      el("div", { class: "intake-side" },
        el("h4", {}, "预演（本地即时）"),
        prevList,
        el("h4", {}, "spec"),
        specBox,
        el("h4", {}, "怎么生效"),
        cmdLine))));

  // 3. 写口存在，但写法必须是"生产变更"那一套 —— 把顺序写在页面上，读的人才知道点下去会发生什么。
  //
  // 这段原来写的是「看板是只读的：它没有写口，也不该有」，而同一页上已经有两个写按钮。
  // 文案与控件互相打脸，比"没有功能"更伤信任：读的人会怀疑整页都在糊弄。
  out.push(section("这里为什么不是「一键开关」",
    "写口在，但按生产变更做",
    el("p", { class: "quiet" },
      "点「生效」不会直接覆盖文件。顺序是固定的：",
      el("b", {}, "预演（只算不写）→ 逐字段 diff → 写临时文件 → 读回来验一遍 → 备份旧版 → 原子换名"),
      "。任何一步失败，盘上什么都不变 —— 包括最后一步失败时把备份放回去。"),
    el("p", { class: "quiet" },
      "停用只把 ", el("span", { class: "mono" }, "enabled"), " 改成 false，",
      el("b", {}, "spec 留着"),
      "：删掉就看不见「曾经接过什么」。真要删，是命令行上更重的动作。"),
    el("p", { class: "quiet" },
      "每一步都进 audit，落点与命令行同一套（", el("span", { class: "mono" }, "anc job tick"), " 走的就是这里）。")));


  return out;
}

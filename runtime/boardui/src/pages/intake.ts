import type { Data, JobRow, Kid } from "../core/types";
import { el, section, table } from "../core/dom";
import { localTime, shortTime } from "../core/format";
import { emptyState, unknownState, cmd } from "../ui/states";

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
  // app 类型的名字通常就是应用名（app-hello-model 跑 hello-model）——
  // 再写一遍「app:hello-model」等于同一句话说两遍。
  const target = (j: JobRow): string =>
    j.kind === "app" ? "应用" : (j.method || "GET") + " " + hostOf(j.url ?? "");
  const plan = (j: JobRow): string => (j.every || "—") + (j.at ? " @" + j.at : "");
  out.push(section("已接入", "共 " + v.jobs.length + " 条",
    v.jobs.length === 0
      ? emptyState("还没有接任何东西。", el("span", {}, "在下面的表单里组装一份 spec，或 ", cmd("anc job ls"), " 看目录。"))
      : el("div", { class: "v2" },
          el("div", { class: "card" },
            ...v.jobs.map((j) => el("div", { class: "row" },
              el("div", { class: "what" },
                el("b", {}, j.name), " ",
                el("span", { class: "mono", title: j.url ?? "" }, target(j))),
              el("div", { class: "meta" },
                plan(j),
                j.kind === "app"
                  ? " · 凭据由应用管"
                  : (j.secret ?? "") === "" ? " · 无凭据" : " · 密钥名 " + (j.secret ?? ""),
                (j.write_path ?? "") === "" ? " · 不落盘" : " · 写到 " + (j.write_path ?? ""))))))));

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
  out.push(section("组装一个", "改一个字段，右边立刻跟着变",
    el("div", { class: "intake-grid" },
      formBox,
      el("div", { class: "intake-side" },
        el("h4", {}, "预演"),
        prevList,
        el("h4", {}, "spec"),
        specBox,
        el("h4", {}, "怎么生效"),
        cmdLine))));

  // 3. 明确写清缺口：看板没有写口。这句话必须在这一页上，不能只写在文档里。
  out.push(section("为什么这里没有「一键生效 / 一键停」",
    "这不是没做完，是有意的",
    el("p", { class: "quiet" },
      "看板是只读的：它没有写口，也不该有。拨一下开关就开始定时打外部 API，那是生产变更 —— ",
      "生效、改期、停用都走命令行（", el("span", { class: "mono" }, "anc job tick"), " / 删掉 spec 文件），",
      "每一步都在 audit 里留痕。"),
    el("p", { class: "quiet" },
      "下一步会把「预演 → diff → 人工确认 → 写盘」做成一条真正的事务（见 PLAN 阶段 5 之后）。")));

  return out;
}

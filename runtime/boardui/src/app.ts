import type { ContextView, GrantsView, SearchView } from "./core/types";
import type { AppsView, CatalogView, DataSourcesView, FileListView, FileSnapView, JobsView, ModelsView } from "./core/types";
import { mountChatPanel } from "./ui/chatpanel";
import { contract, fetchData, getJSON } from "./core/api";
import { ctxQuery, currentId, grantsQuery, hashQuery, searchQuery } from "./core/query";
import { setReloader, setRerender } from "./core/refresh";
import { cache, setCache } from "./core/state";
import { activeSystemTab } from "./pages/system";
import { pageFor } from "./router";
import { shell } from "./ui/shell";

// ANC 看板前端（浏览器侧）入口。
//
// 目录分工：
//   core/   协议类型、取数、URL 参数、主题基础件
//   ui/     通用块、状态口径、外壳、抽屉、命令面板、两张 Canvas
//   org/    组织派生关系与详情抽屉
//   pages/  一页一个文件，只回答页头写下的那一个问题
//   router.ts  页面注册表（顺序就是侧栏顺序）
//
// 五条纪律仍在（真相源文字只走 textContent / 不猜不补 / 几乎只读 / 一页一问 / 未接入如实标）。
// 构建：`npm run build`（esbuild 打成 app.js，随 anc 二进制一起嵌进去）。


async function boot(force = false): Promise<void> {
  if (force) setCache(null);
  if (cache === null) {
    shell();
    setCache(await fetchData());
  }
  shell();
  loadForPage();
}

// 应用页 / 模型页：这两页的数据不在启动时那一次取齐 —— 进了才取，免得每次刷新都多两次请求。
async function loadApps(): Promise<void> {
  const res = await getJSON<AppsView>("/api/apps");
  const ap = contract(res, "应用清单");
  if (cache) setCache({ ...cache, apps: ap.v, appsErr: ap.err });
  shell();
}

async function loadModels(): Promise<void> {
  const res = await getJSON<ModelsView>("/api/models");
  const md = contract(res, "模型注册表");
  if (cache) setCache({ ...cache, models: md.v, modelsErr: md.err });
  shell();
}

async function loadJobs(): Promise<void> {
  const res = await getJSON<JobsView>("/api/jobs");
  const jb = contract(res, "作业清单");
  if (cache) setCache({ ...cache, jobs: jb.v, jobsErr: jb.err });
  shell();
}

async function loadDataSources(): Promise<void> {
  const res = await getJSON<DataSourcesView>("/api/datasources");
  const ds = contract(res, "数据源");
  if (cache) setCache({ ...cache, ds: ds.v, dsErr: ds.err });
  // 没选数据源就默认第一个
  const q = hashQuery().get("ds") ?? "";
  const first = ds.v?.datasources?.[0]?.name ?? "";
  const want = q || first;
  if (want === "") {
    shell();
    return;
  }
  const row = ds.v?.datasources?.find((x) => x.name === want);
  if (row?.type === "file") {
    await loadFiles(want, hashQuery().get("p") ?? "");
    return;
  }
  await loadCatalog(want);
}

async function loadFiles(name: string, path: string): Promise<void> {
  const snap = await getJSON<FileSnapView>("/api/files?ds=" + encodeURIComponent(name));
  const tok = await ensureQueryToken();
  const list = await fetch("/api/files/ls?ds=" + encodeURIComponent(name) + "&path=" + encodeURIComponent(path), {
    headers: { Authorization: "Bearer " + tok },
  });
  let fl: FileListView | null = null;
  let err = "";
  if (list.ok) fl = (await list.json()) as FileListView;
  else err = (await list.text()).slice(0, 200);
  if (cache) setCache({ ...cache, fileSnap: snap.body, fileList: fl, filePath: path, fileErr: err });
  shell();
}

export async function ensureQueryToken(): Promise<string> {
  if (queryToken !== "") return queryToken;
  const sess = await fetch("/api/chat/session", { method: "POST" });
  const j = (await sess.json()) as { query_token?: string };
  queryToken = j.query_token ?? "";
  return queryToken;
}

async function loadCatalog(name: string): Promise<void> {
  const res = await getJSON<CatalogView>("/api/catalog?ds=" + encodeURIComponent(name));
  const cat = contract(res, "目录");
  if (cache) setCache({ ...cache, cat: cat.v, catErr: cat.err });
  shell();
}

function loadForPage(): boolean {
  const id = pageFor(currentId()).id;
  if (id === "apps") { void loadApps(); return true; }
  if (id === "models") { void loadModels(); return true; }
  if (id === "jobs") { void loadJobs(); return true; }
  if (id === "data") { void loadDataSources(); return true; }
  return false;
}

// 授权表那一页的取数：换视角时只重取这一份（其余几份不用动）。
async function loadGrants(): Promise<void> {
  const res = await getJSON<GrantsView>("/api/grants" + grantsQuery());
  const gr = contract(res, "授权表");
  if (cache) setCache({ ...cache, grants: gr.v, grantsErr: gr.err });
  shell();
}

// 上下文那一页的取数：换人 / 换范围时就重取那一份（其余几份不用动）。
// 只重取它自己，不是整页重取 —— 这一页的选择是**参数**，不是刷新。
async function loadContext(): Promise<void> {
  const res = await getJSON<ContextView>("/api/context" + ctxQuery());
  const cx = contract(res, "工作上下文");
  if (cache) setCache({ ...cache, context: cx.v, contextErr: cx.err });
  shell();
}

// 检索页的取数：查询词 / 视角 / 域都在 URL 里。换一次条件只重取这一份；
// 索引仍然是后端每次从 markdown 重建 —— 看板不另存一份会过期的答案。
async function loadSearch(): Promise<void> {
  if (searchQuery() === "") {
    if (cache) setCache({ ...cache, search: null, searchErr: "" });
    shell();
    return;
  }
  const res = await getJSON<SearchView>("/api/search" + searchQuery());
  const sr = contract(res, "知识检索");
  if (cache) setCache({ ...cache, search: sr.v, searchErr: sr.err });
  shell();
}

setRerender(shell);
setReloader((force) => void boot(force));

window.addEventListener("hashchange", () => {
  if (loadForPage()) return;
  // 这一页的「选中谁」在 hash 里：进了这一页就按 URL 重取一次，别的页照旧（取数只在启动 / 刷新）。
  if (pageFor(currentId()).id === "context") {
    void loadContext();
    return;
  }
  if (pageFor(currentId()).id === "search") {
    void loadSearch();
    return;
  }
  // 授权表并进了「系统 · 网关与授权」：换视角只重取它自己（那是**参数**，不是刷新）。
  if (pageFor(currentId()).id === "system" && activeSystemTab().id === "gateway" && (hashQuery().get("who") ?? "") !== "") {
    void loadGrants();
    return;
  }
  shell();
});

void boot();
mountChatPanel();
wireDataQuery();
wireFileClick();

// ---------- 数据页的查询框 ----------
// 票由看板自己签（query_token），主体是 board-ui；策略/行过滤在服务端生效。
let queryToken = "";

async function wireDataQuery(): Promise<void> {
  // 每次渲染后重新挂（页面是整段重建的）
  const box = document.getElementById("data-query");
  if (!box || box.dataset.wired === "1") {
    if (!box) setTimeout(() => void wireDataQuery(), 300);
    return;
  }
  box.dataset.wired = "1";
  const btn = box.querySelector(".query-run") as HTMLButtonElement | null;
  const input = box.querySelector(".query-input") as HTMLTextAreaElement | null;
  const out = box.querySelector(".query-out") as HTMLElement | null;
  if (!btn || !input || !out) return;
  btn.addEventListener("click", async () => {
    const sql = input.value.trim();
    if (sql === "") return;
    out.textContent = "查询中…";
    try {
      if (queryToken === "") {
        const sess = await fetch("/api/chat/session", { method: "POST" });
        const j = (await sess.json()) as { query_token?: string };
        queryToken = j.query_token ?? "";
        if (queryToken === "") throw new Error("看板没签出查询票");
      }
      const ds = cache?.cat?.datasource ?? hashQuery().get("ds") ?? "";
      const res = await fetch("/api/query", {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: "Bearer " + queryToken },
        body: JSON.stringify({ datasource: ds, sql }),
      });
      const body = await res.json();
      if (!res.ok) {
        out.textContent = "被拒/出错：" + (body.error ?? res.status);
        return;
      }
      const head = (body.columns as string[]).join(" | ");
      const lines = (body.rows as (string | null)[][]).map((r) => r.map((v) => (v === null ? "NULL" : v)).join(" | "));
      const meta = `${body.returned} 行` +
        (body.masked?.length ? ` · 脱敏 ${body.masked.join(",")}` : "") +
        (body.filters?.length ? ` · 行过滤 ${body.filters.join("；")}` : "");
      out.textContent = [head, ...lines, "", meta].join("\n");
    } catch (e) {
      out.textContent = "出错：" + (e instanceof Error ? e.message : String(e));
    }
  });
}

// 点文件 → 读内容（事件委托：列表是异步渲染的，逐次绑会漏）
let fileClickWired = false;
function wireFileClick(): void {
  if (fileClickWired) return;
  fileClickWired = true;
  document.addEventListener("click", async (ev) => {
    const a = (ev.target as HTMLElement)?.closest?.("[data-file]") as HTMLElement | null;
    if (!a) return;
    const path = a.getAttribute("data-file") ?? "";
    if (path === "") return; // 目录：走普通链接
    ev.preventDefault();
    const pre = document.getElementById("file-content");
    if (!pre) return;
    const ds = hashQuery().get("ds") ?? "";
    pre.textContent = "读取中…";
    const tok = await ensureQueryToken();
    const res = await fetch("/api/files/cat?ds=" + encodeURIComponent(ds) + "&path=" + encodeURIComponent(path), {
      headers: { Authorization: "Bearer " + tok },
    });
    const body = await res.json();
    pre.textContent = res.ok
      ? (body.content ?? "") + (body.truncated ? "\n…（截断）" : "")
      : "被拒/出错：" + (body.error ?? res.status);
  });
}

import { el } from "../core/dom";

// 右下角的对话面板：像浏览器里的侧边助手，点开在右侧滑出。
//
// 它背后接的是 **ANC 自己的 super agent**（`/api/chat`，走闸、预算、限流、台账）。
// 所以面板里能看见的不只是回答，还有：调了哪个工具、花了多少、为什么被拒。

type Msg = { role: "user" | "agent"; text: string };

let panelOpen = false;
let token = "";
let model = "";
let messages: Msg[] = [];

function bubble(m: Msg): HTMLElement {
  return el("div", { class: "chat-msg " + m.role },
    el("div", { class: "chat-role" }, m.role === "user" ? "我" : model || "ANC"),
    el("div", { class: "chat-text" }, m.text),
  );
}

async function ensureToken(): Promise<string> {
  if (token !== "") return token;
  const res = await fetch("/api/chat/session", { method: "POST" });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`拿不到会话票（HTTP ${res.status}）：${body.slice(0, 120)}`);
  }
  const j = (await res.json()) as { token: string; model: string };
  token = j.token;
  model = j.model;
  return token;
}

// 发一条，边收边写进气泡。SSE 帧按空行切；`data:` 里是 JSON。
async function send(text: string, log: HTMLElement, out: HTMLElement, meta: HTMLElement): Promise<void> {
  const tok = await ensureToken();
  const res = await fetch("/api/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: "Bearer " + tok },
    body: JSON.stringify({ prompt: text }),
  });
  if (!res.ok || !res.body) {
    const body = await res.text();
    out.textContent = "被拒绝了：" + body.slice(0, 300);
    return;
  }
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = "";
  let acc = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    const frames = buf.split("\n\n");
    buf = frames.pop() ?? "";
    for (const f of frames) {
      const evLine = f.split("\n").find((l) => l.startsWith("event: "));
      const dataLine = f.split("\n").find((l) => l.startsWith("data: "));
      if (!dataLine) continue;
      let payload: any = {};
      try { payload = JSON.parse(dataLine.slice(6)); } catch { continue; }
      const ev = (evLine ?? "").slice(7).trim();
      if (ev === "delta" && payload.text) { acc += payload.text; out.textContent = acc; }
      if (ev === "tool") log.textContent = `→ 调用 ${payload.name}`;
      if (ev === "done") {
        const u = payload.usage ?? {};
        meta.textContent = `${payload.steps ?? "?"} 步 · in ${u.in ?? 0} / out ${u.out ?? 0}` +
          (payload.incomplete ? ` · 未完成：${payload.why ?? ""}` : "");
      }
    }
  }
}

export function mountChatPanel(): void {
  if (document.getElementById("anc-chat")) return;

  const list = el("div", { class: "chat-list" });
  const input = el("textarea", { class: "chat-input", rows: "2", placeholder: "问 ANC：现在有什么、谁在等、能不能做…" }) as HTMLTextAreaElement;
  const sendBtn = el("button", { class: "chat-send" }, "发送") as HTMLButtonElement;
  const hint = el("div", { class: "chat-hint" }, "每条都会过闸、记账；被拒会写清理由。");
  const panel = el("div", { class: "chat-panel", id: "anc-chat-panel" },
    el("div", { class: "chat-head" },
      el("span", {}, "和 ANC 说话"),
      el("button", { class: "chat-close", title: "收起" }, "×"),
    ),
    list, hint,
    el("div", { class: "chat-compose" }, input, sendBtn),
  ) as HTMLElement;

  const launcher = el("button", { class: "chat-launcher", id: "anc-chat", title: "和 ANC 说话" }, "✦") as HTMLButtonElement;
  document.body.append(launcher, panel);

  const toggle = (open: boolean): void => {
    panelOpen = open;
    panel.classList.toggle("open", open);
    if (open) input.focus();
  };
  launcher.addEventListener("click", () => toggle(!panelOpen));
  (panel.querySelector(".chat-close") as HTMLElement).addEventListener("click", () => toggle(false));

  const submit = async (): Promise<void> => {
    const text = input.value.trim();
    if (text === "") return;
    input.value = "";
    messages.push({ role: "user", text });
    list.append(bubble({ role: "user", text }));
    const out = el("div", { class: "chat-text" }, "");
    const log = el("div", { class: "chat-tool" }, "");
    const meta = el("div", { class: "chat-meta" }, "");
    const agent = el("div", { class: "chat-msg agent" },
      el("div", { class: "chat-role" }, "ANC"), out, log, meta);
    list.append(agent);
    list.scrollTop = list.scrollHeight;
    try {
      await send(text, log, out, meta);
    } catch (e) {
      out.textContent = "出错：" + (e instanceof Error ? e.message : String(e));
    }
    list.scrollTop = list.scrollHeight;
  };
  sendBtn.addEventListener("click", () => void submit());
  input.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter" && !ev.shiftKey) { ev.preventDefault(); void submit(); }
  });
}

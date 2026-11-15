// ANC 新前端 —— React 19 + AntD 6。
//
// 迁移方式：旧界面留在 /，新界面在 /next 一页一页搬。这里现在是两页：
// 总览（读 + 指路）与审计（表格）。契约一个字节没改，全走现有 /api/*。
//
// 与手搓版的差别不是"更好看"，是：状态由框架管（不整页重建）、
// 表格/分页/排序/空态由组件给（不手写、也就不会漏）。

import { StrictMode, useEffect, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  ConfigProvider, Layout, Menu, Card, Statistic, Row, Col, List, Tag,
  Table, Input, Alert, Button, Space, Typography, Empty,
} from "antd";
import zhCN from "antd/locale/zh_CN";
import type { ColumnsType } from "antd/es/table";

type Member = { name: string; display_name: string; role: string; disabled: boolean; domains: string[] };
type Domain = { slug: string; name: string; what: string };
type Project = { slug: string; name: string; domain: string };
type Agent = { slug: string; domain: string };
type Board = { company: { name: string }; members: Member[]; domains: Domain[]; projects: Project[]; agents: Agent[] };
type Pending = { id: string; title: string; who: string; to: string; enqueued: string };
type Approvals = { wired: boolean; count: number; pending: Pending[]; why?: string };
// 注意：/api/timeline 的 `entries` 是**条数**（number），不是数组；数组在 `cases`。
// 这是照着真实 payload 改的 —— 契约在前，我的类型在后。
type Case = { key: string; title: string; band: string; last_at: string; status?: string };
type Timeline = { wired: boolean; entries: number; total: number; cases: Case[] };
type AuditRow = { id: string; at: string; actor: string; action: string; object: string; result: string; why?: string; src?: string };
type Audit = { wired: boolean; total: number; records: AuditRow[]; note?: string };

const RESULT_COLOR: Record<string, string> = { ok: "green", denied: "volcano", failed: "red" };
const RESULT_WORD: Record<string, string> = { ok: "通过", denied: "被拒", failed: "失败" };

async function get<T>(url: string): Promise<{ data: T | null; err: string }> {
  try {
    const r = await fetch(url);
    const b = await r.json();
    if (r.status >= 400) return { data: null, err: (b && (b.error || b.why)) || `HTTP ${r.status}` };
    return { data: b as T, err: "" };
  } catch (e) {
    return { data: null, err: "连不上看板服务：" + String(e) };
  }
}

// 一句话结论的优先级只看一件事：**要不要你动手**。
// 红 bot 降级为附注 —— 它是运维的事，不是老板的事（这条是用户点的）。
function verdict(b: Board | null, ap: Approvals | null): { text: string; lead: string; tone: "ok" | "warn" | "bad" } {
  if (!b) return { text: "读不到组织事实", lead: "投影没回来 —— 先看服务在不在。", tone: "bad" };
  if (ap && ap.wired && ap.count > 0) {
    return { text: `有 ${ap.count} 件事等你点头`, lead: "点头只留痕，不改真相源；该不该批仍然由你判断。", tone: "warn" };
  }
  return { text: "眼下没有要你动手的事", lead: "该办的都办完了。", tone: "ok" };
}

function Overview({ go }: { go: (k: string) => void }) {
  const [b, setB] = useState<Board | null>(null);
  const [ap, setAp] = useState<Approvals | null>(null);
  const [tl, setTl] = useState<Timeline | null>(null);
  const [err, setErr] = useState("");

  const load = () => {
    setErr("");
    Promise.all([get<Board>("/api/board"), get<Approvals>("/api/approvals"), get<Timeline>("/api/timeline?limit=50")])
      .then(([board, approvals, timeline]) => {
        if (board.err) { setErr(board.err); return; }
        setB(board.data); setAp(approvals.data); setTl(timeline.data);
      });
  };
  useEffect(load, []);

  const v = verdict(b, ap);
  const cells = useMemo(() => {
    if (!b) return [];
    return b.domains.map((d) => {
      const people = b.members.filter((m) => !m.disabled && (m.domains ?? []).includes(d.slug)).length;
      const projs = b.projects.filter((p) => p.domain === d.slug).length;
      const ags = b.agents.filter((a) => a.domain === d.slug).length;
      return { ...d, people, projs, ags, empty: people + projs + ags === 0 };
    });
  }, [b]);

  if (err) {
    return <Alert type="error" showIcon message="读不到投影" description={err}
      action={<Button size="small" onClick={load}>重试</Button>} />;
  }

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      <Card>
        <Typography.Title level={3} style={{ margin: 0, color: v.tone === "warn" ? "#b7791f" : v.tone === "bad" ? "#c03027" : undefined }}>
          {v.text}
        </Typography.Title>
        <Typography.Paragraph type="secondary" style={{ margin: "6px 0 0" }}>{v.lead}</Typography.Paragraph>
        <Row gutter={16} style={{ marginTop: 16 }}>
          <Col span={6}><Statistic title="等你点头" value={ap?.count ?? 0} /></Col>
          <Col span={6}><Statistic title="业务模块" value={b?.domains.length ?? 0} /></Col>
          <Col span={6}><Statistic title="在办项目" value={b?.projects.length ?? 0} /></Col>
          <Col span={6}><Statistic title="成员" value={b?.members.filter((m) => !m.disabled).length ?? 0} /></Col>
        </Row>
      </Card>

      <Card title="业务模块" extra={<Typography.Text type="secondary">点一格进项目列表</Typography.Text>}>
        <Row gutter={[12, 12]}>
          {cells.map((c) => (
            <Col key={c.slug} xs={24} sm={12} md={8} lg={6}>
              <Card size="small" hoverable style={c.empty ? { borderStyle: "dashed", opacity: 0.8 } : undefined}
                onClick={() => { location.href = "/#/projects?domain=" + encodeURIComponent(c.slug); }}>
                <Typography.Text strong>{c.name || c.slug}</Typography.Text>
                <div style={{ marginTop: 6 }}>
                  {c.people} 人 · {c.projs} 项目 · {c.ags} agent
                </div>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {c.empty ? "有人认领，但还没有资料" : c.what || "—"}
                </Typography.Text>
              </Card>
            </Col>
          ))}
        </Row>
      </Card>

      <Row gutter={16}>
        <Col span={12}>
          <Card title="要我做" extra={<Button type="link" onClick={() => go("approvals")}>去待批</Button>}>
            {ap && ap.wired && ap.pending.length > 0 ? (
              <List size="small" dataSource={ap.pending.slice(0, 5)}
                renderItem={(p) => (
                  <List.Item extra={<Tag color="volcano">等你签</Tag>}>
                    <List.Item.Meta title={p.title || p.id} description={`${p.who} · ${(p.to || "").trim() || "没写该谁批"}`} />
                  </List.Item>
                )} />
            ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有等你签的字" />}
          </Card>
        </Col>
        <Col span={12}>
          <Card title="最近发生" extra={<Typography.Text type="secondary">共 {tl?.total ?? 0} 条</Typography.Text>}>
            <List size="small" dataSource={(tl?.cases ?? []).slice(-5).reverse()}
              renderItem={(c) => (
                <List.Item extra={<Typography.Text type="secondary" style={{ fontSize: 12 }}>{String(c.last_at || "").replace("T", " ").slice(5, 16)}</Typography.Text>}>
                  {c.title || c.key}
                </List.Item>
              )} />
          </Card>
        </Col>
      </Row>
    </Space>
  );
}

function AuditPage() {
  const [data, setData] = useState<Audit | null>(null);
  const [err, setErr] = useState("");
  const [q, setQ] = useState("");

  const load = () => {
    setErr("");
    get<Audit>("/api/audit?limit=500").then(({ data, err }) => {
      if (err) { setErr(err); return; }
      setData(data);
    });
  };
  useEffect(load, []);

  const rows = useMemo(() => {
    const list = data?.records ?? [];
    const k = q.trim().toLowerCase();
    if (!k) return list;
    return list.filter((r) => [r.actor, r.action, r.object, r.result, r.why].join(" ").toLowerCase().includes(k));
  }, [data, q]);

  const columns: ColumnsType<AuditRow> = [
    { title: "时间", dataIndex: "at", width: 150, sorter: (a, b) => (a.at < b.at ? -1 : 1),
      render: (v: string) => <span style={{ fontVariantNumeric: "tabular-nums" }}>{v.replace("T", " ").slice(0, 16)}</span> },
    { title: "谁", dataIndex: "actor", width: 110 },
    { title: "做了什么", dataIndex: "action", width: 110,
      filters: [{ text: "read", value: "read" }, { text: "invoke", value: "invoke" }], onFilter: (v, r) => r.action === v },
    { title: "对谁", dataIndex: "object", ellipsis: true },
    { title: "结果", dataIndex: "result", width: 90, render: (v: string) => <Tag color={RESULT_COLOR[v] ?? "default"}>{RESULT_WORD[v] ?? v}</Tag> },
    { title: "为什么", dataIndex: "why", ellipsis: true },
    { title: "来源", dataIndex: "src", width: 200, ellipsis: true, render: (v?: string) => (v ? <code>{v}</code> : "—") },
  ];

  if (err) return <Alert type="error" showIcon message="读不到审计" description={err} action={<Button size="small" onClick={load}>重试</Button>} />;

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      <Space wrap>
        <Typography.Text strong>共 {data?.total ?? 0} 条</Typography.Text>
        <Input.Search allowClear placeholder="搜：人 / 动作 / 对象 / 原因" style={{ width: 320 }} onChange={(e) => setQ(e.target.value)} />
        <Typography.Text type="secondary">点列头排序；来源列是 文件:行号</Typography.Text>
      </Space>
      <Table<AuditRow> size="middle" rowKey={(r) => r.src ?? r.id} columns={columns} dataSource={rows}
        pagination={{ pageSize: 50, showSizeChanger: true, showTotal: (t) => `共 ${t} 条` }} scroll={{ x: 1100 }} />
    </Space>
  );
}

function App() {
  const [page, setPage] = useState("overview");
  return (
    <Layout style={{ minHeight: "100vh", background: "#f5f7fa" }}>
      <Layout.Sider theme="light" width={200}>
        <div style={{ padding: 16, fontWeight: 700, letterSpacing: 1 }}>ANC</div>
        <Menu mode="inline" selectedKeys={[page]} onClick={(e) => setPage(e.key)}
          items={[{ key: "overview", label: "总览" }, { key: "audit", label: "审计" }]} />
      </Layout.Sider>
      <Layout.Content style={{ padding: 20 }}>
        {page === "overview" ? <Overview go={setPage} /> : <AuditPage />}
      </Layout.Content>
    </Layout>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ConfigProvider locale={zhCN} theme={{ token: { colorPrimary: "#3b5bdb", borderRadius: 8 } }}>
      <App />
    </ConfigProvider>
  </StrictMode>,
);

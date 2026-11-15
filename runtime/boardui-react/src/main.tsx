// 新前端的第一个实验页：审计。
//
// 选它的理由：它是全站最表格化的一页（53 条记录、要排序/筛选/看来源），
// 手搓版只能做成"一行两条"的卡片流。用 AntD 的 Table 能不能一屏看完，是这次实验的判据。
//
// 这一版只做**读**：数据从现有 /api/audit 拿，契约与字段一个不改。

import { StrictMode, useEffect, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { ConfigProvider, Table, Tag, Input, Alert, Button, Space, Typography } from "antd";
import zhCN from "antd/locale/zh_CN";
import type { ColumnsType } from "antd/es/table";

type Row = {
  id: string;
  at: string;
  actor: string;
  action: string;
  object: string;
  result: string;
  why?: string;
  tool?: string;
  source?: string;
  src?: string;
};

type AuditView = {
  schema: string;
  wired: boolean;
  total: number;
  records: Row[];
  note?: string;
  error?: string;
};

const RESULT_COLOR: Record<string, string> = { ok: "green", denied: "volcano", failed: "red" };
const RESULT_WORD: Record<string, string> = { ok: "通过", denied: "被拒", failed: "失败" };

function App() {
  const [data, setData] = useState<AuditView | null>(null);
  const [err, setErr] = useState("");
  const [q, setQ] = useState("");

  const load = () => {
    setErr("");
    fetch("/api/audit?limit=500")
      .then((r) => r.json())
      .then((b: AuditView) => setData(b))
      .catch((e) => setErr("连不上看板服务：" + String(e)));
  };
  useEffect(load, []);

  const rows = useMemo(() => {
    const list = data?.records ?? [];
    const k = q.trim().toLowerCase();
    if (k === "") return list;
    return list.filter((r) =>
      [r.actor, r.action, r.object, r.result, r.why, r.tool].join(" ").toLowerCase().includes(k));
  }, [data, q]);

  const columns: ColumnsType<Row> = [
    { title: "时间", dataIndex: "at", width: 150, sorter: (a, b) => (a.at < b.at ? -1 : 1),
      render: (v: string) => <span className="num">{v.replace("T", " ").slice(0, 16)}</span> },
    { title: "谁", dataIndex: "actor", width: 120 },
    { title: "做了什么", dataIndex: "action", width: 110, filters: [{ text: "read", value: "read" }, { text: "invoke", value: "invoke" }],
      onFilter: (v, r) => r.action === v },
    { title: "对谁", dataIndex: "object", ellipsis: true },
    { title: "结果", dataIndex: "result", width: 96,
      render: (v: string) => <Tag color={RESULT_COLOR[v] ?? "default"}>{RESULT_WORD[v] ?? v}</Tag> },
    { title: "为什么", dataIndex: "why", ellipsis: true },
    { title: "来源", dataIndex: "src", width: 210, ellipsis: true,
      render: (v?: string) => (v ? <code>{v}</code> : "—") },
  ];

  if (err) {
    return <Alert type="error" showIcon message="读不到审计" description={err}
      action={<Button size="small" onClick={load}>重试</Button>} />;
  }
  if (!data) return <Table loading columns={columns} dataSource={[]} pagination={false} />;
  if (!data.wired) {
    return <Alert type="warning" showIcon message="审计还没接上"
      description={data.note ?? "服务说这一段读不到。"} action={<Button size="small" onClick={load}>重试</Button>} />;
  }

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      <Space wrap>
        <Typography.Text strong>共 {data.total} 条</Typography.Text>
        <Input.Search allowClear placeholder="搜：人 / 动作 / 对象 / 原因" style={{ width: 320 }}
          onChange={(e) => setQ(e.target.value)} />
        <Typography.Text type="secondary">点列头排序；来源列是 文件:行号</Typography.Text>
      </Space>
      <Table<Row>
        size="middle"
        rowKey={(r) => r.src ?? r.id}
        columns={columns}
        dataSource={rows}
        pagination={{ pageSize: 50, showSizeChanger: true, showTotal: (t) => `共 ${t} 条` }}
        scroll={{ x: 1100 }}
      />
    </Space>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ConfigProvider locale={zhCN} theme={{ token: { colorPrimary: "#3b5bdb", borderRadius: 8 } }}>
      <div style={{ padding: 20 }}>
        <App />
      </div>
    </ConfigProvider>
  </StrictMode>,
);

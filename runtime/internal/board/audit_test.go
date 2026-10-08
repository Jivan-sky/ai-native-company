package board

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/audit"
)

// auditFixture 造一个带业务域（trade → projects / logistics → shipments）的 vault，
// 再往里记几条行使 —— 判「跨域」要靠域表，所以这里必须用 domains 夹具。
func auditFixture(t *testing.T) string {
	t.Helper()
	vault := t.TempDir()
	if err := os.CopyFS(vault, os.DirFS(fixturePath(t, "domains"))); err != nil {
		t.Fatal(err)
	}
	return vault
}

func auditWrite(t *testing.T, vault string, r audit.Record) {
	t.Helper()
	if _, err := audit.Append(vault, r); err != nil {
		t.Fatal(err)
	}
}

func auditOf(t *testing.T, s *Server, path string) (AuditView, string) {
	t.Helper()
	rec := get(t, s.Handler(), path)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200：%s", rec.Code, rec.Body.String())
	}
	var v AuditView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("不是合法 JSON：%v", err)
	}
	if v.Schema != AuditSchema {
		t.Fatalf("schema=%q，期望 %s", v.Schema, AuditSchema)
	}
	return v, rec.Body.String()
}

// 目录不在 = 还没有行使 = 空流水，**不是错**（刚 init 的机器不该红）。
func TestAuditEmptyIsWiredNotBroken(t *testing.T) {
	vault := t.TempDir()
	v, _ := auditOf(t, &Server{Vault: vault}, "/api/audit")
	if !v.Wired || v.Total != 0 || len(v.Records) != 0 {
		t.Fatalf("想要 wired 空页，得到 wired=%v total=%d", v.Wired, v.Total)
	}
	if len(v.Missing) == 0 {
		t.Error("这一页答不了什么必须照写（它是「系统记下来的」，不是「全部行使」）")
	}
}

func TestAuditContractAndNewestFirst(t *testing.T) {
	vault := auditFixture(t)
	auditWrite(t, vault, audit.Record{ID: "a", At: "2026-10-08T10:00:00+08:00", Actor: "alice",
		Action: audit.ActionRead, Object: filepath.Join(vault, "projects", "x.md"), Result: audit.ResultOK})
	auditWrite(t, vault, audit.Record{ID: "b", At: "2026-10-08T11:00:00+08:00", Actor: "alice",
		Action: audit.ActionWrite, Object: filepath.Join(vault, "shipments", "y.md"),
		Result: audit.ResultDenied, Why: "Permission to use Write has been denied"})

	v, _ := auditOf(t, &Server{Vault: vault}, "/api/audit")
	if v.Total != 2 || len(v.Records) != 2 {
		t.Fatalf("想要 2 条，得到 %d/%d", v.Total, len(v.Records))
	}
	// 最新在上：看板第一眼看到的是最近发生的事。
	if v.Records[0].ID != "b" || v.Records[1].ID != "a" {
		t.Errorf("顺序不对：%s, %s", v.Records[0].ID, v.Records[1].ID)
	}
	// 五样齐（谁 / 何时 / 对谁 / 类别 / 结果）+ 派生。
	b := v.Records[0]
	if b.Actor != "alice" || b.At == "" || b.Action != audit.ActionWrite ||
		b.Band != audit.ResultDenied || b.Zone != audit.ZoneDomain || b.Actee != "logistics" {
		t.Errorf("字段没齐：%+v", b)
	}
	if v.Bands.Denied != 1 || v.Bands.OK != 1 {
		t.Errorf("四档计数不对：%+v", v.Bands)
	}
	if v.Scopes.Cross != 1 || v.Scopes.Domain != 2 {
		t.Errorf("落点计数不对：%+v", v.Scopes)
	}
	if !b.HasWhy {
		t.Error("有原话时要给标记（看板据此提示去 CLI 看）")
	}
}

// 看板是观测面：**不许**把本机布局摊出来。
// 流水里的 object 是本机绝对路径，`why` / `detail` 是自由文本（夹着路径）——
// 两者都不能原样出现在这一页。
func TestAuditDoesNotLeakLocalLayout(t *testing.T) {
	vault := auditFixture(t)
	secretDir := filepath.Join(t.TempDir(), "Users", "somebody", ".claude")
	auditWrite(t, vault, audit.Record{ID: "a", At: "2026-10-08T10:00:00+08:00", Actor: "alice",
		Action: audit.ActionRead, Object: filepath.Join(secretDir, "settings.json"),
		Result: audit.ResultOK, Why: "File does not exist. Note: your current working directory is " + secretDir,
		Detail: "ls -la " + secretDir})

	_, raw := auditOf(t, &Server{Vault: vault}, "/api/audit")
	if strings.Contains(raw, "somebody") {
		t.Errorf("看板把本机路径摊出来了：%s", raw)
	}
	v, _ := auditOf(t, &Server{Vault: vault}, "/api/audit")
	if got := v.Records[0].Object; !strings.HasPrefix(got, "…/") {
		t.Errorf("vault 外的目标要归一成 …/ 开头，得到 %q", got)
	}
	if v.Records[0].Why != "" || v.Records[0].Detail != "" {
		t.Error("自由文本不该带到看板（scrub 自由文本永远做不干净）")
	}
	// vault 内的目标：保留结构、换成 <vault> 前缀。
	auditWrite(t, vault, audit.Record{ID: "b", At: "2026-10-08T12:00:00+08:00", Actor: "alice",
		Action: audit.ActionRead, Object: filepath.Join(vault, "projects", "z.md"), Result: audit.ResultOK})
	v2, raw2 := auditOf(t, &Server{Vault: vault}, "/api/audit")
	if got := v2.Records[0].Object; got != "<vault>/projects/z.md" {
		t.Errorf("vault 内要归一成 <vault>/…，得到 %q", got)
	}
	if strings.Contains(raw2, filepath.ToSlash(vault)) {
		t.Error("vault 根的绝对路径不该出现")
	}
}

func TestAuditFilters(t *testing.T) {
	vault := auditFixture(t)
	auditWrite(t, vault, audit.Record{ID: "a", At: "2026-10-08T10:00:00+08:00", Actor: "alice",
		Action: audit.ActionRead, Object: filepath.Join(vault, "projects", "x.md"), Result: audit.ResultOK})
	auditWrite(t, vault, audit.Record{ID: "b", At: "2026-10-08T11:00:00+08:00", Actor: "alice",
		Action: audit.ActionWrite, Object: filepath.Join(vault, "shipments", "y.md"), Result: audit.ResultDenied})
	auditWrite(t, vault, audit.Record{ID: "c", At: "2026-10-08T12:00:00+08:00", Actor: "bob",
		Action: audit.ActionRead, Object: filepath.Join(vault, "shipments", "z.md"), Result: audit.ResultOK})

	s := &Server{Vault: vault}
	v, _ := auditOf(t, s, "/api/audit?result=denied")
	if v.Total != 1 || v.Records[0].ID != "b" {
		t.Errorf("--result 筛选不对：total=%d", v.Total)
	}
	v, _ = auditOf(t, s, "/api/audit?actor=bob")
	if v.Total != 1 || v.Records[0].ID != "c" {
		t.Errorf("--actor 筛选不对：total=%d", v.Total)
	}
	// alice 在 trade 域：写 shipments（logistics 域）算跨域；bob 在 logistics 域，读自己域不算。
	v, _ = auditOf(t, s, "/api/audit?cross=1")
	if v.Total != 1 || v.Records[0].ID != "b" {
		t.Errorf("跨域筛选不对：total=%d，%+v", v.Total, v.Records)
	}
	v, _ = auditOf(t, s, "/api/audit?limit=1")
	if len(v.Records) != 1 || v.Total != 3 {
		t.Errorf("limit 要在截断 records 的同时保留 total：len=%d total=%d", len(v.Records), v.Total)
	}
	if _, err := json.Marshal(v); err != nil {
		t.Fatal(err)
	}
}

// 读不懂的行必须报出来，不许静默跳过（少一行就可能让一次行使看起来没发生）。
func TestAuditReportsBadLines(t *testing.T) {
	vault := t.TempDir()
	dir := filepath.Join(vault, audit.DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-10.alice.jsonl"), []byte("{ 坏行\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, _ := auditOf(t, &Server{Vault: vault}, "/api/audit")
	if len(v.Bad) != 1 {
		t.Errorf("坏行要逐条报，得到 %v", v.Bad)
	}
	if strings.Contains(strings.Join(v.Bad, ""), filepath.ToSlash(vault)) {
		t.Error("坏行只报 文件名:行号，不带本机路径")
	}
}

func TestAuditRejectsBadLimit(t *testing.T) {
	v, _ := auditOf(t, &Server{Vault: t.TempDir()}, "/api/audit?limit=abc")
	if v.Error == "" {
		t.Error("limit 不是正整数时要说出来")
	}
	if http.StatusOK == 0 {
		t.Fatal("unreachable")
	}
}

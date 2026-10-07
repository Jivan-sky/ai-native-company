package board

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// assetsFixture 造一份「真相源 + 数据目录里有东西」的现场。
func assetsFixture(t *testing.T) (vault string) {
	t.Helper()
	vault = t.TempDir()
	if err := os.CopyFS(vault, os.DirFS(fixturePath(t, "domains"))); err != nil {
		t.Fatal(err)
	}
	return vault
}

func assetsOf(t *testing.T, s *Server) (AssetsView, string) {
	t.Helper()
	rec := get(t, s.Handler(), "/api/assets")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200：%s", rec.Code, rec.Body.String())
	}
	var v AssetsView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("不是合法 JSON：%v", err)
	}
	if v.Schema != AssetsSchema {
		t.Fatalf("schema=%q，期望 %s", v.Schema, AssetsSchema)
	}
	return v, rec.Body.String()
}

// 目录里刚放进去的东西，这一页要数得出来 —— 而且只出相对路径 + 明说这不是沉淀。
func TestAssetsCountsWhatIsThere(t *testing.T) {
	vault := assetsFixture(t)
	dir := "shipments"
	if err := os.MkdirAll(filepath.Join(vault, dir, "2026-10"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.md", filepath.Join("2026-10", "b.md")} {
		if err := os.WriteFile(filepath.Join(vault, dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	v, body := assetsOf(t, &Server{Vault: vault})
	if !v.Wired {
		t.Fatalf("真相源读得动却报 wired=false：%s", v.Error)
	}
	var got *AssetsDir
	for i := range v.Dirs {
		if v.Dirs[i].Dir == dir {
			got = &v.Dirs[i]
		}
	}
	if got == nil {
		t.Fatalf("%s 是真相源里的数据目录，视图里必须有它：%+v", dir, v.Dirs)
	}
	if got.Files < 2 {
		t.Fatalf("%s 底下至少有 2 个文件，实际 %d（夹具里本来也可能有东西，所以是下限）", dir, got.Files)
	}
	if len(got.Recent) == 0 || got.Newest == "" {
		t.Fatalf("要回显「最近动的文件」，实际 recent=%v newest=%q", got.Recent, got.Newest)
	}
	for _, f := range got.Recent {
		if filepath.IsAbs(f.Path) {
			t.Fatalf("回显的路径必须是相对的，实际 %q", f.Path)
		}
	}
	if strings.Contains(body, vault) {
		t.Fatalf("响应里带出了 vault 绝对路径：\n%s", body)
	}
	if !strings.Contains(v.Note, "不是沉淀") {
		t.Fatalf("这一页最容易犯的错就是把「有文件」当成「有资产」，必须在 note 里说死：%q", v.Note)
	}
}

// 没东西的目录照回 0 —— 「还没开始沉淀」是个真实状态，不是错误。
func TestAssetsEmptyDirsAreFine(t *testing.T) {
	v, _ := assetsOf(t, &Server{Vault: assetsFixture(t)})
	if len(v.Dirs) == 0 {
		t.Fatal("真相源里声明了数据目录，就算空也要列出来")
	}
	for _, d := range v.Dirs {
		if d.Files == 0 && (len(d.Recent) != 0 || d.Newest != "") {
			t.Errorf("%s 一个文件都没有，却在回显最近文件：%+v", d.Dir, d)
		}
	}
}

// 到顶就翻 truncated 旗，如实说「只数到这里」。
func TestScanDirTruncatesHonestly(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"1.md", "2.md", "3.md"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d := scanDir(root, "d", "", 2)
	if d.Files != 2 {
		t.Fatalf("limit=2 时应当数到 2 个就停，实际 %d", d.Files)
	}
	if !d.Truncated {
		t.Fatal("数到上限要翻 truncated 旗 —— 不翻就等于把「没数完」说成「就这些」")
	}
	if len(d.Recent) > 2 {
		t.Fatalf("回显条数不该超过数到的条数：%+v", d.Recent)
	}
}

// 最近动的排前面；同一秒的按路径排 —— 同一份现场两次刷新要一致。
func TestScanDirOrdersByNewest(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	for _, n := range []string{"old.md", "new.md"} {
		p := filepath.Join(root, n)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := old
		if n == "new.md" {
			mt = time.Now()
		}
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	a := scanDir(root, "d", "", 10)
	b := scanDir(root, "d", "", 10)
	if len(a.Recent) != 2 || a.Recent[0].Path != "new.md" {
		t.Fatalf("最近动的该排第一，实际 %+v", a.Recent)
	}
	if a.Recent[0].Path != b.Recent[0].Path || a.Newest != b.Newest {
		t.Fatalf("两次扫结果不一致：%+v vs %+v", a.Recent, b.Recent)
	}
}

func TestAssetsBadVault(t *testing.T) {
	v, _ := assetsOf(t, &Server{Vault: t.TempDir()})
	if v.Wired || v.Error == "" {
		t.Fatalf("空目录读不出组织时该 wired=false + 说明原因，实际 wired=%v err=%q", v.Wired, v.Error)
	}
}

func TestAssetsIsReadOnly(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/assets", nil)
	rec := httptest.NewRecorder()
	(&Server{Vault: fixturePath(t, "domains")}).Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 %d，期望 405", rec.Code)
	}
}

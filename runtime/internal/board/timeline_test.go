package board

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/timeline"
)

// tlFixture 造一个只放了 timeline 的 vault。这一页不读 org 真相源，
// 所以不用 domains 夹具 —— 少依赖一层，测的就是这一页自己的口径。
func tlFixture(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func tlWrite(t *testing.T, vault, file, body string) {
	t.Helper()
	dir := filepath.Join(vault, timeline.DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tlOf(t *testing.T, s *Server, path string) (TimelineView, string) {
	t.Helper()
	rec := get(t, s.Handler(), path)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200：%s", rec.Code, rec.Body.String())
	}
	var v TimelineView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("不是合法 JSON：%v", err)
	}
	if v.Schema != TimelineSchema {
		t.Fatalf("schema=%q，期望 %s", v.Schema, TimelineSchema)
	}
	return v, rec.Body.String()
}

// 刚 init 的机器：没人记过 = 空时间线，不是错。空数组要是 [] 不是 null
// （前端 .length / .map 会崩）。
func TestTimelineEmptyIsNotAnError(t *testing.T) {
	v, body := tlOf(t, &Server{Vault: tlFixture(t)}, "/api/timeline")
	if !v.Wired {
		t.Fatalf("目录不存在应当算「还没开始」而不是没接入：%s", v.Error)
	}
	if v.Error != "" {
		t.Fatalf("空时间线不该报错：%s", v.Error)
	}
	if v.Total != 0 || len(v.Cases) != 0 || len(v.Bad) != 0 {
		t.Fatalf("want 空，got total=%d cases=%d bad=%v", v.Total, len(v.Cases), v.Bad)
	}
	for _, k := range []string{`"cases": []`, `"bad": []`} {
		if !strings.Contains(body, k) {
			t.Errorf("空数组要出 [] 不是 null，缺 %s：\n%s", k, body)
		}
	}
	if len(v.Missing) == 0 {
		t.Error("答不了的部分（审计 / 谁能写）要如实列在 missing，不能静默省略")
	}
}

// 折叠：一个 case 多行 → 一行，状态取最后一条，条数是全量。
func TestTimelineFoldsAndCounts(t *testing.T) {
	vault := tlFixture(t)
	tlWrite(t, vault, "2026-10.demo-alice.jsonl", strings.Join([]string{
		`{"id":"a1","case":"c1","at":"2026-10-01T09:00:00+08:00","status":"running","title":"第一步","to":"Alice"}`,
		`{"id":"a2","case":"c1","at":"2026-10-03T09:00:00+08:00","status":"blocked","title":"卡住了"}`,
		`{"id":"b1","at":"2026-10-02T09:00:00+08:00","status":"done","title":"独立一条"}`,
		`{"id":"d1","at":"2026-10-04T09:00:00+08:00","status":"waiting","title":"词表外的词"}`,
	}, "\n")+"\n")

	v, _ := tlOf(t, &Server{Vault: vault}, "/api/timeline")
	if v.Entries != 4 {
		t.Errorf("entries = %d, want 4", v.Entries)
	}
	if v.Total != 3 {
		t.Fatalf("折出 %d 个 case, want 3", v.Total)
	}
	got := v.Counts
	if got.Green != 1 || got.Yellow != 0 || got.Red != 1 || got.Unknown != 1 {
		t.Errorf("counts = %+v, want 绿1 黄0 红1 灰1", got)
	}
	if v.Cases[0].Key != "d1" {
		t.Errorf("按最后活动倒序，want d1（10-04）在最前，got %s", v.Cases[0].Key)
	}
	if v.Cases[0].Status != "waiting" || v.Cases[0].Band != timeline.BandUnknown {
		t.Errorf("认不出的词要原样保留 + 灰档，got %s/%s", v.Cases[0].Status, v.Cases[0].Band)
	}
	var c1 *TimelineCaseView
	for i := range v.Cases {
		if v.Cases[i].Key == "c1" {
			c1 = &v.Cases[i]
		}
	}
	if c1 == nil {
		t.Fatal("c1 不见了")
	}
	if c1.Entries != 2 || c1.Status != "blocked" || c1.Band != timeline.BandRed {
		t.Errorf("c1 = %+v，want 2 条 / blocked / red", c1)
	}
	if c1.To != "Alice" {
		t.Errorf("后面那条没写 to 时要沿用上一条，got %q", c1.To)
	}
}

// 坏行：逐条报出来（少一行就可能把「卡住」看成「没事」），且**不带本机路径**。
func TestTimelineBadLinesReportedAndNoLocalPaths(t *testing.T) {
	vault := tlFixture(t)
	tlWrite(t, vault, "2026-10.demo-alice.jsonl", strings.Join([]string{
		`{"id":"ok","at":"2026-10-01T09:00:00+08:00","status":"done","title":"好行"}`,
		`不是 JSON`,
	}, "\n")+"\n")

	v, body := tlOf(t, &Server{Vault: vault}, "/api/timeline")
	if len(v.Bad) != 1 || v.Bad[0] != "2026-10.demo-alice.jsonl:2" {
		t.Fatalf("bad = %v, want [2026-10.demo-alice.jsonl:2]", v.Bad)
	}
	if v.Entries != 1 {
		t.Errorf("坏行不该影响好行，entries = %d", v.Entries)
	}
	if strings.Contains(body, vault) || strings.Contains(body, strings.ReplaceAll(vault, `\`, `\\`)) {
		t.Errorf("看板不外泄本机路径，实际回显里有：\n%s", body)
	}
}

// 分页：next_before 能把第二页翻出来，且两页不重不漏。
func TestTimelinePaging(t *testing.T) {
	vault := tlFixture(t)
	var lines []string
	for i := 1; i <= 5; i++ {
		lines = append(lines, `{"id":"e`+string(rune('0'+i))+`","at":"2026-10-0`+string(rune('0'+i))+`T09:00:00+08:00","status":"done","title":"第 `+string(rune('0'+i))+` 条"}`)
	}
	tlWrite(t, vault, "2026-10.demo-alice.jsonl", strings.Join(lines, "\n")+"\n")

	v1, _ := tlOf(t, &Server{Vault: vault}, "/api/timeline?limit=2")
	if len(v1.Cases) != 2 || v1.Total != 5 {
		t.Fatalf("第一页 = %d 条 / total %d, want 2 / 5", len(v1.Cases), v1.Total)
	}
	if v1.Next == "" {
		t.Fatal("还有下一页却没给 next_before")
	}
	v2, _ := tlOf(t, &Server{Vault: vault}, "/api/timeline?limit=2&before="+v1.Next)
	if len(v2.Cases) != 2 {
		t.Fatalf("第二页 = %d 条, want 2", len(v2.Cases))
	}
	seen := map[string]bool{}
	for _, c := range append(append([]TimelineCaseView{}, v1.Cases...), v2.Cases...) {
		if seen[c.Key] {
			t.Errorf("两页重复了 %s（分页必须不重）", c.Key)
		}
		seen[c.Key] = true
	}
	if len(seen) != 4 {
		t.Errorf("两页合计 %d 条, want 4（不重不漏）", len(seen))
	}
}

// 过滤与参数错：非法 limit 是「参数错」，不是 500 —— 看板不该整页崩。
func TestTimelineFiltersAndBadParams(t *testing.T) {
	vault := tlFixture(t)
	tlWrite(t, vault, "2026-10.demo-alice.jsonl", strings.Join([]string{
		`{"id":"a","at":"2026-10-01T09:00:00+08:00","status":"done","title":"绿的"}`,
		`{"id":"b","at":"2026-10-02T09:00:00+08:00","status":"blocked","title":"红的"}`,
	}, "\n")+"\n")

	red, _ := tlOf(t, &Server{Vault: vault}, "/api/timeline?band=red")
	if len(red.Cases) != 1 || red.Cases[0].Key != "b" {
		t.Errorf("band=red 只该出一条，got %+v", red.Cases)
	}
	if red.Counts.Green != 1 {
		t.Errorf("counts 是**全局**的，不该被过滤带走：%+v", red.Counts)
	}

	bad, _ := tlOf(t, &Server{Vault: vault}, "/api/timeline?limit=0")
	if bad.Error == "" {
		t.Error("limit=0 该如实说参数不对")
	}
	if bad.Total != 0 || len(bad.Cases) != 0 {
		t.Error("参数错时不该顺带回数据")
	}

	badBefore, _ := tlOf(t, &Server{Vault: vault}, "/api/timeline?before=昨天")
	if badBefore.Error == "" {
		t.Error("before 不是 RFC3339 该如实说")
	}
}

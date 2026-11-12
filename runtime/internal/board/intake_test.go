package board

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/job"
)

// intakeServer：vault 在 <tmp>/vault，作业目录因此是 <tmp>/jobs —— 与真跑的口径一致。
func intakeServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "jobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Server{Vault: vault}, filepath.Join(root, "jobs")
}

func postIntake(t *testing.T, h http.HandlerFunc, body string) intakePreviewBody {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)))
	var out intakePreviewBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("回的不是 JSON：%v / %s", err, rec.Body.String())
	}
	return out
}

const goodSpec = `{"spec":{"schema":"anc.job/v1","name":"sweep-a","title":"每日巡检",
"schedule":{"every":"24h","at":"09:00","tz":"Asia/Shanghai"},"timeout":"60s",
"http":{"method":"GET","url":"https://example.com/x","auth":{"secret":"TOKEN","scheme":"bearer"}},
"write":{"path":"reports/x-{{date}}.md"}}}`

// 预演只算不写：点十次也不该在 jobs/ 下留任何东西。
func TestIntakePreviewDoesNotWrite(t *testing.T) {
	s, jobs := intakeServer(t)
	out := postIntake(t, s.handleIntakePreview, goodSpec)
	if !out.OK {
		t.Fatalf("预演该成功：%+v", out)
	}
	if out.Diff != "new" {
		t.Errorf("新作业的 diff 要 new，得到 %q", out.Diff)
	}
	if len(out.Facts) == 0 {
		t.Error("预演要给出会发生什么")
	}
	ents, _ := os.ReadDir(jobs)
	if len(ents) != 0 {
		t.Fatalf("预演写了盘：%v", ents)
	}
}

// 生效：写进去、读得回来、再预演一次就是 replace 且有 diff。
func TestIntakeApplyWritesAndParsesBack(t *testing.T) {
	s, jobs := intakeServer(t)
	out := postIntake(t, s.handleIntakeApply, goodSpec)
	if !out.OK {
		t.Fatalf("生效该成功：%+v", out)
	}
	p := filepath.Join(jobs, "sweep-a.json")
	sp, err := job.Load(p)
	if err != nil {
		t.Fatalf("写出来的读不回来：%v", err)
	}
	if sp.Name != "sweep-a" || sp.HTTP.URL == "" {
		t.Errorf("落盘内容不对：%+v", sp)
	}

	// 改一个字段再预演：必须说清「改了什么」
	changed := strings.Replace(goodSpec, "https://example.com/x", "https://example.com/y", 1)
	pv := postIntake(t, s.handleIntakePreview, changed)
	if pv.Diff != "replace" {
		t.Fatalf("已存在同名 spec 要判 replace，得到 %q", pv.Diff)
	}
	joined := strings.Join(pv.Changes, "\n")
	if !strings.Contains(joined, "http.url") || !strings.Contains(joined, "/y") {
		t.Errorf("diff 要说清改了哪个字段：%v", pv.Changes)
	}
}

// 替换必须留备份：退不回去的替换是耍流氓。
func TestIntakeReplaceKeepsBackup(t *testing.T) {
	s, jobs := intakeServer(t)
	if out := postIntake(t, s.handleIntakeApply, goodSpec); !out.OK {
		t.Fatalf("第一次该成功：%+v", out)
	}
	changed := strings.Replace(goodSpec, "https://example.com/x", "https://example.com/y", 1)
	if out := postIntake(t, s.handleIntakeApply, changed); !out.OK {
		t.Fatalf("第二次该成功：%+v", out)
	}
	baks, err := filepath.Glob(filepath.Join(jobs, ".backup", "sweep-a.*.json"))
	if err != nil || len(baks) != 1 {
		t.Fatalf("要有且只有一份备份，得到 %v（%v）", baks, err)
	}
	b, _ := os.ReadFile(baks[0])
	if !strings.Contains(string(b), "example.com/x") {
		t.Error("备份里该是上一版（/x），不是新的那版")
	}
}

// 名字当文件名用：路径穿越、大写、中文一律拒，且不写任何东西。
func TestIntakeRejectsBadNames(t *testing.T) {
	s, jobs := intakeServer(t)
	for _, name := range []string{"../evil", "A-B", "中文名", "", "a/b"} {
		body := strings.Replace(goodSpec, "sweep-a", name, 1)
		out := postIntake(t, s.handleIntakeApply, body)
		if out.OK {
			t.Errorf("名字 %q 该被拒", name)
		}
	}
	ents, _ := os.ReadDir(jobs)
	if len(ents) != 0 {
		t.Fatalf("被拒的请求一个字节都不该写：%v", ents)
	}
}

// 动作二选一：http 与 app 都空 = 跑起来什么也不干，是坏 spec。
func TestIntakeRequiresExactlyOneAction(t *testing.T) {
	s, _ := intakeServer(t)
	noAction := `{"spec":{"schema":"anc.job/v1","name":"x","title":"t","schedule":{"every":"24h"},"write":{}}}`
	if out := postIntake(t, s.handleIntakePreview, noAction); out.OK {
		t.Error("没有动作的 spec 该被拒")
	}
}

// 停用：enabled=false，spec 留着（删掉就看不见「曾经接过什么」）。
func TestIntakeStopKeepsSpec(t *testing.T) {
	s, jobs := intakeServer(t)
	if out := postIntake(t, s.handleIntakeApply, goodSpec); !out.OK {
		t.Fatalf("先要能落盘：%+v", out)
	}
	rec := httptest.NewRecorder()
	s.handleIntakeStop(rec, httptest.NewRequest(http.MethodPost, "/x", bytes.NewBufferString(`{"name":"sweep-a"}`)))
	var out intakePreviewBody
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.OK {
		t.Fatalf("停用该成功：%+v", out)
	}
	sp, err := job.Load(filepath.Join(jobs, "sweep-a.json"))
	if err != nil {
		t.Fatalf("停用后读不回来：%v", err)
	}
	if sp.EnabledOrDefault() {
		t.Error("停用后 enabled 要是 false")
	}
}

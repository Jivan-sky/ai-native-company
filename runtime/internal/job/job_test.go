package job

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "j.json")
	body := `{"schema":"anc.job/v1","name":"x","schedule":{"every":"24h"},"http":{"url":"http://x"},"write":{},"schdule":{}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("未知字段必须报错（spec 是契约，写错的字段不该静默失效）")
	}
}

func TestLoadValidates(t *testing.T) {
	cases := map[string]string{
		"没 name":       `{"schema":"anc.job/v1","schedule":{"every":"24h"},"http":{"url":"http://x"}}`,
		"name 非 ASCII": `{"schema":"anc.job/v1","name":"作业","schedule":{"every":"24h"},"http":{"url":"http://x"}}`,
		"没 url":        `{"schema":"anc.job/v1","name":"a","schedule":{"every":"24h"},"http":{}}`,
		"没 schedule":   `{"schema":"anc.job/v1","name":"a","http":{"url":"http://x"}}`,
		"every 非法":     `{"schema":"anc.job/v1","name":"a","schedule":{"every":"每天"},"http":{"url":"http://x"}}`,
	}
	for name, body := range cases {
		dir := t.TempDir()
		p := filepath.Join(dir, "j.json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil {
			t.Errorf("%s：应当报错", name)
		}
	}
}

// 一次成功 + 同一天再跑**跳过**（幂等），落盘是原子的。
func TestRunWritesOncePerKey(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if got := r.Header.Get("Authorization"); got != "token sekret" {
			t.Errorf("凭据没按 scheme 注入：%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"number":1,"title":"a|b","html_url":"u","created_at":"2026-11-01T00:00:00Z","labels":[{"name":"P0"}]}]`))
	}))
	defer srv.Close()

	vault, data := t.TempDir(), t.TempDir()
	s := Spec{
		Schema: Schema, Name: "t1",
		Schedule: Schedule{Every: "24h"},
		HTTP:     HTTP{URL: srv.URL, Auth: &Auth{Secret: "TOK", Scheme: "token"}},
		Write:    Write{Path: "reports/{{date}}.md", Render: "issues-md"},
	}
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	opts := Options{Vault: vault, DataDir: data, Secrets: map[string]string{"TOK": "sekret"}, Now: now}

	res, err := Run(context.Background(), s, opts)
	if err != nil || res.Status != "ok" {
		t.Fatalf("第一次应当成功：%+v %v", res, err)
	}
	if hits != 1 || res.Attempts != 1 {
		t.Fatalf("只该请求一次：hits=%d attempts=%d", hits, res.Attempts)
	}
	full := filepath.Join(vault, "reports", "2026-11-10.md")
	b, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("文件没落盘：%v", err)
	}
	if !strings.Contains(string(b), "P0") || !strings.Contains(string(b), "a\\|b") {
		t.Fatalf("摘要内容不对：\n%s", b)
	}

	// 同一天同一键：跳过，不再请求。
	res2, _ := Run(context.Background(), s, opts)
	if res2.Status != "skipped" || hits != 1 {
		t.Fatalf("幂等没生效：%+v hits=%d", res2, hits)
	}

	// 换一天：应当再跑一次。
	res3, _ := Run(context.Background(), s, Options{Vault: vault, DataDir: data, Secrets: opts.Secrets, Now: now.AddDate(0, 0, 1)})
	if res3.Status != "ok" || hits != 2 {
		t.Fatalf("换一天应当再跑：%+v hits=%d", res3, hits)
	}
}

// 4xx 不重试（契约问题，不是抖动）；5xx 重试到上限。
func TestRunRetryPolicy(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()
	s := Spec{Schema: Schema, Name: "t2", Schedule: Schedule{Every: "24h"},
		Retry: Retry{Max: 1, Backoff: "1ms"},
		HTTP:  HTTP{URL: srv.URL}, Write: Write{}}
	res, _ := Run(context.Background(), s, Options{Vault: t.TempDir(), DataDir: t.TempDir(),
		Now: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)})
	if res.Status != "ok" || hits != 2 {
		t.Fatalf("5xx 应当重试一次后成功：%+v hits=%d", res, hits)
	}

	hits = 0
	srv4 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(404)
	}))
	defer srv4.Close()
	s4 := Spec{Schema: Schema, Name: "t3", Schedule: Schedule{Every: "24h"},
		Retry: Retry{Max: 3, Backoff: "1ms"}, HTTP: HTTP{URL: srv4.URL}}
	res4, _ := Run(context.Background(), s4, Options{Vault: t.TempDir(), DataDir: t.TempDir(),
		Now: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)})
	if res4.Status != "failed" || hits != 1 {
		t.Fatalf("4xx 不该重试：%+v hits=%d", res4, hits)
	}
}

// 凭据不在 secrets 里：明确失败，且**不许**把 key 推进（否则今天这件事永远不做）。
func TestRunMissingSecretDoesNotAdvanceKey(t *testing.T) {
	vault, data := t.TempDir(), t.TempDir()
	s := Spec{Schema: Schema, Name: "t4", Schedule: Schedule{Every: "24h"},
		HTTP: HTTP{URL: "http://127.0.0.1:1/x", Auth: &Auth{Secret: "NOPE"}}}
	res, _ := Run(context.Background(), s, Options{Vault: vault, DataDir: data, Now: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)})
	if res.Status != "failed" || !strings.Contains(res.Why, "凭据") {
		t.Fatalf("缺凭据要说清楚：%+v", res)
	}
	st, ok := ReadState(data, "t4")
	if ok && st.LastOK {
		t.Fatal("失败不该被记成「跑过」")
	}
}

func TestRenderIssuesSkipsPullRequests(t *testing.T) {
	body := []byte(`[{"number":1,"title":"issue","labels":[{"name":"B"}],"created_at":"2026-11-01T00:00:00Z"},
	  {"number":2,"title":"pr","pull_request":{"url":"x"},"labels":[],"created_at":"2026-11-01T00:00:00Z"}]`)
	out := string(renderIssues(body, "j", time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)))
	if strings.Contains(out, "+1") || !strings.Contains(out, "含拉请求 1") {
		t.Fatalf("拉请求应当被剔除并计数：\n%s", out)
	}
}

// systemd unit 的内容判据：开机先跑一次（重启后自己回来）+ 之后按间隔跑。
func TestBuildSystemdUnits(t *testing.T) {
	svc, tm := BuildSystemdUnits("anc-jobs-acme", "/usr/local/bin/anc", "/srv/acme", "/srv/jobs", "/etc/anc/secrets.env", 300)
	for _, want := range []string{"Type=oneshot", "ExecStart=/usr/local/bin/anc job tick /srv/acme --dir /srv/jobs --secrets /etc/anc/secrets.env", "WorkingDirectory=/"} {
		if !strings.Contains(svc, want) {
			t.Errorf("service 缺 %q：\n%s", want, svc)
		}
	}
	for _, want := range []string{"OnBootSec=20s", "OnUnitActiveSec=300s", "Unit=anc-jobs-acme.service", "WantedBy=timers.target"} {
		if !strings.Contains(tm, want) {
			t.Errorf("timer 缺 %q：\n%s", want, tm)
		}
	}
}

// 执行要进两处账：audit（逐次，可对账）+ timeline（按 case 折叠，给看板/日报）。
// 跳过（幂等）**不写** —— 什么都没发生，写一条会让「做了几次」失真。
func TestRunRecordsExecutions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	vault, data := t.TempDir(), t.TempDir()
	s := Spec{Schema: Schema, Name: "rec1", Schedule: Schedule{Every: "24h"},
		HTTP: HTTP{URL: srv.URL}, Write: Write{Path: "out/{{date}}.txt"}}
	now := time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)

	res, err := Run(context.Background(), s, Options{Vault: vault, DataDir: data, Now: now})
	if err != nil || !res.Recorded {
		t.Fatalf("成功执行应当记账：%+v %v", res, err)
	}
	// audit：一个分片文件里能搜到 job 名与客体
	auditFiles, _ := filepath.Glob(filepath.Join(vault, "audit", "*.jsonl"))
	if len(auditFiles) != 1 {
		t.Fatalf("audit 应当有一份分片：%v", auditFiles)
	}
	b, _ := os.ReadFile(auditFiles[0])
	for _, want := range []string{"job:rec1", srv.URL, `"source":"job"`, `"result":"ok"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("audit 缺 %q：%s", want, b)
		}
	}
	// timeline：case = job:<name>
	tlFiles, _ := filepath.Glob(filepath.Join(vault, "timeline", "*.jsonl"))
	if len(tlFiles) != 1 {
		t.Fatalf("timeline 应当有一份分片：%v", tlFiles)
	}
	tb, _ := os.ReadFile(tlFiles[0])
	if !strings.Contains(string(tb), `"case":"job:rec1"`) || !strings.Contains(string(tb), `"kind":"execution"`) {
		t.Errorf("timeline 不对：%s", tb)
	}

	// 同一天再跑：跳过，不新增账
	beforeA, _ := os.ReadFile(auditFiles[0])
	res2, _ := Run(context.Background(), s, Options{Vault: vault, DataDir: data, Now: now})
	if res2.Status != "skipped" {
		t.Fatalf("应当跳过：%+v", res2)
	}
	afterA, _ := os.ReadFile(auditFiles[0])
	if string(beforeA) != string(afterA) {
		t.Fatal("跳过不该再写一条审计（会把「做了几次」算多）")
	}
}

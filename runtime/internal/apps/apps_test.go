package apps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(`{
	  "schema":"anc.app/v1","name":"hello","version":"1.0.0","entrypoint":"run.sh",
	  "run":{"timeout":"5s","image":"redis:7-alpine"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

type fakeRunner struct {
	dir  string
	hits int
}

func (f *fakeRunner) Run(ctx context.Context, e Exec) (Output, error) {
	f.hits++
	f.dir = e.Dir
	return Output{Code: 0, Stdout: "hi\n"}, nil
}

// 安装 → 未审批拒跑 → 审批 → 跑通 → 撤销 → 再拒。
func TestLifecycle(t *testing.T) {
	vault := t.TempDir()
	st, err := Install(vault, sampleDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "pending" || len(st.SHA256) != 64 {
		t.Fatalf("安装状态不对：%+v", st)
	}
	fr := &fakeRunner{}
	res, _ := Run(context.Background(), vault, "hello", Options{Runner: fr})
	if res.Status != "refused" || fr.hits != 0 {
		t.Fatalf("未审批不该执行：%+v hits=%d", res, fr.hits)
	}
	if _, err := Approve(vault, "hello", "alice"); err != nil {
		t.Fatal(err)
	}
	res, _ = Run(context.Background(), vault, "hello", Options{Runner: fr})
	if res.Status != "ok" || fr.hits != 1 {
		t.Fatalf("审批后应当跑：%+v hits=%d", res, fr.hits)
	}
	if _, err := Revoke(vault, "hello"); err != nil {
		t.Fatal(err)
	}
	res, _ = Run(context.Background(), vault, "hello", Options{Runner: fr})
	if res.Status != "refused" || fr.hits != 1 {
		t.Fatalf("撤销后不该再跑：%+v hits=%d", res, fr.hits)
	}
}

// 审批之后改一个字节：必须拒绝（审批的是那一个版本）。
func TestTamperRefused(t *testing.T) {
	vault := t.TempDir()
	st, err := Install(vault, sampleDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Approve(vault, "hello", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir, "run.sh"), []byte("#!/bin/sh\necho hacked\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{}
	res, _ := Run(context.Background(), vault, "hello", Options{Runner: fr})
	if res.Status != "refused" || fr.hits != 0 {
		t.Fatalf("改过内容必须拒绝：%+v hits=%d", res, fr.hits)
	}
	if !strings.Contains(res.Why, "不一致") {
		t.Fatalf("理由要写清：%q", res.Why)
	}
}

// 改名不能绕过哈希：路径也进哈希。
func TestHashIncludesNames(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.sh"), []byte("x"), 0o644)
	h1, _ := HashDir(dir)
	_ = os.Rename(filepath.Join(dir, "a.sh"), filepath.Join(dir, "b.sh"))
	h2, _ := HashDir(dir)
	if h1 == h2 {
		t.Fatal("改名后哈希不该相同（否则改名就能绕过审批）")
	}
}

// 没有沙箱就不跑：Runner 返回错，结果必须是 failed，而不是「裸跑成功」。
func TestNoSandboxRefuses(t *testing.T) {
	vault := t.TempDir()
	if _, err := Install(vault, sampleDir(t)); err != nil {
		t.Fatal(err)
	}
	_, _ = Approve(vault, "hello", "alice")
	res, _ := Run(context.Background(), vault, "hello", Options{Runner: failingRunner{}})
	if res.Status != "failed" || !strings.Contains(res.Why, "沙箱") {
		t.Fatalf("没有沙箱应当失败并说明：%+v", res)
	}
}

type failingRunner struct{}

func (failingRunner) Run(ctx context.Context, e Exec) (Output, error) {
	return Output{}, errNoSandbox
}

var errNoSandbox = &sandboxErr{}

type sandboxErr struct{}

func (*sandboxErr) Error() string { return "没有沙箱（找不到 docker）—— 拒绝执行" }

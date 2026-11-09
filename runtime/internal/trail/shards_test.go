package trail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/harness"
)

// 没有桥的机器上，trail 也要读得出来：按记录自带的 cwd 认领分片。
func TestShardSessionsClaimsByCwd(t *testing.T) {
	root := t.TempDir()
	mustDSH := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustDSH("sessions/a/session.v4.jsonl", dshFixture) // cwd = /Users/x/acme
	mustDSH("sessions/b/session.v4.jsonl", strings.Replace(dshFixture, "/Users/x/acme", "/Users/x/other", -1))

	fam := harness.Family{
		ID:     "dsh",
		Reader: ReaderDSH,
		Layout: harness.Layout{ShardDir: "sessions", ShardGlob: "session.v4.jsonl"},
	}

	got, ok := ShardSessions(fam, root, "acme-alice", "/Users/x/acme")
	if !ok {
		t.Fatal("这家应当有分片枚举")
	}
	if len(got) != 1 {
		t.Fatalf("cwd 对得上的只有一份，实际 %d：%+v", len(got), got)
	}
	if got[0].Project != "acme-alice" {
		t.Fatalf("认领没写回 project：%+v", got[0])
	}

	// 换一个对不上的 work_dir：一份都不该认领（但也不许把另一份当自己的）。
	none, ok := ShardSessions(fam, root, "acme-alice", "/Users/x/nope")
	if !ok || len(none) != 0 {
		t.Fatalf("cwd 对不上时不该认领：ok=%v got=%+v", ok, none)
	}
}

// 没有分片枚举的家族（claude 那种按项目推目录的）：返回 ok=false，让调用方**别**拿它当「没有会话」。
func TestShardSessionsNoShardPath(t *testing.T) {
	fam := harness.Family{ID: "claude", Reader: "claude-jsonl", Layout: harness.Layout{ProjectsDir: "projects"}}
	if got, ok := ShardSessions(fam, t.TempDir(), "p", "/x"); ok || got != nil {
		t.Fatalf("没有 shard_dir 的家族应当返回 ok=false：got=%v ok=%v", got, ok)
	}
}

// 按项目目录认记录的那条路（claude / codebuddy）：目录不在 = 还没发生过，不是读不到。
func TestDirSessionsMissingDirIsNotAnError(t *testing.T) {
	fam := harness.Family{ID: "claude", Reader: "claude-jsonl",
		Layout: harness.Layout{ProjectsDir: "projects", DirRule: "nonalnum-to-dash"}}
	got, ok := DirSessions(fam, t.TempDir(), "p", "/Users/x/acme")
	if !ok {
		t.Fatal("这家应当有「按项目目录找」这条路")
	}
	if len(got) != 0 {
		t.Fatalf("目录不在应当返回空：%+v", got)
	}
}

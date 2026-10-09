package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 分片枚举（shard_dir / shard_glob）是「这一家的分片不能按项目推目录」时用的口径：
// 先在记录根底下全扫出来，调用方再按记录里的 cwd 认领到项目。

func TestEnumerateShardsIsRecursive(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "sessions", "2026", "10", "09")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rollout-a.jsonl", "rollout-b.jsonl", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(deep, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fam, ok := Default().Lookup("codex")
	if !ok {
		t.Fatal("口径表里没有 codex 这家")
	}
	got, global, err := fam.EnumerateShards(root)
	if err != nil {
		t.Fatalf("EnumerateShards: %v", err)
	}
	if !global {
		t.Fatal("codex 声明了 shard_dir —— 这一路该走全量枚举")
	}
	if len(got) != 2 {
		t.Fatalf("该只列 rollout-*.jsonl 那两个，拿到 %v", got)
	}
	for _, p := range got {
		if !strings.HasSuffix(p, ".jsonl") || !strings.Contains(filepath.Base(p), "rollout-") {
			t.Errorf("列出来的不该是 %s", p)
		}
	}
}

// 没声明 shard_dir 的家：global=false（调用方走 TranscriptDir，不列全量）。
func TestEnumerateShardsOffForProjectScopedFamily(t *testing.T) {
	fam, ok := Default().Lookup("claude")
	if !ok {
		t.Fatal("口径表里没有 claude 这家")
	}
	got, global, err := fam.EnumerateShards(t.TempDir())
	if err != nil || global || len(got) != 0 {
		t.Errorf("claude 该按项目推目录：got=%v global=%v err=%v", got, global, err)
	}
}

// 记录目录不在**不是错误**：那是「这一家还没跑过会话」。
func TestEnumerateShardsMissingDirIsNotAnError(t *testing.T) {
	fam, ok := Default().Lookup("codex")
	if !ok {
		t.Fatal("口径表里没有 codex 这家")
	}
	got, global, err := fam.EnumerateShards(filepath.Join(t.TempDir(), "nope"))
	if err != nil || !global || len(got) != 0 {
		t.Errorf("目录不在该是空集且不报错：got=%v global=%v err=%v", got, global, err)
	}
}

// 成对出现：只写一半的表**必须报错**（写半边会被当成「没有分片」——那就是假绿）。
func TestLoadRejectsHalfDeclaredShardPair(t *testing.T) {
	raw := `{"schema":"anc.harnesses/v1","families":[{"id":"x","display":"X","record_format":"jsonl-plain",
		"reader":"claude-jsonl","layout":{"dir_rule":"nonalnum-to-dash","projects_dir":"projects",
		"main_file":"<id>.jsonl","shard_dir":"sessions"}}]}`
	p := filepath.Join(t.TempDir(), "h.json")
	if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("只有 shard_dir、没有 shard_glob 的表该被拒")
	} else if !strings.Contains(err.Error(), "成对") {
		t.Errorf("报错该说清是「成对出现」的问题，拿到 %v", err)
	}
}

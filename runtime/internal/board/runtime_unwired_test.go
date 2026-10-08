package board

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 声明了「还没接凭据」的 bot 在**看板**上也必须是灰 —— 跟 `anc probe` 用同一支算。
// 各算各的就会出现「CLI 说灰、看板说黄」两张互相打脸的报告（2026-10-08 实测踩到）。
func TestRuntimeUnwiredIsGray(t *testing.T) {
	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	if err := os.CopyFS(vault, os.DirFS(fixturePath(t, "one"))); err != nil {
		t.Fatal(err)
	}
	persona := filepath.Join(vault, "members", "devbot", "persona.md")
	b, err := os.ReadFile(persona)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(b), "disabled: false", "disabled: false\nunwired: true", 1)
	if patched == string(b) {
		t.Fatal("fixture 的 devbot persona 里没找到锚点")
	}
	if err := os.WriteFile(persona, []byte(patched), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := filepath.Join(root, "gateway", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "\n[[projects]]\nname = \"demo-alice\"\n\n[[projects]]\nname = \"demo-devbot\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	data := filepath.Join(root, "data")
	if err := os.MkdirAll(filepath.Join(data, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(data, "run", "api.sock"))
	if err != nil {
		t.Fatalf("起不了 unix socket：%v（路径长度 %d）", err, len(filepath.Join(data, "run", "api.sock")))
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if err := os.MkdirAll(filepath.Join(data, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeRuntimeSession(t, data, "demo-alice_00000001.json", time.Now())

	v, _ := runtimeOf(t, &Server{Vault: vault, DataDir: data})
	got := map[string]string{}
	for _, f := range v.Bots {
		got[f.Project] = string(f.State)
	}
	if got["demo-alice"] != "ok" {
		t.Fatalf("alice 刚回过话应当报绿，实际 %q（%+v）", got["demo-alice"], v.Bots)
	}
	if got["demo-devbot"] != "unwired" {
		t.Fatalf("声明 unwired 的成员在看板上应当是灰，实际 %q（%+v）", got["demo-devbot"], v.Bots)
	}
}

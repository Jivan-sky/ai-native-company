package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 这一份用例管一件事：**一段会话按哪一家的口径读，认的是桥里写的那一行**，
// 不是命令行那个旗标 —— 一个组织里不同 bot 用不同 harness 是常态，旗标一次只能给一家。
//
// 桥（网关的会话落盘）里写的是**它自己的叫法**（实测 cc-connect 写 claudecode / codex），
// 和我们表里的 id 不是一回事，所以翻译表也在数据里（agent_types）。
//
// 一次跑出四种情形，四段会话都在同一份桥文件里：
//
//  1. 桥写 codex    → 按 codex 口径 + codex 自己的记录根读（100）；
//  2. 桥写 openclaw → 同上，换根（200）；
//  3. 桥写 gemini（表里不认）→ **明说读不到**。关键负例：这个 id 在兜底那家的根里
//     是**有记录**的（999）—— 一旦有人改回「认不出就退回默认口径」，这里就会读出 999。
//     按错的口径读出来的数字比读不到更糟：读不到会被追，读错了不会。
//  4. 桥没写类型    → 退回 --harness 那一家（旗标本来就是干这个的）（300）。
//
// 合计必须是 600：多出来的 999 就是「认错家」的指纹。
func TestTrailCLIPicksFamilyPerSessionFromBridge(t *testing.T) {
	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	dataDir := filepath.Join(root, "data")
	sessDir := filepath.Join(dataDir, "sessions")
	gatewayDir := filepath.Join(root, "gateway")
	codexHome := filepath.Join(root, "codex-home")
	ocHome := filepath.Join(root, "oc-home")
	claudeHome := filepath.Join(root, "claude-home")
	workDir := filepath.Join(root, "work")
	for _, d := range []string{vault, sessDir, gatewayDir, workDir,
		filepath.Join(codexHome, "recs"), filepath.Join(ocHome, "recs"), filepath.Join(claudeHome, "recs")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeRecord(t, filepath.Join(codexHome, "recs", "cx-1.jsonl"), "cx-1", 100, 10)
	writeRecord(t, filepath.Join(ocHome, "recs", "oc-1.jsonl"), "oc-1", 200, 20)
	writeRecord(t, filepath.Join(claudeHome, "recs", "cl-9.jsonl"), "cl-9", 300, 30)
	writeRecord(t, filepath.Join(claudeHome, "recs", "cl-gem.jsonl"), "cl-gem", 999, 99)

	tableFile := filepath.Join(root, "h.json")
	writeFile(t, tableFile, `{"schema":"anc.harnesses/v1","families":[
	  {"id":"claude","display":"兜底那家","agent_types":["claudecode","claude"],"record_format":"jsonl-plain","reader":"codex-jsonl","layout":{"search_dir":"recs","search_glob":"<id>.jsonl"}},
	  {"id":"codex","display":"Codex 那家","agent_types":["codex"],"root_env":["ANC_TEST_CODEX"],"record_format":"jsonl-plain","reader":"codex-jsonl","layout":{"search_dir":"recs","search_glob":"<id>.jsonl"}},
	  {"id":"openclaw","display":"OpenClaw 那家","agent_types":["openclaw"],"root_env":["ANC_TEST_OC"],"record_format":"jsonl-plain","reader":"codex-jsonl","layout":{"search_dir":"recs","search_glob":"<id>.jsonl"}}]}`)
	t.Setenv("ANC_TEST_CODEX", codexHome)
	t.Setenv("ANC_TEST_OC", ocHome)

	writeFile(t, filepath.Join(gatewayDir, "config.toml"),
		"data_dir = \""+dataDir+"\"\n[[projects]]\nname = \"demo\"\n\n[projects.agent]\ntype = \"claudecode\"\n\n[projects.agent.options]\nwork_dir = \""+workDir+"\"\n")
	writeFile(t, filepath.Join(sessDir, "demo_0.json"), `{"sessions":{
	  "s1":{"id":"s1","agent_session_id":"cx-1","agent_type":"codex"},
	  "s2":{"id":"s2","agent_session_id":"oc-1","agent_type":"openclaw"},
	  "s3":{"id":"s3","agent_session_id":"cl-gem","agent_type":"gemini"},
	  "s4":{"id":"s4","agent_session_id":"cl-9"}},
	  "active_session":"s1","version":3}`)

	out, code := captureFileStdout(t, func() int {
		return cmdTrail([]string{vault, "--harness", "claude", "--claude-home", claudeHome,
			"--harnesses", tableFile, "--data", dataDir, "--json"})
	})
	// 第 3 段读不到 → 这份账不全 → 退出码 1（不是 0，也不是 2）。
	if code != 1 {
		t.Fatalf("有一段读不到就该 exit 1，实际 %d\n%s", code, out)
	}

	var doc struct {
		Sessions []struct {
			Session struct {
				Slot      string   `json:"slot"`
				AgentType string   `json:"agent_type"`
				Found     bool     `json:"found"`
				Problems  []string `json:"problems"`
				Usage     struct {
					In  int `json:"in"`
					Out int `json:"out"`
				} `json:"usage"`
			} `json:"session"`
		} `json:"sessions"`
		Totals struct {
			In int `json:"in"`
		} `json:"totals"`
		Records []struct {
			Harness string `json:"harness"`
			Root    string `json:"root"`
		} `json:"records"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--json 解不开：%v\n%s", err, out)
	}

	got := map[string]int{}
	found := map[string]bool{}
	for _, it := range doc.Sessions {
		got[it.Session.Slot] = it.Session.Usage.In
		found[it.Session.Slot] = it.Session.Found
		if it.Session.Slot != "s3" {
			continue
		}
		if it.Session.Found {
			t.Errorf("s3 的类型表里不认，不许读到东西；拿到 in=%d", it.Session.Usage.In)
		}
		joined := strings.Join(it.Session.Problems, "\n")
		if !strings.Contains(joined, "gemini") {
			t.Errorf("s3 的读不到要说清「桥里写的哪个类型」认不出，拿到：%s", joined)
		}
	}
	for slot, want := range map[string]int{"s1": 100, "s2": 200, "s3": 0, "s4": 300} {
		if got[slot] != want {
			t.Errorf("%s 该读到 in %d，拿到 %d（看见 999 就是退回了默认口径 —— 认错家的指纹）",
				slot, want, got[slot])
		}
	}
	if !found["s1"] || !found["s2"] || !found["s4"] {
		t.Errorf("s1/s2/s4 都该读到，拿到 %v", found)
	}
	if doc.Totals.In != 600 {
		t.Errorf("合计该是 600（100+200+300），拿到 %d —— 多出来的就是认错家", doc.Totals.In)
	}
	// 每一家用到的根各列一行：混编的组织里「哪段会话是从哪读的」正是这份账可不可信的前提。
	wantRoots := map[string]string{"codex": codexHome, "openclaw": ocHome, "claude": claudeHome}
	seen := map[string]string{}
	for _, r := range doc.Records {
		seen[r.Harness] = r.Root
	}
	for fam, want := range wantRoots {
		if seen[fam] != want {
			t.Errorf("%s 的记录根该是 %q，拿到 %q（全部：%v）", fam, want, seen[fam], seen)
		}
	}
}

// 文本模式下，用到的每一家各占一行 —— 人一眼能看出这一段是从哪个根、按哪一家的口径读出来的。
func TestTrailCLITextListsEveryRootInUse(t *testing.T) {
	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	sessDir := filepath.Join(root, "data", "sessions")
	gatewayDir := filepath.Join(root, "gateway")
	ocHome := filepath.Join(root, "oc-home")
	for _, d := range []string{vault, sessDir, gatewayDir, filepath.Join(root, "work"), filepath.Join(ocHome, "recs")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeRecord(t, filepath.Join(ocHome, "recs", "oc-1.jsonl"), "oc-1", 7, 1)

	tableFile := filepath.Join(root, "h.json")
	writeFile(t, tableFile, `{"schema":"anc.harnesses/v1","families":[
	  {"id":"claude","display":"兜底那家","agent_types":["claudecode"],"record_format":"jsonl-plain","reader":"codex-jsonl","layout":{"search_dir":"recs","search_glob":"<id>.jsonl"}},
	  {"id":"openclaw","display":"OpenClaw 那家","agent_types":["openclaw"],"root_env":["ANC_TEST_OC2"],"record_format":"jsonl-plain","reader":"codex-jsonl","layout":{"search_dir":"recs","search_glob":"<id>.jsonl"}}]}`)
	t.Setenv("ANC_TEST_OC2", ocHome)
	writeFile(t, filepath.Join(gatewayDir, "config.toml"),
		"[[projects]]\nname = \"demo\"\n\n[projects.agent.options]\nwork_dir = \""+filepath.Join(root, "work")+"\"\n")
	writeFile(t, filepath.Join(sessDir, "demo_0.json"),
		`{"sessions":{"s1":{"id":"s1","agent_session_id":"oc-1","agent_type":"openclaw"}},"active_session":"s1"}`)

	out, _ := captureFileStdout(t, func() int {
		return cmdTrail([]string{vault, "--harness", "claude", "--claude-home", filepath.Join(root, "claude-home"),
			"--harnesses", tableFile, "--data", filepath.Join(root, "data")})
	})
	if !strings.Contains(out, ocHome) {
		t.Errorf("用到的家的根该列出来，输出里没有 %q：\n%s", ocHome, out)
	}
	if !strings.Contains(out, "OpenClaw 那家") {
		t.Errorf("该标出这是按哪一家的口径读的：\n%s", out)
	}
}

// captureFileStdout 把一次调用的 stdout 落进文件再读回来。
//
// 不用 captureStdout：它走管道，输出超过管道缓冲就会卡死 —— JSON 输出很容易超，
// 而这个用例要断言的就是 JSON（卡死过一次，别再踩）。
func captureFileStdout(t *testing.T, f func() int) (string, int) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stdout.txt")
	fh, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = fh
	code := f()
	os.Stdout = old
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), code
}

// writeRecord 造一份 codex 形状的最小记录（1 轮：提问 + 一次调用的用量）。
func writeRecord(t *testing.T, path, id string, in, out int) {
	t.Helper()
	body := `{"timestamp":"2026-10-09T10:00:00.000Z","type":"session_meta","payload":{"session_id":"` + id + `"}}` + "\n" +
		`{"timestamp":"2026-10-09T10:00:01.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"t1","item":{"type":"UserMessage","content":[{"text":"你好"}]}}}` + "\n" +
		`{"timestamp":"2026-10-09T10:00:02.000Z","type":"token_usage_record","payload":{"turn_id":"t1","response_id":"r1","usage":{"input_tokens":` + strconv.Itoa(in) + `,"output_tokens":` + strconv.Itoa(out) + `}}}` + "\n"
	writeFile(t, path, body)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

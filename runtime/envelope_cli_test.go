package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEnvelope(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "e.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const envelopeGood = `{"id":"e1","ts":"2026-10-08T02:41:00+08:00","who":"alice",
  "on_behalf_of":"member:alice","scope":{"domain":"trade","project":"trade-q3"},"kind":"ask"}`

const envelopeBadWho = `{"id":"e2","ts":"2026-10-08T02:41:00+08:00","who":"nobody",
  "on_behalf_of":"member:alice","kind":"ask"}`

// 判据走完一圈：六问都打在输出里，且干净信封不报警。
func TestEnvelopeCLIAnswersSix(t *testing.T) {
	vault := copyFixture(t, "domains")
	file := writeEnvelope(t, envelopeGood)
	out, code := captureStdout(t, func() int { return cmdEnvelopeCheck([]string{vault, file}) })
	if code != 0 {
		t.Fatalf("干净信封应当 exit 0，实际 %d\n%s", code, out)
	}
	for _, want := range []string{"谁", "代谁", "哪块业务", "要什么", "证据在哪", "要不要人拍", "alice", "trade / trade-q3", "0 项发现"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里没有 %q：\n%s", want, out)
		}
	}
}

// 缺 who = 读不懂 = 拒收（退出码 1），不是「一条 warn 然后放行」。
func TestEnvelopeCLIRejectsIncomplete(t *testing.T) {
	vault := copyFixture(t, "domains")
	file := writeEnvelope(t, `{"id":"e3","ts":"2026-10-08T02:41:00+08:00"}`)
	if code := quiet(t, func() int { return cmdEnvelopeCheck([]string{vault, file}) }); code != 1 {
		t.Fatalf("缺 who 应当 exit 1，实际 %d", code)
	}
}

// 身份对不上：报 warn 但**不拦**（退出码 0）—— 默认档就是「先看见，不先拦」。
func TestEnvelopeCLIWarnsButDoesNotBlock(t *testing.T) {
	vault := copyFixture(t, "domains")
	file := writeEnvelope(t, envelopeBadWho)
	out, code := captureStdout(t, func() int { return cmdEnvelopeCheck([]string{vault, file}) })
	if code != 0 {
		t.Fatalf("非红档不该拦，实际 exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "envelope.who.unknown") {
		t.Errorf("应当报出 envelope.who.unknown：\n%s", out)
	}
}

// --json 是给机器吃的：同一份输入，结构化出口里的 fatal/warn 与人类可读版一致。
func TestEnvelopeCLIJSON(t *testing.T) {
	vault := copyFixture(t, "domains")
	file := writeEnvelope(t, envelopeBadWho)
	out, code := captureStdout(t, func() int { return cmdEnvelopeCheck([]string{vault, file, "--json"}) })
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{`"ok": true`, `"envelope.who.unknown"`, `"q": "谁"`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON 输出里没有 %s：\n%s", want, out)
		}
	}
}

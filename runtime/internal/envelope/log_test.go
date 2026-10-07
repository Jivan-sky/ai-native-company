package envelope

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anc/internal/org"
)

func rec(who, body string) Record {
	return Record{
		Envelope: Envelope{
			ID: "e1", TS: "2026-10-08T03:28:27+08:00", Who: who,
			OnBehalfOf: "member:" + who, Kind: KindReport, Body: body,
		},
		At: "2026-10-08T03:28:27+08:00",
	}
}

// 按 who 分片、按月分文件；一条一行；原信封一字不改地留着。
func TestAppendShardsByWho(t *testing.T) {
	data := t.TempDir()
	p1, err := Append(data, rec("alice", "一"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Append(data, rec("bob", "二")); err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(p1), "2026-10.alice.jsonl"; got != want {
		t.Fatalf("文件名应 %q，实际 %q", want, got)
	}
	if filepath.Dir(p1) != filepath.Join(data, DirName) {
		t.Fatalf("应当落在 <data>/%s 下，实际 %s", DirName, p1)
	}
	b, err := os.ReadFile(p1)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 1 {
		t.Fatalf("应当只有一行，实际 %d", len(lines))
	}
	var got Record
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("落盘的不是合法 JSONL：%v", err)
	}
	if got.Envelope.Body != "一" || got.Envelope.Who != "alice" || got.At == "" {
		t.Fatalf("落盘内容不对：%+v", got)
	}
}

// 追加不覆盖：同一个人第二封信进的是同一个文件的**第二行**。
func TestAppendIsAppendOnly(t *testing.T) {
	data := t.TempDir()
	p, err := Append(data, rec("alice", "一"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Append(data, rec("alice", "二")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if n := strings.Count(strings.TrimSpace(string(b)), "\n") + 1; n != 2 {
		t.Fatalf("应当两行，实际 %d", n)
	}
}

func TestAppendRejectsIncomplete(t *testing.T) {
	data := t.TempDir()
	cases := []struct {
		name string
		rec  Record
	}{
		{"缺 id", Record{Envelope: Envelope{TS: "2026-10-08T03:28:27+08:00", Who: "alice"}, At: "2026-10-08T03:28:27+08:00"}},
		{"缺 who", Record{Envelope: Envelope{ID: "e", TS: "2026-10-08T03:28:27+08:00"}, At: "2026-10-08T03:28:27+08:00"}},
		{"缺 at", Record{Envelope: Envelope{ID: "e", TS: "2026-10-08T03:28:27+08:00", Who: "alice"}}},
		{"at 不是 RFC3339", Record{Envelope: Envelope{ID: "e", TS: "2026-10-08T03:28:27+08:00", Who: "alice"}, At: "今天"}},
	}
	for _, c := range cases {
		if _, err := Append(data, c.rec); err == nil {
			t.Errorf("%s：应当拒收", c.name)
		}
	}
	// 一条都没落。
	names, _ := os.ReadDir(filepath.Join(data, DirName))
	if len(names) != 0 {
		t.Fatalf("拒收的信不该留下文件，实际 %d 个", len(names))
	}
}

// 中文 who 压不出文件名 → 报错，而不是写出一个怪文件（同 timeline 的口径）。
func TestAppendRejectsUnusableWho(t *testing.T) {
	if _, err := Append(t.TempDir(), rec("张三", "x")); err == nil {
		t.Fatal("压不出文件名的 who 应当报错")
	}
}

func TestToFindings(t *testing.T) {
	got := ToFindings([]org.Issue{{Rule: "envelope.who.unknown", Level: org.LevelWarn, Msg: "m"}})
	if len(got) != 1 || got[0].Rule != "envelope.who.unknown" || got[0].Level != "warn" {
		t.Fatalf("换算不对：%+v", got)
	}
	if len(ToFindings(nil)) != 0 {
		t.Fatal("空输入应当出空切片")
	}
}

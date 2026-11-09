package trail

// 标定的「第三方复跑」那一格：黄金样本 + 期望值 + sha256 都在 testdata/calib，
// 期望值由**另一套实现**（dev/calib/usage.py / oracle.py）算出来，不是跑本读取器抄的。
//
// 任何人 clone 之后 `go test -run Calib ./internal/trail` 就该得到同样的结论；
// 样本一变（哪怕一个字节），sha256 先红 —— 防止「悄悄改样本让测试变绿」。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"anc/internal/harness"
)

type calibManifest struct {
	Schema   string `json:"schema"`
	Fixtures []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Family string `json:"family"`
	} `json:"fixtures"`
	Expected map[string]struct {
		Turns    int            `json:"turns"`
		Usage    Usage          `json:"usage"`
		Acts     int            `json:"acts"`
		Tools    map[string]int `json:"tools"`
		Failures int            `json:"failures"`
		Cwd      string         `json:"cwd"`
		ID       string         `json:"id"`
	} `json:"expected"`
}

func TestCalibGoldenManifest(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "calib")
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("读不到 manifest：%v", err)
	}
	var m calibManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest 坏了：%v", err)
	}
	if m.Schema != "anc.calib/v1" {
		t.Fatalf("schema 不认识：%q", m.Schema)
	}
	if len(m.Fixtures) == 0 {
		t.Fatal("manifest 里一份样本都没有")
	}
	tb, err := harness.Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, fx := range m.Fixtures {
		t.Run(filepath.Base(fx.Path), func(t *testing.T) {
			p := filepath.Join(dir, fx.Path)
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("样本不见了：%v", err)
			}
			sum := sha256.Sum256(b)
			if got := hex.EncodeToString(sum[:]); got != fx.SHA256 {
				t.Fatalf("样本被改过（sha256 不符）\n  期望 %s\n  实际 %s\n  改了样本就要同时改 manifest 的 sha256 与期望值",
					fx.SHA256, got)
			}
			fam, ok := tb.Lookup(fx.Family)
			if !ok {
				t.Fatalf("口径表里没有 %q", fx.Family)
			}
			s := ReadShard(fam, p)
			if !s.Found {
				t.Fatalf("样本读不到：%v", s.Problems)
			}
			want, ok := m.Expected[fx.Path]
			if !ok {
				t.Fatalf("manifest 没给 %q 的期望值", fx.Path)
			}
			if len(s.Turns) != want.Turns {
				t.Errorf("轮数 %d，期望 %d", len(s.Turns), want.Turns)
			}
			if s.Usage != want.Usage {
				t.Errorf("用量 %+v，期望 %+v", s.Usage, want.Usage)
			}
			if len(s.Acts) != want.Acts {
				t.Errorf("行使 %d 次，期望 %d", len(s.Acts), want.Acts)
			}
			if len(s.Failures) != want.Failures {
				t.Errorf("失败 %d 条，期望 %d", len(s.Failures), want.Failures)
			}
			got := map[string]int{}
			for _, c := range s.Tools {
				got[c.Name] = c.Count
			}
			for name, n := range want.Tools {
				if got[name] != n {
					t.Errorf("工具 %s：%d 次，期望 %d", name, got[name], n)
				}
			}
			if s.Cwd != want.Cwd || s.ID != want.ID {
				t.Errorf("cwd/id = %q/%q，期望 %q/%q", s.Cwd, s.ID, want.Cwd, want.ID)
			}
		})
	}
}

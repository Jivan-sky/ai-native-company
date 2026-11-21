package effect

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyFixture 拷一份 testdata 里的 vault —— org.Load 对结构要求严，
// 手搓最小 vault 会一路红；用真夹具才测的是真行为。
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "orgs", name)
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("拷贝夹具失败：%v", err)
	}
	return dst
}

func write(t *testing.T, vault, rel, body string) {
	t.Helper()
	p := filepath.Join(vault, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 同类卡点按月次数与方向：down 才是好事。
func TestComputeTrendPerMonth(t *testing.T) {
	v := t.TempDir()
	write(t, v, "blockers/b1.md", "---\nid: b1\nstatus: open\nkind: 口径\ndomain: trade\nsince: 2026-10-05T10:00:00+08:00\n---\n\n# 口径不一致\n")
	write(t, v, "blockers/b2.md", "---\nid: b2\nstatus: open\nkind: 口径\ndomain: trade\nsince: 2026-11-06T10:00:00+08:00\n---\n\n# 又卡在口径\n")
	rep, err := Compute(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Trends) != 1 {
		t.Fatalf("该有 1 类趋势，拿到 %d", len(rep.Trends))
	}
	tr := rep.Trends[0]
	if len(tr.Months) != 2 || tr.Previous != 1 || tr.Last != 1 || tr.Direction != "flat" {
		t.Fatalf("两个月各 1 次该是 flat：%+v", tr)
	}
}

// 采纳数来自 roles/*.md 的 skills 列 —— 没有声明就是 0，并如实说"没人知道或没用"。
func TestComputeAdoptionFromRoles(t *testing.T) {
	v := t.TempDir()
	// 采纳只看 roles/*/persona.md 的 skills 列 —— 不需要整个 org 合规
	write(t, v, "roles/manager/persona.md", "---\nrole: manager\ntitle: 经理\nskills: [exp-1]\n---\n\n## 职责\n- 管账\n")
	write(t, v, "roles/devbot/persona.md", "---\nrole: devbot\ntitle: 开发\nskills:\n  - other\n---\n\n## 职责\n- 写码\n")
	write(t, v, "experiences/exp-1.md", "---\nid: exp-1\ntitle: 对账口径\nstatus: deployed\nscope: 财务\nversion: 2\nat: 2026-11-19T10:00:00+08:00\n---\n\n# 对账口径\n\n以附件三为准\n")
	rep, err := Compute(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skills) != 1 {
		t.Fatalf("该有 1 条已部署经验，拿到 %d（unknown=%v）", len(rep.Skills), rep.Unknown)
	}
	if rep.Skills[0].Adopted != 1 || len(rep.Skills[0].AdoptedBy) == 0 || rep.Skills[0].AdoptedBy[0] != "manager" {
		t.Logf("unknown=%v", rep.Unknown)
		t.Fatalf("采纳该是 manager：%+v", rep.Skills[0])
	}
}

// 算不出来的必须写进 Unknown —— 这一环最容易编数字。
func TestComputeDeclaresWhatItCannotMeasure(t *testing.T) {
	v := t.TempDir()
	rep, err := Compute(v)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rep.Unknown, " | ")
	for _, want := range []string{"卡点", "经验", "装配流水"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Unknown 里该说明「%s」算不出来，拿到：%s", want, joined)
		}
	}
}

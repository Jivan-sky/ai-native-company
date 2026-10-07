package org

import (
	"os"
	"path/filepath"
	"testing"
)

// 落点：一个项目一个子目录，目录名 = projects.md 的 slug。内容 ANC 一个字节都不解析。
func TestScanCharters(t *testing.T) {
	o, err := Load(fixture(t, "domains"))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Charters) != 1 {
		t.Fatalf("期望 1 个副本条目，实际 %d：%+v", len(o.Charters), o.Charters)
	}
	c, ok := o.Charter("trade-q3")
	if !ok {
		t.Fatal("取不到 trade-q3 的副本")
	}
	if c.Path != "charters/trade-q3" {
		t.Fatalf("副本路径不对：%q", c.Path)
	}
}

// 落点里的目录名对不上任何项目行 → 只告警，不拦：副本比表行**早到**是正常的
// （材料先丢进来、抽取还没做），那是进度问题不是错。
func TestCharterUnmatchedWarnsOnly(t *testing.T) {
	v := copyVault(t, "domains")
	writeAt(t, filepath.Join(v, ChartersDir, "还没立项", "材料.md"), "随手丢进来的材料\n")
	o, err := Load(v)
	if err != nil {
		t.Fatalf("对不上不该拦：%v", err)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{"charter.entry.unmatched"})
	// 发现 ≠ 消失：条目照旧在表里，只是被指出来。
	if _, ok := o.Charter("还没立项"); !ok {
		t.Fatal("对不上的副本条目应当照旧扫得到")
	}
}

// 目录不在 = 还没有副本，不是错。真源在客户侧，副本只是保障。
func TestChartersAbsentIsFine(t *testing.T) {
	o, err := Load(fixture(t, "one"))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Charters) != 0 || len(o.Warnings) != 0 {
		t.Fatalf("one 没有 charters/，不该有条目也不该有发现：%+v / %v", o.Charters, IssueStrings(o.Warnings))
	}
}

// 落点自己的说明文件不是项目条目：只扫子目录 —— 否则放一条 README 就报一个「对不上」。
func TestCharterFilesInDropPointAreIgnored(t *testing.T) {
	v := copyVault(t, "one")
	writeAt(t, filepath.Join(v, ChartersDir, "CLAUDE.md"), "# charters\n\n落点说明。\n")
	writeAt(t, filepath.Join(v, ChartersDir, "README.md"), "随手放的说明。\n")
	o, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Charters) != 0 {
		t.Fatalf("说明文件被当成了项目条目：%+v", o.Charters)
	}
	assertSameSet(t, "warn", ruleIDs(o.Warnings), []string{})
}

// 指纹要覆盖落点：多了 / 少了副本目录，重渲染判据必须变。
func TestCharterParticipatesInInputsHash(t *testing.T) {
	host := Host{VaultRoot: "/vault", HomesRoot: "/homes", DataDir: "/data"}
	v := copyVault(t, "domains")
	before, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(v, ChartersDir, "trade-q4"), 0o700); err != nil {
		t.Fatal(err)
	}
	after, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if before.InputsHash(host) == after.InputsHash(host) {
		t.Fatal("多了副本目录但指纹没变 —— 漂移检测会漏掉接入落点")
	}
}

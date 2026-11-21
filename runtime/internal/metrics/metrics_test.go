package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeMetrics(t *testing.T, v, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(v, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTimeline(t *testing.T, v string, lines ...string) {
	t.Helper()
	dir := filepath.Join(v, "timeline")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-11.alice.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const table = `# 口径

| id | 名称 | kind | 条件 | 窗口(天) | 负责人 | 说明 |
|---|---|---|---|---|---|---|
| done_7d | 近 7 天完成 | count | status in (done) | 7 | 记录 | 只说明忙不说明有用 |
| failed_7d | 近 7 天失败 | count | status in (failed) | 7 | 记录 | |
| achieve | 达成率 | ratio | done_7d / (done_7d + failed_7d) | 7 | 记录 | |
`

// 口径由客户定义：count 按 status 词表数，ratio 由两条 count 派生。
func TestComputeCountAndRatio(t *testing.T) {
	v := t.TempDir()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	writeMetrics(t, v, table)
	writeTimeline(t, v,
		`{"id":"a","at":"2026-11-20T10:00:00Z","status":"done","title":"x"}`,
		`{"id":"b","at":"2026-11-19T10:00:00Z","status":"done","title":"y"}`,
		`{"id":"c","at":"2026-11-18T10:00:00Z","status":"failed","title":"z"}`,
		`{"id":"d","at":"2026-10-01T10:00:00Z","status":"done","title":"old"}`,
	)
	rep, err := Compute(v, now)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Value{}
	for _, x := range rep.Values {
		byID[x.ID] = x
	}
	if byID["done_7d"].Value != 2 {
		t.Fatalf("近 7 天完成该是 2（老的不算），拿到 %+v", byID["done_7d"])
	}
	if byID["failed_7d"].Value != 1 {
		t.Fatalf("失败该是 1，拿到 %+v", byID["failed_7d"])
	}
	if got := byID["achieve"].Value; got < 0.66 || got > 0.67 {
		t.Fatalf("达成率该是 2/3，拿到 %v", got)
	}
}

// 分母为 0：算不出就说算不出，不报 0。
func TestComputeRatioUndefinedWhenDenominatorZero(t *testing.T) {
	v := t.TempDir()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	writeMetrics(t, v, table)
	writeTimeline(t, v) // 空时间线
	rep, _ := Compute(v, now)
	for _, x := range rep.Values {
		if x.ID == "achieve" {
			if !x.Undefined || x.Reason == "" {
				t.Fatalf("分母为 0 该标未定义并说明，拿到 %+v", x)
			}
			return
		}
	}
	t.Fatal("没算出 achieve")
}

// 没有定义就不上屏 —— 不退回硬编码口径。
func TestComputeWithoutDefinitionShowsNothing(t *testing.T) {
	v := t.TempDir()
	rep, err := Compute(v, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Values) != 0 {
		t.Fatalf("没有 metrics.md 时不该有数字，拿到 %+v", rep.Values)
	}
	if len(rep.Missing) == 0 {
		t.Fatal("要说明为什么没有数字")
	}
}

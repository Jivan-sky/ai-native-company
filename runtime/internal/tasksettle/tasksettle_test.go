package tasksettle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anc/internal/hot"
	"anc/internal/timeline"
)

type fakeSettleStore struct {
	m       map[string]hot.State
	getErr  error
	dropErr error
	dropped []string
}

func (f *fakeSettleStore) Get(id string) (hot.State, bool, error) {
	if f.getErr != nil {
		return hot.State{}, false, f.getErr
	}
	s, ok := f.m[id]
	return s, ok, nil
}

func (f *fakeSettleStore) DropCAS(id string, version int64) (bool, error) {
	if f.dropErr != nil {
		return false, f.dropErr
	}
	cur, ok := f.m[id]
	if !ok {
		return false, nil
	}
	// 与真实现同一条纪律：版本对不上就不动。
	if cur.Version != version {
		return false, nil
	}
	delete(f.m, id)
	f.dropped = append(f.dropped, id)
	return true, nil
}

var settleAt = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func settledTask() hot.State {
	return hot.State{
		ID: "t-1", Status: "done", By: "anc", From: "anc", To: "tradebot",
		Title: "处理单据", Note: "已答复客户", Domain: "trade", Project: "p-1",
		Session: "s-1", Intent: "queue", Version: 3,
		AsOf: "2026-10-18T11:00:00Z",
	}
}

func TestSettleTaskWritesEntryAndDrops(t *testing.T) {
	vault := t.TempDir()
	st := &fakeSettleStore{m: map[string]hot.State{"t-1": settledTask()}}

	res, err := Settle(st, vault, "t-1", Opts{At: settleAt})
	if err != nil {
		t.Fatalf("结账失败：%v", err)
	}
	if res.Already || res.Path == "" {
		t.Fatalf("应是新写一条：%+v", res)
	}
	e := res.Entry
	if e.Case != "t-1" || e.ID != "task-t-1" || e.Status != "done" || e.By != "tradebot" {
		t.Fatalf("落库字段不对：%+v", e)
	}
	if e.Title != "处理单据" || e.Domain != "trade" || e.Project != "p-1" || e.To != "tradebot" {
		t.Fatalf("任务字段没带过去：%+v", e)
	}
	if !strings.Contains(e.Detail, "已答复客户") || !strings.Contains(e.Detail, "会话 s-1") ||
		!strings.Contains(e.Detail, "v3") || !strings.Contains(e.Detail, "派单 anc") {
		t.Fatalf("detail 应带上正文 / 会话 / 版本：%q", e.Detail)
	}
	if len(st.dropped) != 1 || st.dropped[0] != "t-1" {
		t.Fatalf("落库后热层应清走：%v", st.dropped)
	}
	doc, err := timeline.Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Entries) != 1 || doc.Entries[0].Case != "t-1" {
		t.Fatalf("timeline 应有一条：%+v", doc.Entries)
	}
	if timeline.Band(doc.Entries[0].Status) != timeline.BandGreen {
		t.Fatalf("done 应落绿档：%s", doc.Entries[0].Status)
	}
}

func TestSettleTaskIsIdempotent(t *testing.T) {
	vault := t.TempDir()
	st := &fakeSettleStore{m: map[string]hot.State{"t-1": settledTask()}}
	if _, err := Settle(st, vault, "t-1", Opts{At: settleAt}); err != nil {
		t.Fatal(err)
	}
	res, err := Settle(st, vault, "t-1", Opts{At: settleAt.Add(time.Hour)})
	if err != nil {
		t.Fatalf("重复结账不该报错：%v", err)
	}
	if !res.Already || res.Path != "" {
		t.Fatalf("重复结账应只报已沉淀：%+v", res)
	}
	doc, err := timeline.Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Entries) != 1 {
		t.Fatalf("重复结账不该追加第二条：%d", len(doc.Entries))
	}
}

func TestSettleTaskRefusesOpenTask(t *testing.T) {
	vault := t.TempDir()
	queued := settledTask()
	queued.Status = "queued"
	st := &fakeSettleStore{m: map[string]hot.State{"t-1": queued}}
	if _, err := Settle(st, vault, "t-1", Opts{At: settleAt}); err == nil ||
		!strings.Contains(err.Error(), "还在队列里") {
		t.Fatalf("queued 默认不该结账：%v", err)
	}
	if len(st.dropped) != 0 {
		t.Fatalf("被拒时热层不许动：%v", st.dropped)
	}

	running := settledTask()
	running.Status = "running"
	st = &fakeSettleStore{m: map[string]hot.State{"t-1": running}}
	if _, err := Settle(st, vault, "t-1", Opts{At: settleAt}); err == nil ||
		!strings.Contains(err.Error(), "还在进行中") {
		t.Fatalf("running 默认不该结账：%v", err)
	}

	res, err := Settle(st, vault, "t-1", Opts{At: settleAt, Status: "cancelled", Note: "客户撤回"})
	if err != nil {
		t.Fatalf("显式给终态应能结账：%v", err)
	}
	if res.Entry.Status != "cancelled" || !strings.Contains(res.Entry.Detail, "客户撤回") {
		t.Fatalf("显式终态没落上：%+v", res.Entry)
	}
}

func TestSettleTaskKeepsHotWhenTimelineFails(t *testing.T) {
	vault := t.TempDir()
	dir := filepath.Join(vault, timeline.DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "2026-10.human.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	st := &fakeSettleStore{m: map[string]hot.State{"t-1": settledTask()}}
	_, err := Settle(st, vault, "t-1", Opts{At: settleAt, By: "human"})
	if err == nil || !strings.Contains(err.Error(), "落库失败") {
		t.Fatalf("落库失败应报出来：%v", err)
	}
	if len(st.dropped) != 0 {
		t.Fatalf("落库失败时热层一个字都不许动：%v", st.dropped)
	}
}

func TestSettleTaskReportsDropFailure(t *testing.T) {
	vault := t.TempDir()
	st := &fakeSettleStore{m: map[string]hot.State{"t-1": settledTask()}, dropErr: errors.New("redis down")}
	res, err := Settle(st, vault, "t-1", Opts{At: settleAt})
	if err == nil || !strings.Contains(err.Error(), "库已落") {
		t.Fatalf("清热层失败要说清已落库：%v", err)
	}
	if res.Path == "" {
		t.Fatalf("应把落库路径带回来：%+v", res)
	}
	doc, _ := timeline.Load(vault)
	if len(doc.Entries) != 1 {
		t.Fatalf("库应已落一条：%d", len(doc.Entries))
	}
}

// A1-1 普通备注：同案的一条进度备注**不能**让 settle 判成「早已沉淀」。
// 之前按 case 匹配，备注会被当结账回执，然后把还在跑的任务从热层删掉。
func TestSettleIgnoresSameCaseNote(t *testing.T) {
	vault := t.TempDir()
	id := "t-note"
	// 先写一条同 case 的进度备注（不是结账行：ID 不是 task-<id>）
	if _, err := timeline.Append(vault, timeline.Entry{
		ID: "note-1", Case: id, At: settleAt.Format(time.RFC3339),
		Kind: "note", Status: "running", By: "bob", Title: "进度：还在等对方回",
	}); err != nil {
		t.Fatal(err)
	}
	st := &fakeSettleStore{m: map[string]hot.State{
		id: {ID: id, Status: "running", Stage: hot.StageRunning, By: "anc", To: "bob", Title: "处理单据", Version: 2},
	}}

	_, err := Settle(st, vault, id, Opts{At: settleAt})
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("进行中的任务要报「还没结束」，得到 %v", err)
	}
	if _, still := st.m[id]; !still {
		t.Fatal("备注把活干掉了：任务被从热层删了")
	}
	doc, _ := timeline.Load(vault)
	if len(doc.Entries) != 1 {
		t.Fatalf("不该多写结账行，时间线应仍是 1 条，得到 %d", len(doc.Entries))
	}
}

// A1-2 交接先赢：请求基于 v2，但现场已经到 v3 —— 必须判冲突，且不写行、不清热层。
func TestSettleStaleVersionIsConflict(t *testing.T) {
	vault := t.TempDir()
	id := "t-stale"
	st := &fakeSettleStore{m: map[string]hot.State{
		id: {ID: id, Status: "running", Stage: hot.StageRunning, By: "anc", To: "bob", Title: "处理单据", Version: 3},
	}}
	_, err := Settle(st, vault, id, Opts{Status: "cancelled", By: "alice", ExpectedVersion: 2, At: settleAt})
	if !errors.Is(err, ErrStale) {
		t.Fatalf("版本过期要判冲突，得到 %v", err)
	}
	if len(st.dropped) != 0 {
		t.Error("冲突时不该清热层")
	}
	if doc, _ := timeline.Load(vault); len(doc.Entries) != 0 {
		t.Errorf("冲突时不该写时间线，得到 %d 条", len(doc.Entries))
	}
}

// 落库后才发现版本变了：终态行已写（事实不能撤），但热层那一条是别人的现场，不能删。
type racyStore struct{ inner *fakeSettleStore }

func (r *racyStore) Get(id string) (hot.State, bool, error) { return r.inner.Get(id) }
func (r *racyStore) DropCAS(id string, version int64) (bool, error) {
	// 模拟：读完到清理之间，有人交接把版本顶到 v9
	s := r.inner.m[id]
	s.Version = 9
	r.inner.m[id] = s
	return false, nil
}

func TestSettleDoesNotDropNewerState(t *testing.T) {
	vault := t.TempDir()
	id := "t-race"
	inner := &fakeSettleStore{m: map[string]hot.State{
		id: {ID: id, Status: "done", Stage: hot.StageDone, By: "anc", To: "bob", Title: "处理单据", Version: 2},
	}}
	_, err := Settle(&racyStore{inner: inner}, vault, id, Opts{At: settleAt})
	if err == nil {
		t.Fatal("清不掉要如实报错，不能假装清干净了")
	}
	if !errors.Is(err, ErrStale) {
		t.Errorf("错误里要能认出「现场变了」，得到 %v", err)
	}
	if _, still := inner.m[id]; !still {
		t.Error("把别人交接后的那一条删了")
	}
}

// A1-3 结账先赢：回执一旦落库，别的写入口（交接）就能看见它。
// 这里测的是那个共享判据：Receipt() 认的是**结账行自己的身份**，不是同 case 的任何一行。
func TestReceiptVisibleToOtherWriters(t *testing.T) {
	vault := t.TempDir()
	id := "t-handoff"
	st := &fakeSettleStore{m: map[string]hot.State{
		id: {ID: id, Status: "done", Stage: hot.StageDone, By: "anc", To: "bob", Title: "处理单据", Version: 2},
	}}
	// 落账之前：别的写入口看得到"没有回执"，可以交接
	if _, settled, err := Receipt(vault, id); err != nil || settled {
		t.Fatalf("还没结账时不该看见回执（settled=%v err=%v）", settled, err)
	}
	res, err := Settle(st, vault, id, Opts{At: settleAt})
	if err != nil {
		t.Fatal(err)
	}
	// 落账之后：交接必须看见它，并拿到原回执
	rec, settled, err := Receipt(vault, id)
	if err != nil || !settled {
		t.Fatalf("结账后要能查到回执（settled=%v err=%v）", settled, err)
	}
	if rec.ID != res.Entry.ID {
		t.Fatalf("回执身份要对得上：%q vs %q", rec.ID, res.Entry.ID)
	}
	if rec.Status == "" {
		t.Error("回执要带终态 —— 拒绝交接时得说清「已经结成了什么」")
	}
}

// A1-4 中断与重放：四个点位里最容易丢的三个（回执前 / 回执后未清队 / 清队后响应丢失）。
// 每个点位都模拟「进程重启」——重放靠 `Settle` 本身幂等，不依赖内存状态。
func TestSettleReplayAtInterruptPoints(t *testing.T) {
	// ① 回执前中断：什么都没写，重放要能正常结账
	vault := t.TempDir()
	st := &fakeSettleStore{m: map[string]hot.State{
		"t-a": {ID: "t-a", Status: "done", Stage: hot.StageDone, By: "anc", To: "bob", Title: "单据", Version: 1},
	}}
	if _, err := Settle(st, vault, "t-a", Opts{At: settleAt}); err != nil {
		t.Fatal(err)
	}

	// ② 回执后、清队前中断：热层还在、账上已有
	vault2 := t.TempDir()
	st2 := &fakeSettleStore{m: map[string]hot.State{
		"t-b": {ID: "t-b", Status: "done", Stage: hot.StageDone, By: "anc", To: "bob", Title: "单据", Version: 1},
	}}
	if _, err := Settle(st2, vault2, "t-b", Opts{At: settleAt}); err != nil {
		t.Fatal(err)
	}
	st2.m["t-b"] = hot.State{ID: "t-b", Status: "done", Stage: hot.StageDone, By: "anc", To: "bob", Title: "单据", Version: 1} // 模拟「没清掉」
	res, err := Settle(st2, vault2, "t-b", Opts{At: settleAt})
	if err != nil {
		t.Fatalf("重放该幂等成功：%v", err)
	}
	if !res.Already {
		t.Error("第二次要标 Already")
	}
	if _, still := st2.m["t-b"]; still {
		t.Error("重放要把那一刀补清")
	}

	// ③ 清队后、响应丢失：热层没了，账上还在 —— 重放要回原回执，不能再写一行
	res2, err := Settle(st2, vault2, "t-b", Opts{At: settleAt})
	if err != nil {
		t.Fatalf("队列已清时重放也该幂等：%v", err)
	}
	if !res2.Already || res2.Entry.ID != res.Entry.ID {
		t.Fatalf("要回原回执：%+v", res2.Entry)
	}
	doc, _ := timeline.Load(vault2)
	if n := len(doc.Entries); n != 1 {
		t.Fatalf("三个点位走完，账上只该有 1 条终态，实际 %d", n)
	}
}

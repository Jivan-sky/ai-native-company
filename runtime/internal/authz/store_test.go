package authz

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRevokeAndExpire(t *testing.T) {
	dir := t.TempDir()
	r := Revocations{Path: filepath.Join(dir, "revoked.json")}
	if r.Revoked("j-1") {
		t.Fatal("没撤过怎么是已撤")
	}
	if err := r.Revoke("j-1", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !r.Revoked("j-1") {
		t.Fatal("撤销没生效")
	}
	// 过期的撤销条目应当被清掉（令牌自己已经失效）
	if err := r.Revoke("j-old", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if r.Revoked("j-old") {
		t.Fatal("已过期的撤销条目不该再挡")
	}
}

// 这条是「预算 1 元、10 个并发只许成功 1 个」的最小版：预留 0.02、预算 0.02。
func TestBudgetReserveIsAtomic(t *testing.T) {
	b := Budgets{Path: filepath.Join(t.TempDir(), "b.json"), Reserve: 0.02}
	var ok int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _, err := b.TryReserve("j-1", 0.02)
			if err != nil {
				t.Errorf("预留报错：%v", err)
				return
			}
			if got {
				atomic.AddInt32(&ok, 1)
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("预算 0.02 只允许 1 次预留，实际 %d 次 —— 说明判断与扣款没在同一个锁里", ok)
	}
	if spent := b.Spent("j-1"); spent != 0.02 {
		t.Fatalf("已花应当 0.02，实际 %v", spent)
	}
}

// 结算多退少补；不知道实际花费时保留预留（不假装知道）。
func TestBudgetSettle(t *testing.T) {
	b := Budgets{Path: filepath.Join(t.TempDir(), "b.json"), Reserve: 0.02}
	if ok, _, _ := b.TryReserve("j-2", 1); !ok {
		t.Fatal("应当放行")
	}
	if err := b.Settle("j-2", 0.02, 0.005); err != nil {
		t.Fatal(err)
	}
	if spent := b.Spent("j-2"); spent < 0.0049 || spent > 0.0051 {
		t.Fatalf("结算应当只扣实际花掉的，实际 %v", spent)
	}
	if err := b.Settle("j-2", 0.005, 0); err != nil {
		t.Fatal(err)
	}
	if spent := b.Spent("j-2"); spent < 0.0049 || spent > 0.0051 {
		t.Fatalf("不知道实际花费时不该改账，实际 %v", spent)
	}
}

// 没有预算的票不许放行。
func TestBudgetZeroDenies(t *testing.T) {
	b := Budgets{Path: filepath.Join(t.TempDir(), "b.json")}
	if ok, _, err := b.TryReserve("j-3", 0); ok || err == nil {
		t.Fatal("没有预算应当拒绝并给理由")
	}
}

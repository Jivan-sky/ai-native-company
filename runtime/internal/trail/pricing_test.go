package trail

import (
	"strings"
	"testing"

	"anc/internal/pricing"
)

// 有价 → 按表算；没价 → 显式报缺，**不写 0 元**。
func TestPriceSession(t *testing.T) {
	tb := &pricing.Table{
		Schema: pricing.Schema, Currency: "USD", Unit: "per_1m_tokens",
		Entries: []pricing.Entry{{Model: "deepseek-flash", In: 0.1, Out: 0.2, CacheRead: 0.01}},
	}
	pricing.Arm(tb)
	t.Cleanup(func() { pricing.Arm(nil) })

	priced := &Session{Model: "deepseek-flash", Usage: Usage{In: 1000000, Out: 500000, CacheRead: 2000000}}
	PriceSession(priced)
	if priced.CostUSD != 0.1+0.1+0.02 || priced.PricedBy != "table" {
		t.Fatalf("按表算钱不对：%+v", priced)
	}

	// 没有条目：金额保持 0，但必须有一条 Problem 说清楚「无价格」。
	unknown := &Session{Model: "some-new-model", Usage: Usage{In: 1000}}
	PriceSession(unknown)
	if unknown.CostUSD != 0 || unknown.PricedBy != "" {
		t.Fatalf("没价就不许编：%+v", unknown)
	}
	found := false
	for _, p := range unknown.Problems {
		if strings.Contains(p, "没有价格表条目") {
			found = true
		}
	}
	if !found {
		t.Fatalf("无价格要显式报缺：%v", unknown.Problems)
	}

	// harness 自己给的金额不许被覆盖。
	fromRecord := &Session{Model: "deepseek-flash", CostUSD: 9.9, PricedBy: "record", Usage: Usage{In: 1}}
	PriceSession(fromRecord)
	if fromRecord.CostUSD != 9.9 {
		t.Fatalf("record 的金额被覆盖了：%+v", fromRecord)
	}
}

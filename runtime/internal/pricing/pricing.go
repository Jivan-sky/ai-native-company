// Package pricing —— 价格表：**数据**，不是代码。
//
// 三条口径（2026-11-10，议题 #12 的价格表那一格）：
//
//  1. **出厂表是空的。** 我们不知道各家 2026 年的挂牌价，也不该替客户猜；
//     编一个数比没有更坏 —— 它会让「成本」看起来是实测的。
//  2. **一模型一行，带生效日期与出处。** 价格随调价漂移，表必须能回答「这个数是哪天的价」。
//  3. **没有条目就显式「无价格」**，绝不按「相似模型」估。估算值一旦混进实测账里，
//     整份账就不可信了。
package pricing

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

//go:embed prices.json
var builtin embed.FS

// Schema 是这份表的版本号：字段就是契约。
const Schema = "anc.prices/v1"

// Entry 是一条模型价格。单位由表头的 Unit 决定（出厂是「每 100 万 token」，美元）。
type Entry struct {
	Model         string  `json:"model"`
	In            float64 `json:"in"`
	Out           float64 `json:"out"`
	CacheRead     float64 `json:"cache_read"`
	CacheWrite    float64 `json:"cache_write"`
	EffectiveFrom string  `json:"effective_from,omitempty"`
	Source        string  `json:"source,omitempty"`
}

// Table 是整份价格表。
type Table struct {
	Schema   string  `json:"schema"`
	Readme   string  `json:"_readme,omitempty"`
	Currency string  `json:"currency"`
	Unit     string  `json:"unit"`
	Entries  []Entry `json:"entries"`
}

// Load 读价格表：path 为空用内嵌那份（出厂空表）。
func Load(path string) (*Table, error) {
	var raw []byte
	var err error
	if strings.TrimSpace(path) == "" {
		raw, err = builtin.ReadFile("prices.json")
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var t Table
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("价格表不是合法 JSON：%v", err)
	}
	if t.Schema != Schema {
		return nil, fmt.Errorf("价格表 schema=%q，只认 %q（字段是契约，换版本要显式改）", t.Schema, Schema)
	}
	seen := map[string]bool{}
	for i, e := range t.Entries {
		m := strings.TrimSpace(e.Model)
		if m == "" {
			return nil, fmt.Errorf("第 %d 条没有 model", i+1)
		}
		if seen[strings.ToLower(m)] {
			return nil, fmt.Errorf("模型 %q 重复（一条模型一行，重复会让「取哪条」变成运气）", m)
		}
		seen[strings.ToLower(m)] = true
	}
	return &t, nil
}

// Lookup 按模型名取价格。大小写不敏感（记录里的写法各家不同）。
func (t *Table) Lookup(model string) (Entry, bool) {
	if t == nil {
		return Entry{}, false
	}
	want := strings.TrimSpace(strings.ToLower(model))
	if want == "" {
		return Entry{}, false
	}
	for _, e := range t.Entries {
		if strings.ToLower(strings.TrimSpace(e.Model)) == want {
			return e, true
		}
	}
	return Entry{}, false
}

// Cost 按这条价算钱。四个字段各自计价（缓存读/写本来就不是同价）。
// 单位是「每 100 万 token」——表头写了 Unit，这里不猜。
func (e Entry) Cost(in, out, cacheRead, cacheWrite int) float64 {
	const per = 1_000_000.0
	return (float64(in)*e.In + float64(out)*e.Out +
		float64(cacheRead)*e.CacheRead + float64(cacheWrite)*e.CacheWrite) / per
}

// active 是进程级生效的价格表。与脱敏表同一套做法：CLI 启动时落一次，
// 读取器不必各自接参数；测试直接 Arm 一份。
var active *Table

// Arm 落定进程级价格表。
func Arm(t *Table) { active = t }

// Active 取当前生效的表（可能是 nil = 没有表，那就谁都算不出钱）。
func Active() *Table { return active }

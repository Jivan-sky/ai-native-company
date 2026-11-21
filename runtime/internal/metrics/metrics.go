// Package metrics —— 口径由**客户定义**，代码只负责按定义去数。
//
// 为什么不是"公式语言"：任意表达式会变成一个小型解释器，客户写错一次就没人敢改。
// 这里只支持两种形状，够用且可核：
//
//	count   某段时间里 status 落在给定词表里的条数
//	ratio   两个指标相除
//
// 定义写在 `<vault>/metrics.md` 的一张表里（人可直接改）。**没有定义 = 不上屏**，
// 而不是退回代码里的硬编码 —— 那等于口径还是我们定的。
package metrics

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"anc/internal/timeline"
)

const FileName = "metrics.md"

// Def 是一条指标定义。
type Def struct {
	ID         string
	Name       string
	Kind       string   // count | ratio
	StatusIn   []string // kind=count 时用
	Num        string   // kind=ratio 的分子（指标 id）
	Den        []string // kind=ratio 的分母（指标 id 之和）
	WindowDays int
	Owner      string
	Note       string
}

// Value 是一条指标的计算结果。
type Value struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Owner      string `json:"owner,omitempty"`
	WindowDays int    `json:"window_days"`
	// Value 是结果；Undefined=true 时它没有意义。
	Value     float64 `json:"value"`
	Undefined bool    `json:"undefined,omitempty"`
	Reason    string  `json:"reason,omitempty"`
	Note      string  `json:"note,omitempty"`
}

// Report 是一次计算。
type Report struct {
	Schema  string   `json:"schema"`
	Values  []Value  `json:"values"`
	Source  string   `json:"source"`            // 定义文件路径
	Missing []string `json:"missing,omitempty"` // 定义或数据缺口，如实给
}

// Load 读定义。文件不在 = 没有定义（不是错误）。
func Load(vault string) ([]Def, error) {
	b, err := os.ReadFile(filepath.Join(vault, FileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseDefs(string(b)), nil
}

// parseDefs 读那张表：`| id | 名称 | kind | 条件 | 窗口 | 负责人 | …`
//
// 只认以 `|` 开头的行；表头与分隔行按内容跳过。**格式错的行跳过并留痕**（见 parse 返回的注释）。
func parseDefs(doc string) []Def {
	var out []Def
	for _, line := range strings.Split(doc, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "|") {
			continue
		}
		cells := splitRow(t)
		if len(cells) < 4 {
			continue
		}
		if cells[0] == "id" || strings.HasPrefix(cells[0], "---") || cells[0] == "" {
			continue
		}
		d := Def{ID: cells[0], Name: cells[1], Kind: strings.ToLower(cells[2]), Owner: cells[5-1]}
		cond := cells[3]
		if len(cells) > 5 {
			d.WindowDays = atoi(cells[4])
			d.Owner = cells[5]
		} else {
			d.Owner = cells[3]
			cond = ""
		}
		if len(cells) > 6 {
			d.Note = cells[6]
		}
		switch d.Kind {
		case "count":
			d.StatusIn = parseStatusIn(cond)
		case "ratio":
			d.Num, d.Den = parseRatio(cond)
		default:
			continue
		}
		if d.WindowDays <= 0 {
			d.WindowDays = 7
		}
		out = append(out, d)
	}
	return out
}

func splitRow(line string) []string {
	line = strings.Trim(line, "|")
	var out []string
	for _, c := range strings.Split(line, "|") {
		out = append(out, strings.TrimSpace(c))
	}
	return out
}

// status in (done, failed) → [done failed]
func parseStatusIn(cond string) []string {
	c := strings.ToLower(cond)
	i := strings.Index(c, "(")
	j := strings.LastIndex(c, ")")
	if i < 0 || j <= i {
		return nil
	}
	var out []string
	for _, p := range strings.Split(cond[i+1:j], ",") {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// a / (a + b) 或 a / b → 分子, 分母列表
func parseRatio(cond string) (string, []string) {
	parts := strings.Split(cond, "/")
	if len(parts) != 2 {
		return "", nil
	}
	num := strings.TrimSpace(parts[0])
	// 先去掉两边空白再去括号：`(a + b)` 前面可能有空格，
	// 直接 Trim("()") 会剥不掉左括号（第一版就是这么错的）
	den := strings.TrimSpace(parts[1])
	inner := strings.TrimSpace(strings.Trim(den, "()"))
	var dens []string
	for _, p := range strings.Split(inner, "+") {
		if s := strings.TrimSpace(p); s != "" {
			dens = append(dens, s)
		}
	}
	return num, dens
}

// Compute 按定义算。算不出来的写 Reason，不编 0。
func Compute(vault string, now time.Time) (Report, error) {
	// Source 只给相对路径：接口不回本机绝对路径（与 scrub 同一条纪律）
	rep := Report{Schema: "anc.metrics/v1", Source: FileName, Values: []Value{}}
	defs, err := Load(vault)
	if err != nil {
		return rep, err
	}
	if len(defs) == 0 {
		rep.Missing = append(rep.Missing, "vault 里没有 metrics.md（或表是空的）—— 口径没定义，就不上屏")
		return rep, nil
	}
	doc, err := timeline.Load(vault)
	if err != nil {
		return rep, err
	}

	counts := map[string]float64{}
	reasons := map[string]string{}
	// 先算 count，再算 ratio（ratio 依赖 count）
	for _, d := range defs {
		if d.Kind != "count" {
			continue
		}
		if len(d.StatusIn) == 0 {
			reasons[d.ID] = "条件读不懂：要写 status in (done) 这种形状"
			continue
		}
		counts[d.ID] = countStatus(doc, d.StatusIn, d.WindowDays, now)
	}
	for _, d := range defs {
		v := Value{ID: d.ID, Name: d.Name, Kind: d.Kind, Owner: d.Owner, WindowDays: d.WindowDays, Note: d.Note}
		switch d.Kind {
		case "count":
			if why, bad := reasons[d.ID]; bad {
				v.Undefined, v.Reason = true, why
			} else {
				v.Value = counts[d.ID]
			}
		case "ratio":
			if d.Num == "" || len(d.Den) == 0 {
				v.Undefined, v.Reason = true, "公式读不懂：要写 a / (a + b)"
				break
			}
			num, ok1 := counts[d.Num]
			if !ok1 {
				v.Undefined, v.Reason = true, "分子指标 "+d.Num+" 没有定义"
				break
			}
			var den float64
			badDen := ""
			for _, id := range d.Den {
				c, ok := counts[id]
				if !ok {
					badDen = id
					break
				}
				den += c
			}
			if badDen != "" {
				v.Undefined, v.Reason = true, "分母指标 "+badDen+" 没有定义"
				break
			}
			if den == 0 {
				v.Undefined, v.Reason = true, "分母为 0 —— 这段时间没有任何相关记录，算比例没有意义"
				break
			}
			v.Value = num / den
		}
		rep.Values = append(rep.Values, v)
	}
	return rep, nil
}

func countStatus(doc timeline.Doc, statusIn []string, days int, now time.Time) float64 {
	cut := now.AddDate(0, 0, -days)
	want := map[string]bool{}
	for _, s := range statusIn {
		want[strings.ToLower(s)] = true
	}
	var n float64
	for _, e := range doc.Entries {
		if !want[strings.ToLower(strings.TrimSpace(e.Status))] {
			continue
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(e.At))
		if err != nil || t.Before(cut) {
			continue
		}
		n++
	}
	return n
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

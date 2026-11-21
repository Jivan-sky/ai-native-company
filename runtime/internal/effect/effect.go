// Package effect —— 飞轮第⑥环：「用了之后有没有用」。
//
// 这一环最容易被做成编数字的地方。所以三条纪律：
//
//	1 只报**能算出来的**：卡点按月次数、经验被哪些岗位采纳；
//	2 算不出来的**明说算不出来**（装配只记到段级，记不到"某个技能被用了多少次"）；
//	3 不把"部署了"当"有用了" —— 部署是动作，有用是结果，两者之间隔着数据。
package effect

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"anc/internal/casework"
	"anc/internal/exp"
	"anc/internal/org"
)

// MonthCount 是一个月里某类卡点的次数。
type MonthCount struct {
	Month string `json:"month"` // 2026-11
	Count int    `json:"count"`
}

// RecurTrend 是一类卡点的月度走势。
type RecurTrend struct {
	Domain   string       `json:"domain,omitempty"`
	Kind     string       `json:"kind"`
	Months   []MonthCount `json:"months"`
	Last     int          `json:"last"`
	Previous int          `json:"previous"`
	// Direction：down / up / flat。down 才是好事。
	Direction string `json:"direction"`
}

// SkillEffect 是一条已生效经验的"有没有被用"。
type SkillEffect struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Version   int      `json:"version"`
	At        string   `json:"deployed_at,omitempty"`
	AdoptedBy []string `json:"adopted_by,omitempty"` // 哪些岗位在 roles/*.md 的 skills 里声明了它
	Adopted   int      `json:"adopted"`
}

// Report 是一次效果盘点。
type Report struct {
	Schema      string        `json:"schema"`
	Vault       string        `json:"vault"`
	GeneratedAt string        `json:"generated_at"`
	Trends      []RecurTrend  `json:"trends"`
	Skills      []SkillEffect `json:"skills"`
	// Unknown 是"这一版算不出来"的清单 —— 必须随报告一起给。
	Unknown []string `json:"unknown,omitempty"`
	Note    string   `json:"note,omitempty"`
}

const effectSchema = "anc.effect/v1"

// Compute 盘点效果。算不出来的写进 Unknown，不猜。
func Compute(vault string) (Report, error) {
	rep := Report{Schema: effectSchema, Vault: vault,
		GeneratedAt: time.Now().Format(time.RFC3339),
		Note:        "只报能算出来的：卡点按月次数、经验被哪些岗位采纳。装配只记到段级，记不到单个技能用了多少次。"}

	// ① 卡点按月走势
	bs, err := casework.ListBlockers(vault)
	if err != nil {
		return rep, err
	}
	type key struct{ domain, kind string }
	byKey := map[key]map[string]int{}
	for _, b := range bs {
		m := monthOf(b.Since)
		if m == "" {
			continue
		}
		k := key{b.Domain, b.Kind}
		if byKey[k] == nil {
			byKey[k] = map[string]int{}
		}
		byKey[k][m]++
	}
	for k, months := range byKey {
		var list []MonthCount
		for m, n := range months {
			list = append(list, MonthCount{Month: m, Count: n})
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].Month < list[j].Month })
		t := RecurTrend{Domain: k.domain, Kind: k.kind, Months: list}
		if n := len(list); n > 0 {
			t.Last = list[n-1].Count
			if n > 1 {
				t.Previous = list[n-2].Count
			}
		}
		switch {
		case len(t.Months) < 2:
			// 只有一个月的点，没有方向可言 —— 报 up/down 就是编
			t.Direction = "unknown"
		case t.Last < t.Previous:
			t.Direction = "down"
		case t.Last > t.Previous:
			t.Direction = "up"
		default:
			t.Direction = "flat"
		}
		rep.Trends = append(rep.Trends, t)
	}
	sort.SliceStable(rep.Trends, func(i, j int) bool {
		if rep.Trends[i].Domain != rep.Trends[j].Domain {
			return rep.Trends[i].Domain < rep.Trends[j].Domain
		}
		return rep.Trends[i].Kind < rep.Trends[j].Kind
	})
	if len(bs) == 0 {
		rep.Unknown = append(rep.Unknown, "没有任何卡点记录 —— 「同类卡点有没有变少」答不出来")
	} else if len(rep.Trends) > 0 && allFlat(rep.Trends) {
		rep.Unknown = append(rep.Unknown, "所有类别只有一个月的数据 —— 还看不出趋势（至少要两个月才有方向）")
	}

	// ② 经验被哪些岗位采纳（roles/*.md 的 skills 列）
	skills, err := exp.List(vault)
	if err != nil {
		return rep, err
	}
	// 采纳是**声明计数**：只读 roles/*/persona.md 的 skills 列，
	// 不走 org.Load（它跑全套校验，一个无关红档就会让这个数算不出来）。
	roleSkills, badRoles := org.RoleSkills(vault)
	if len(badRoles) > 0 {
		rep.Unknown = append(rep.Unknown, "有 "+itoa(len(badRoles))+" 个岗位读不动（"+strings.Join(badRoles, "、")+"）—— 它们的采纳没算进来")
	}
	for _, e := range skills {
		if e.Status != exp.StatusDeployed {
			continue
		}
		se := SkillEffect{ID: e.ID, Title: e.Title, Version: e.Version, At: e.At}
		for name, skillsList := range roleSkills {
			for _, sname := range skillsList {
				if strings.EqualFold(strings.TrimSpace(sname), e.ID) {
					se.AdoptedBy = append(se.AdoptedBy, name)
					break
				}
			}
		}
		sort.Strings(se.AdoptedBy)
		se.Adopted = len(se.AdoptedBy)
		rep.Skills = append(rep.Skills, se)
	}
	if len(rep.Skills) == 0 {
		rep.Unknown = append(rep.Unknown, "还没有已生效的经验 —— 谈不上用处")
	} else {
		zero := 0
		for _, s := range rep.Skills {
			if s.Adopted == 0 {
				zero++
			}
		}
		if zero > 0 {
			rep.Unknown = append(rep.Unknown, "有 "+itoa(zero)+" 条已部署经验没有任何岗位声明使用 —— 可能是没人知道，也可能是没用")
		}
	}

	rep.Unknown = append(rep.Unknown,
		"单个技能被用了多少次：算不出来 —— 装配流水只记到段级（skills 一段），不记具体哪条")
	if _, err := os.Stat(filepath.Join(vault, "context", "assembly.jsonl")); err != nil {
		rep.Unknown = append(rep.Unknown, "装配流水没开（--assembly-log）—— 连段级使用都没有历史")
	}
	return rep, nil
}

func monthOf(ts string) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(ts))
	if err != nil {
		return ""
	}
	return t.Format("2006-01")
}

func allFlat(ts []RecurTrend) bool {
	for _, t := range ts {
		if len(t.Months) > 1 {
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i > 0 {
		return s[:i]
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

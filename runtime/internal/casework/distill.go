package casework

// 结账即触发蒸馏（学 Hermes 的「学习闭环挂在会话边界上」）。
//
// Hermes 的做法：会话结束时把这次的经验沉淀下来。ANC 的天然边界是**结账**——
// 一个 case 结账，等于"这件事办完了"，此刻把它的卡点与突破变成经验候选最准：
// 再晚，人已经忘了当时怎么解的。
//
// 三条纪律：
//  1 只产出 **candidate**，绝不自动 deploy（判据 7：未经人确认不得生效）
//  2 幂等：候选 id 由卡点 id 派生（exp-from-<blocker>），重跑不会产出重复条目
//  3 有门槛：不是每条卡点都值得变成经验 —— 要么"有可复用的突破"，要么"同类踩了 ≥N 次"

import (
	"fmt"
	"strings"

	"anc/internal/exp"
)

// DistillOpts 是触发条件。
type DistillOpts struct {
	MinRecur int  // 同类（域 + 类别）至少踩几次才提炼；<=0 用默认 2
	DryRun   bool // 只算不写（给人看会产出什么）
}

// Candidate 是"将要生成的经验"，DryRun 时也能返回给人看。
type Candidate struct {
	BlockerID     string
	Why           string // 为什么它够格（哪条门槛命中）
	Experience    exp.Experience
	Breakthroughs []Breakthrough
}

// DistillCase 把一个 case 的卡点/突破变成经验候选。
//
// 返回：本次新生成的候选、以及因门槛不够被跳过的卡点与理由。
func DistillCase(vault, caseID string, o DistillOpts) (made []Candidate, skipped map[string]string, err error) {
	minRecur := o.MinRecur
	if minRecur <= 0 {
		minRecur = 2
	}
	all, err := ListBlockers(vault)
	if err != nil {
		return nil, nil, err
	}
	bts, err := ListBreakthroughs(vault)
	if err != nil {
		return nil, nil, err
	}
	recur, err := Recurring(vault)
	if err != nil {
		return nil, nil, err
	}
	groupCount := map[string]int{}
	for _, g := range recur {
		groupCount[g.Domain+"|"+g.Kind] = g.Count
	}

	byBlocker := map[string][]Breakthrough{}
	for _, bt := range bts {
		byBlocker[bt.BlockerID] = append(byBlocker[bt.BlockerID], bt)
	}

	skipped = map[string]string{}
	for _, b := range all {
		if caseID != "" && b.Case != caseID {
			continue
		}
		linked := byBlocker[b.ID]
		var reusable []Breakthrough
		for _, bt := range linked {
			if bt.Reusable && strings.TrimSpace(bt.Method) != "" {
				reusable = append(reusable, bt)
			}
		}
		// 门槛一：有可复用的突破
		why := ""
		if len(reusable) > 0 {
			why = "有可复用的突破：" + reusable[0].ID
		} else if n := groupCount[b.Domain+"|"+b.Kind]; n >= minRecur {
			// 门槛二：同类（域+类别）踩够次数
			why = fmt.Sprintf("同类卡点踩了 %d 次（%s / %s）", n, dash(b.Domain), b.Kind)
		}
		if why == "" {
			skipped[b.ID] = "没有可复用的突破，同类也没踩够 " + fmt.Sprint(minRecur) + " 次"
			continue
		}

		claim := b.Title
		scope := b.Domain
		var from []string
		from = append(from, "blocker:"+b.ID)
		for _, bt := range reusable {
			claim = bt.Method // 有突破时，结论就是"这么解"
			if strings.TrimSpace(bt.Scope) != "" {
				scope = bt.Scope
			}
			from = append(from, "breakthrough:"+bt.ID)
		}
		e := exp.Experience{
			ID:    "exp-from-" + b.ID,
			Title: b.Title,
			Claim: claim,
			Scope: scope,
			From:  from,
			By:    "anc distill-case", // 自动产出的候选：署名写机器，人确认时才写人
		}
		made = append(made, Candidate{BlockerID: b.ID, Why: why, Experience: e, Breakthroughs: reusable})
	}

	if o.DryRun {
		return made, skipped, nil
	}
	out := made[:0]
	for _, c := range made {
		if _, err := exp.Add(vault, c.Experience); err != nil {
			// 已存在 = 幂等跳过（重跑结账不该产生重复候选），不算错
			if strings.Contains(err.Error(), "已经存在") {
				skipped[c.BlockerID] = "候选已存在（幂等）"
				continue
			}
			return nil, skipped, err
		}
		out = append(out, c)
	}
	return out, skipped, nil
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(未标域)"
	}
	return s
}

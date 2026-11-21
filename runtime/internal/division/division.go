// Package division —— 「这件事该谁拍板」的可执行判定（治理判据 11'）。
//
// 为什么把它做成代码而不是一段话：一段话没法被执行、也没法被审计。
// 但**这里只判"树"，不判"事实"** —— 三个事实（跨不跨数据、花不花钱、可不可逆）
// 由提案人/一线 agent 声明；本包按声明的树给出结论与理由链。
//
// 序（2026-11-19 定，来自执行者的反例）：
//
//	① 会不会把别人的东西带到别人眼前？→ 人（删文档≠撤回，对方看过就收不回）
//	② 会不会花钱 / 改对外承诺 / 动别人权限？→ 人
//	③ 做错了能不能真正撤回？不能 → 人；能 → agent 可做，人抽查
//
// 判据 11' 的验收不是"AI 同意我的判断"，而是**同一组事实永远得到同一个结论**，
// 且结论能指出是哪一条把它推到"人"这一侧的。
package division

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// Case 是三个事实的声明。false 不是"小事"，是"提案人说不是"。
type Case struct {
	Name         string `json:"name"`
	CrossData    bool   `json:"cross_data"`   // 会不会把别人的东西（数据/结论）带到别人眼前
	Money        bool   `json:"money"`        // 花钱
	Promise      bool   `json:"promise"`      // 改对外承诺（报价/SOW/合同）
	Permissions  bool   `json:"permissions"`  // 动别人的权限
	Irreversible bool   `json:"irreversible"` // 做错了收不回（删/合并/对外发送）
	Note         string `json:"note,omitempty"`
	// Want 是答案集里人给的结论：true = 人拍板。
	Want bool `json:"want_human,omitempty"`
}

// Verdict 是判定结果。
type Verdict struct {
	Human  bool     `json:"human"`  // true = 人拍板
	Reason string   `json:"reason"` // 一句话：为什么
	Rule   string   `json:"rule"`   // 命中哪一条（rule1/rule2/rule3/else）
	Path   []string `json:"path"`   // 走过来的每一步（可审计）
}

// Decide 按声明的树判定。纯函数：同样的输入永远同样的输出。
func Decide(c Case) Verdict {
	var path []string
	path = append(path, fmt.Sprintf("跨数据=%v 花钱=%v 对外承诺=%v 动权限=%v 可撤回=%v",
		c.CrossData, c.Money, c.Promise, c.Permissions, !c.Irreversible))

	if c.CrossData {
		return Verdict{Human: true, Rule: "rule1", Path: path,
			Reason: "可能把别人的东西带到别人眼前 —— 删文档不等于撤回，对方看过就收不回"}
	}
	if c.Money || c.Promise || c.Permissions {
		return Verdict{Human: true, Rule: "rule2", Path: path,
			Reason: "涉及钱 / 对外承诺 / 别人的权限 —— 这三样都不该由 agent 自己决定"}
	}
	if c.Irreversible {
		return Verdict{Human: true, Rule: "rule3", Path: path,
			Reason: "做错了收不回 —— 能撤回的才允许 agent 自己做"}
	}
	return Verdict{Human: false, Rule: "else", Path: path,
		Reason: "可撤回、不花钱、不对外、不动权限 —— agent 可做，人抽查"}
}

//go:embed cases.json
var casesJSON []byte

// LoadCases 读内置答案集（同一份文件也给人类评审用）。
func LoadCases() ([]Case, error) {
	var cs []Case
	if err := json.Unmarshal(casesJSON, &cs); err != nil {
		return nil, err
	}
	return cs, nil
}

// SelfTest 用答案集回放：每条都必须与 Want 一致，且不一致的要指出来。
//
// 注意它**不证明判断高明**，只证明"树与答案集没打架"。
// 答案集本身要人签过字才算数（S1 的教训：自说自话不算通过）。
type SelfTestResult struct {
	Total     int      `json:"total"`
	Agreed    int      `json:"agreed"`
	Disagreed []string `json:"disagreed,omitempty"`
	Rate      float64  `json:"rate"`
}

func SelfTest() (SelfTestResult, error) {
	cs, err := LoadCases()
	if err != nil {
		return SelfTestResult{}, err
	}
	res := SelfTestResult{Total: len(cs)}
	for _, c := range cs {
		v := Decide(c)
		if v.Human == c.Want {
			res.Agreed++
		} else {
			res.Disagreed = append(res.Disagreed, fmt.Sprintf("%s：树=%v 答案=%v（%s）", c.Name, v.Human, c.Want, v.Rule))
		}
	}
	if res.Total > 0 {
		res.Rate = float64(res.Agreed) / float64(res.Total)
	}
	return res, nil
}

package org

import "strings"

// 本文件是「markdown 表 → 结构」的共用底座。
//
// 用它的是两张表：业务域表（domains.md）与项目表（projects.md）——
// 「哪一行是表头」「哪些列认得」「单元格怎么切」这三件事一旦各写一份，
// 迟早出现「域表认得出的表，项目表认不出」这种到了现场才发现的漂移。
//
// 边界也是刻意的：这里只做**表结构**，不做字段语义。
// 某一列合不合法、缺了报什么，归各自的 LoadDomains / LoadProjects 定 ——
// 底座越厚，两张表能表达的东西就越像，而它们本该长得不一样：
// 域表是罗盘（给 agent 的上下文与作用域），项目表是汇总（给人看的进度）。

// itoa 是「第 n 行」用的十进制整数，不引 strconv —— 这层没必要为它多担一个包。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// tableCells 拆一行 markdown 表；不是表行返回 nil。
// 行首必须是 | —— 否则正文里带 | 的句子会被当成表行读进配置。
func tableCells(line string) []string {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "|") {
		return nil
	}
	parts := strings.Split(strings.Trim(t, "|"), "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// isTableSep 判断是不是 |---|---| 这种分隔行。
func isTableSep(line string) bool {
	cells := tableCells(line)
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if c == "" {
			return false
		}
		for _, r := range c {
			if r != '-' && r != ':' {
				return false
			}
		}
	}
	return true
}

// findTable 定位表头行，返回它的下标与「列名 → 列序」映射；找不到返回 (-1, nil)。
//
//   - known 是认得的列名（全小写）；不认得的列忽略 —— 表可以多列备注，不影响解析。
//   - required 是必须出现的列名；缺了它 = 「这不是那张表」，继续往下找。
//
// 判据是「表头 + 下一行是分隔行 + 有 required 列」。宁可漏认（报 table.missing）
// 也不误认：误认会把人写来给人看的表，当成配置读。
func findTable(lines []string, known map[string]bool, required string) (int, map[string]int) {
	for i := 0; i+1 < len(lines); i++ {
		cells := tableCells(lines[i])
		if len(cells) == 0 || !isTableSep(lines[i+1]) {
			continue
		}
		cols := map[string]int{}
		for j, c := range cells {
			if key := strings.ToLower(strings.TrimSpace(c)); known[key] {
				cols[key] = j
			}
		}
		if _, ok := cols[required]; ok {
			return i, cols
		}
	}
	return -1, nil
}

// rowReader 把「列序映射 + 这一行的单元格」包成按列名取值：缺列或越界都返回空串，
// 于是调用方不必在每一个字段上再判一次边界。
func rowReader(cols map[string]int, cells []string) func(string) string {
	return func(key string) string {
		if j, ok := cols[key]; ok && j < len(cells) {
			return strings.TrimSpace(cells[j])
		}
		return ""
	}
}

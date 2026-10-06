// Package org 负责 org 真相源（markdown + frontmatter）的解析、加载与校验。
//
// frontmatter 只支持 mini YAML 子集：标量 / 布尔 / 整数 / 内联数组 / 一层嵌套映射。
// 越界（锚点、多行块、两层以上嵌套、tab 缩进）直接报错并给出行号 —— 宁可拒绝，不做静默猜测。
package org

import (
	"fmt"
	"strconv"
	"strings"
)

// Doc 是一份带 frontmatter 的 markdown 文档。
type Doc struct {
	Meta  map[string]any // 键路径 → 值；嵌套层用 "父.子" 扁平存放
	Body  string         // frontmatter 之后的正文（已 TrimSpace）
	lines map[string]int // 键路径 → frontmatter 行号（报错用）
}

// Parse 解析 frontmatter + 正文。所有错误都带行号。
func Parse(src string) (*Doc, error) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, fmt.Errorf("第 1 行: 缺 frontmatter 起始 `---`")
	}
	d := &Doc{Meta: map[string]any{}, lines: map[string]int{}}
	end := -1
	parent := "" // 当前嵌套映射的父键；空 = 顶层
	for i := 1; i < len(lines); i++ {
		raw := lines[i]
		if strings.TrimSpace(raw) == "---" {
			end = i
			break
		}
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if strings.ContainsRune(raw[:indent], '\t') {
			return nil, fmt.Errorf("frontmatter 第 %d 行: 缩进不许用 tab", i+1)
		}
		key, val, ok := splitKV(trimmed)
		if !ok {
			return nil, fmt.Errorf("frontmatter 第 %d 行: 不是 `key: value` 形态: %q", i+1, trimmed)
		}
		if strings.ContainsAny(key, "&*|>") {
			return nil, fmt.Errorf("frontmatter 第 %d 行: 键名含非法字符（锚点/别名不在支持范围）: %q", i+1, key)
		}
		if strings.HasPrefix(val, "|") || strings.HasPrefix(val, ">") {
			return nil, fmt.Errorf("frontmatter 第 %d 行: 不支持多行块标量（| / >）", i+1)
		}
		if indent > 0 {
			if parent == "" {
				return nil, fmt.Errorf("frontmatter 第 %d 行: %q 缩进了，但上面没有开启嵌套的键", i+1, key)
			}
			if indent >= 4 {
				return nil, fmt.Errorf("frontmatter 第 %d 行: 只支持一层嵌套", i+1)
			}
			if val == "" {
				return nil, fmt.Errorf("frontmatter 第 %d 行: %q 下面不能再嵌套", i+1, key)
			}
			full := parent + "." + key
			v, err := value(val)
			if err != nil {
				return nil, fmt.Errorf("frontmatter 第 %d 行: 键 %q %v", i+1, key, err)
			}
			d.Meta[full] = v
			d.lines[full] = i + 1
			continue
		}
		if val == "" { // 开启嵌套映射
			parent = key
			d.lines[key] = i + 1
			continue
		}
		parent = ""
		v, err := value(val)
		if err != nil {
			return nil, fmt.Errorf("frontmatter 第 %d 行: 键 %q %v", i+1, key, err)
		}
		d.Meta[key] = v
		d.lines[key] = i + 1
	}
	if end < 0 {
		return nil, fmt.Errorf("缺 frontmatter 结束 `---`")
	}
	d.Body = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	return d, nil
}

// Line 返回键路径的行号；不存在返回 0。
func (d *Doc) Line(path string) int { return d.lines[path] }

func (d *Doc) errf(path, format string, a ...any) error {
	where := "frontmatter"
	if n := d.lines[path]; n > 0 {
		where = fmt.Sprintf("frontmatter 第 %d 行", n)
	}
	return fmt.Errorf("%s: %s", where, fmt.Sprintf(format, a...))
}

// Str 取字符串；缺失返回 ""，类型不符报错。
func (d *Doc) Str(path string) (string, error) {
	raw, ok := d.Meta[path]
	if !ok {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", d.errf(path, "%s 应为字符串", path)
	}
	return s, nil
}

// StrList 取字符串数组；缺失返回 nil。
func (d *Doc) StrList(path string) ([]string, error) {
	raw, ok := d.Meta[path]
	if !ok {
		return nil, nil
	}
	v, ok := raw.([]string)
	if !ok {
		return nil, d.errf(path, "%s 应为数组（如 [a, b]）", path)
	}
	return v, nil
}

// Bool 取布尔；缺失返回 false。
func (d *Doc) Bool(path string) (bool, error) {
	raw, ok := d.Meta[path]
	if !ok {
		return false, nil
	}
	v, ok := raw.(bool)
	if !ok {
		return false, d.errf(path, "%s 应为 true / false", path)
	}
	return v, nil
}

// Int 取整数；缺失返回 0。
func (d *Doc) Int(path string) (int, error) {
	raw, ok := d.Meta[path]
	if !ok {
		return 0, nil
	}
	v, ok := raw.(int)
	if !ok {
		return 0, d.errf(path, "%s 应为整数", path)
	}
	return v, nil
}

// Has 判断键是否存在（用于区分「没写」与「写了空值」）。
func (d *Doc) Has(path string) bool { _, ok := d.Meta[path]; return ok }

// splitKV 按第一个不在引号内的 `:` 切分。
func splitKV(s string) (string, string, bool) {
	inS, inD := false, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inD {
				inS = !inS
			}
		case '"':
			if !inS {
				inD = !inD
			}
		case ':':
			if !inS && !inD {
				return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
			}
		}
	}
	return "", "", false
}

// value 把标量文本转成具体类型。
func value(s string) (any, error) {
	s = stripComment(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return strings.ReplaceAll(s[1:len(s)-1], `\'`, `'`), nil
	}
	switch s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n, nil
	}
	if strings.HasPrefix(s, "[") {
		return inlineArray(s)
	}
	return s, nil
}

func inlineArray(s string) ([]string, error) {
	if !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("数组缺少收尾 `]`")
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return []string{}, nil
	}
	parts := strings.Split(inner, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("数组里有空元素")
		}
		v, err := value(p)
		if err != nil {
			return nil, err
		}
		str, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("数组只支持字符串元素，遇到 %v", v)
		}
		out = append(out, str)
	}
	return out, nil
}

// stripComment 去掉行尾注释（不在引号内的 ` #`）。
func stripComment(s string) string {
	inS, inD := false, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inD {
				inS = !inS
			}
		case '"':
			if !inS {
				inD = !inD
			}
		case '#':
			if !inS && !inD && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return strings.TrimSpace(s)
}

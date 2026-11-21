package org

// RoleSkills 只读一件事：**每个岗位声明了用哪些技能**（roles/<名>/persona.md 的 skills 列）。
//
// 为什么不走 Load：Load 会跑全套校验，一个无关的红档就让"采纳数"算不出来；
// 而采纳是一个**声明计数**，与 org 是否合规无关。这里用容错读法，读不动的岗位跳过并在返回值里标出来。
//
// 支持两种写法（与 frontmatter 兼容）：
//
//	skills: [now, im-send]
//	skills:
//	  - now
//	  - im-send

import (
	"os"
	"path/filepath"
	"strings"
)

// RoleSkills 返回 岗位名 → 技能列表；以及读不动的岗位名（如实报，不静默丢）。
func RoleSkills(vault string) (map[string][]string, []string) {
	out := map[string][]string{}
	var bad []string
	base := filepath.Join(vault, "roles")
	entries, err := os.ReadDir(base)
	if err != nil {
		return out, nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		b, err := os.ReadFile(filepath.Join(base, name, "persona.md"))
		if err != nil {
			bad = append(bad, name)
			continue
		}
		if skills, ok := parseSkillsList(string(b)); ok {
			out[name] = skills
		}
	}
	return out, bad
}

// parseSkillsList 从 frontmatter 里抠 skills。抠不到返回 ok=false。
func parseSkillsList(doc string) ([]string, bool) {
	if !strings.HasPrefix(doc, "---\n") {
		return nil, false
	}
	body := doc[4:]
	end := strings.Index(body, "\n---\n")
	if end < 0 {
		return nil, false
	}
	lines := strings.Split(body[:end], "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "skills:") {
			continue
		}
		inline := strings.TrimSpace(strings.TrimPrefix(t, "skills:"))
		if inline != "" {
			inline = strings.Trim(inline, "[]")
			var out []string
			for _, p := range strings.Split(inline, ",") {
				if s := strings.Trim(strings.TrimSpace(p), `"'`); s != "" {
					out = append(out, s)
				}
			}
			return out, true
		}
		// 列表写法：往下读连续的 "  - xxx"
		var out []string
		for _, nxt := range lines[i+1:] {
			nt := strings.TrimSpace(nxt)
			if nt == "" {
				continue
			}
			if !strings.HasPrefix(nt, "-") {
				break
			}
			if s := strings.Trim(strings.TrimSpace(strings.TrimPrefix(nt, "-")), `"'`); s != "" {
				out = append(out, s)
			}
		}
		return out, true
	}
	return nil, false
}

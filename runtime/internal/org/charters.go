package org

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CharterCopy 是 charters/ 下的一个副本条目 —— 一个项目一个子目录。
//
// 落点为什么是「目录」而不是某个固定文件名：立项书的文件名与结构随公司 / 行业 / PMO 而变
// （真实案例里，负责人写在正文的中文表格里、周期写在里程碑标题里）。ANC 只认**目录名**
// （期望等于 projects.md 的 slug）；里面放什么、叫什么名字，由交付的人定 ——
// 我们不解压、不解析、不要求格式。这是「接入契约」里最薄的那一半。
type CharterCopy struct {
	Slug string // 目录名，期望等于 projects.md 的 slug
	Path string // 相对 vault 根的路径（POSIX 分隔符），如 charters/trade-q3
}

// ChartersDir 是立项书副本的落点（vault 顶层）。
//
// 它**不进** persona 段 3 的路由表（见 org.go 的 skipDirs）—— 那行「数据来源」回答的是
// 「业务资料去哪查」，副本落点不是资料；而且资产页已经单独统计它，再进路由表会重复计一次。
const ChartersDir = "charters"

// ScanCharters 扫落点。
//
// 只认**子目录**为条目：落点自己的说明文件（CLAUDE.md / README.md）不是项目条目，忽略。
// 目录不在 = 还没有副本，不算错 —— 真源在客户侧，副本是保障（断网 / 换人 / 客户改权限时
// 还查得到），不是义务。
//
// 纯扫描，不做跨表判断：「这个 slug 是不是真项目」归 validateCharters。
func ScanCharters(root string) ([]CharterCopy, error) {
	ents, err := os.ReadDir(filepath.Join(root, ChartersDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []CharterCopy
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, CharterCopy{Slug: e.Name(), Path: ChartersDir + "/" + e.Name()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// Charter 按 slug 取副本条目。
func (o *Org) Charter(slug string) (CharterCopy, bool) {
	for _, c := range o.Charters {
		if c.Slug == slug {
			return c, true
		}
	}
	return CharterCopy{}, false
}

// validateCharters 跨表校验：落点里的目录名是不是 projects.md 里的真项目。
//
// 只发现事实，档次由规则表定（实现里不许硬编码档次）。这一条默认只告警，理由和域表一样：
// 副本目录可能比表行**早到**（人先把材料丢进来，抽取还没做），那不是错，是进度落后于事实。
func (o *Org) validateCharters(p *Policy, rep *Report) {
	for _, c := range o.Charters {
		if _, ok := o.Project(c.Slug); !ok {
			rep.Add(p.Issue("charter.entry.unmatched", c.Path,
				"落点里没有对应的项目行（%s 里没有 slug=%q）—— 多半是名字写错，或这个项目还没抽成表行",
				ProjectsFile, c.Slug))
		}
	}
}

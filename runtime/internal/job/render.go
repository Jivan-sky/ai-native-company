package job

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// render 把响应体变成落盘内容。两种：
//
//	raw       —— 原文照搬（最诚实：不解释、不加工）
//	issues-md —— GitHub issue 列表 → 一张按 label 分组的表（这条 job 真需要的摘要）
//
// 认不出的 render 直接报错，不退回 raw —— 静默退回会让「我明明配了摘要」变成原文。
func render(kind string, body []byte, jobName string, now time.Time) []byte {
	switch kind {
	case "", "raw":
		return body
	case "issues-md":
		return renderIssues(body, jobName, now)
	}
	return append([]byte("render=%q 不认识（raw | issues-md）\n"), body...)
}

type ghIssue struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	HTMLURL   string `json:"html_url"`
	CreatedAt string `json:"created_at"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
	PullRequest json.RawMessage `json:"pull_request"`
}

// renderIssues 生成一份「今天开着什么」的清单：按 label 分组，拉请求单独剔出去。
func renderIssues(body []byte, jobName string, now time.Time) []byte {
	var list []ghIssue
	if err := json.Unmarshal(body, &list); err != nil {
		return []byte(fmt.Sprintf("# %s %s\n\n> 响应不是 issue 数组，原文 %d 字节 —— 没有生成摘要（不猜）。\n\n",
			jobName, now.Format("2006-01-02"), len(body)))
	}
	byLabel := map[string][]ghIssue{}
	prs := 0
	for _, it := range list {
		if len(it.PullRequest) > 0 && string(it.PullRequest) != "null" {
			prs++
			continue
		}
		label := "未分类"
		if len(it.Labels) > 0 {
			label = it.Labels[0].Name
		}
		byLabel[label] = append(byLabel[label], it)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s · %s\n\n", jobName, now.Format("2006-01-02"))
	fmt.Fprintf(&b, "> 由 `anc job` 从 GitHub API 拉取并生成；数字是这一次请求的真实结果，不是猜的。\n\n")
	fmt.Fprintf(&b, "- 开着的条目：**%d**（含拉请求 %d，已剔除）\n", len(list)-prs, prs)
	fmt.Fprintf(&b, "- 分组：%d 个 label\n\n", len(byLabel))
	labels := make([]string, 0, len(byLabel))
	for k := range byLabel {
		labels = append(labels, k)
	}
	sort.Strings(labels)
	for _, l := range labels {
		items := byLabel[l]
		fmt.Fprintf(&b, "## %s（%d）\n\n| # | 标题 | 建的时间 |\n|---|---|---|\n", l, len(items))
		for _, it := range items {
			fmt.Fprintf(&b, "| [#%d](%s) | %s | %s |\n", it.Number, it.HTMLURL, escapeCell(it.Title), shortDate(it.CreatedAt))
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func escapeCell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

func shortDate(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Local().Format("2006-01-02")
	}
	return s
}

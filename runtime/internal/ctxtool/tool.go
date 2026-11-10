// Package ctxtool —— 把「读上下文」挂成 agent 工具（CLI 与 HTTP 出口共用同一份）。
//
// 它就是 MCP 读出口的同一个装配点（ctxpack.Assemble）—— 不另写一套，
// 否则同一个问题在三个出口会有三个答案。
package ctxtool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"anc/internal/agent"
	"anc/internal/ctxpack"
	"anc/internal/org"
)

// ReadContext 返回 `read_context` 工具：按成员名/agent slug 读工作上下文。
func ReadContext(vault string) agent.Tool {
	return agent.Tool{
		Spec: agent.Spec("read_context",
			"读某个成员/agent 的工作上下文（身份 / 业务域 / 项目 / 可见范围 / 缺口）",
			`{"type":"object","properties":{"who":{"type":"string","description":"成员名或 agent 的 slug"}},"required":["who"]}`),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Who string `json:"who"`
			}
			if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Who) == "" {
				return "", fmt.Errorf("需要 who（成员名或 agent slug）")
			}
			o, err := org.Load(vault)
			if err != nil {
				return "", fmt.Errorf("vault 加载失败：%v", err)
			}
			out, _ := ctxpack.Assemble(o, vault, a.Who, "", "", time.Now(), ctxpack.TasksIn{})
			b, err := json.Marshal(out)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	}
}

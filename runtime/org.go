package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"anc/internal/org"
)

const orgWhoUsage = `anc org who —— 把平台那一侧的定位信息翻成 org 里的谁（只查表，不猜）

用法：
  anc org who <vault> [--open-id <ou_…>] [--app-id <cli_…>] [--project <名>] [--json]

选项：
  --open-id <ou_…>   谁发的 —— 平台那边唯一的「人」标识（只可能落在成员上：业务 agent 不代表人）
  --app-id <cli_…>   这条发到哪个 app → 哪台 bot（成员 bot / 业务 agent 二选一）
  --project <名>     这台 bot 的 project 名（<公司 id>-<名字>）；与 --app-id 一起给时以 app_id 为准
  --json             机器读的出口

两头各自解、**互不代偿**：一头解不出就如实说解不出，不拿另一头顶上 ——
身份认错，后面每一条留痕都记在错的人头上，比不记还坏。
「属于谁」只在业务 agent 上答（域 → who 岗位 → 该岗位上启用中的成员）；人没有归属人。
`

// whoSide 是命令行/JSON 那一侧的一头：问的是什么、解出来是谁、凭什么。
type whoSide struct {
	Asked       string   `json:"asked"` // open_id | app_id | project
	Value       string   `json:"value"`
	Kind        string   `json:"kind,omitempty"`    // member | agent
	Ref         string   `json:"ref,omitempty"`     // 编号：成员名 / agent slug
	Name        string   `json:"name,omitempty"`    // 显示名
	Role        string   `json:"role,omitempty"`    // 岗位
	Domains     []string `json:"domains,omitempty"` // 归属域
	Inscription string   `json:"inscription,omitempty"`
	Project     string   `json:"project,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	OwnerWhy    string   `json:"owner_why,omitempty"`
	Why         string   `json:"why,omitempty"` // 解不出时的原因（非空 = 这一头没解出来）
}

type whoOut struct {
	Vault string   `json:"vault"`
	From  *whoSide `json:"from,omitempty"`
	To    *whoSide `json:"to,omitempty"`
}

// resolveSide 把一次查询落成 whoSide：**解不出来也返回一侧**（带 Why），
// 让「没解出来」与「没问」在输出里能分开 —— 后者是没问，前者是问了没人认领。
func resolveSide(o *org.Org, asked, value string, lookup func(*org.Org, string) (org.Identity, bool), whyNot string) *whoSide {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	s := &whoSide{Asked: asked, Value: strings.TrimSpace(value)}
	id, ok := lookup(o, value)
	if !ok {
		s.Why = whyNot
		return s
	}
	s.Kind, s.Ref, s.Name = id.Kind, id.Ref, id.Name
	s.Role, s.Domains, s.Inscription = id.Role, id.Domains, id.Inscription
	s.Project = org.ProjectName(o.Company.ID, id.Ref)
	// 归属只在「代岗位」的那一类上有意义：人属于他自己，这一格留空。
	if id.Kind == org.KindAgent {
		s.Owner, s.OwnerWhy = o.Owner(id)
	}
	return s
}

func cmdOrgWho(args []string) int {
	fs := flag.NewFlagSet("org who", flag.ContinueOnError)
	openID := fs.String("open-id", "", "谁发的（平台唯一的「人」标识）")
	appID := fs.String("app-id", "", "这条发到哪个 app")
	project := fs.String("project", "", "project 名")
	asJSON := fs.Bool("json", false, "机器读的出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 ||
		(strings.TrimSpace(*openID) == "" && strings.TrimSpace(*appID) == "" && strings.TrimSpace(*project) == "") {
		fmt.Fprint(os.Stderr, orgWhoUsage)
		return 2
	}
	abs, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 2
	}
	o, err := org.Load(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 1
	}

	out := whoOut{Vault: abs}
	out.From = resolveSide(o, "open_id", *openID, (*org.Org).ByOpenID,
		fmt.Sprintf("open_id=%s 在 members/ 里没人认领 —— 自报的身份不算（同 gate 的口径）", strings.TrimSpace(*openID)))
	// app_id 比 project 名更贴平台事实：两个都给时按 app_id 解（project 那一头就不再报）。
	if strings.TrimSpace(*appID) != "" {
		out.To = resolveSide(o, "app_id", *appID, (*org.Org).ByAppID,
			fmt.Sprintf("app_id=%s 不挂在任何成员 bot 或业务 agent 上（这台还没接线，或是别人家的 app）", strings.TrimSpace(*appID)))
	} else {
		out.To = resolveSide(o, "project", *project, (*org.Org).ByProject,
			fmt.Sprintf("project=%s 既不是成员 bot、也不是业务 agent 的 project 名（本公司 id=%s）",
				strings.TrimSpace(*project), o.Company.ID))
	}

	if *asJSON {
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}

	fmt.Printf("anc org who —— 平台身份 → org 里的谁\n")
	fmt.Printf("  vault   %s\n", abs)
	printSide("谁发的", out.From)
	printSide("发到谁", out.To)
	return 0
}

// printSide 出一头。字段按「越贴身份越先」排：先是谁，再是挂在哪儿，最后才是凭什么。
func printSide(title string, s *whoSide) {
	if s == nil {
		return
	}
	fmt.Printf("\n  ── %s（%s=%s）\n", title, s.Asked, s.Value)
	if s.Why != "" {
		fmt.Printf("      解不出      %s\n", s.Why)
		return
	}
	if s.Kind == org.KindAgent {
		fmt.Printf("      业务 agent  %s（%s）\n", s.Ref, s.Name)
	} else {
		fmt.Printf("      人          %s（%s）\n", s.Ref, s.Name)
	}
	if s.Inscription != "" {
		fmt.Printf("      铭文        %s\n", s.Inscription)
	}
	if s.Role != "" {
		fmt.Printf("      岗位        %s\n", s.Role)
	}
	if len(s.Domains) > 0 {
		fmt.Printf("      域          %s\n", strings.Join(s.Domains, "、"))
	}
	if s.Owner != "" {
		fmt.Printf("      属于        %s\n", s.Owner)
	}
	if s.OwnerWhy != "" {
		fmt.Printf("      凭什么      %s\n", s.OwnerWhy)
	}
	fmt.Printf("      这台        %s\n", s.Project)
}

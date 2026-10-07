// Package board 把 org 真相源投影成**看板 / 前端消费的只读视图**（JSON）。
//
// 与 internal/render 分开是刻意的：
//
//   - render 的产物是**要给 gateway 吃的** —— 全量生成、带指纹、安全敏感；
//   - board 的产物是**给看板 / 前端吃的** —— 只读、可对外、不含凭据。
//
// 混在一个包里，迟早会有人拿「生成看板」的那条路径去生成配置。
package board

import (
	"encoding/json"
	"time"

	"anc/internal/org"
)

// Schema 是这个视图的版本。字段增删一律改它 —— 消费方据此判断能不能吃。
const Schema = "anc.board/v1"

// View 是看板的全部输入。字段**恒在**（不做 omitempty）：schema 稳定比省几个字节值钱。
//
// **刻意不导出**：feishu.app_id / feishu.open_id / extra_allow_from / allow_chat。
// 看板是**观测面，不是凭据面** —— 「这个人绑没绑好飞书」属于运行态探针（阶段 C），
// 不该靠把标识符摊在看板上解决（SPEC §2.3 / §6-3）。
type View struct {
	Schema    string    `json:"schema"`
	Generated string    `json:"generated_at"`
	Company   Company   `json:"company"`
	Roles     []Role    `json:"roles"`
	Members   []Member  `json:"members"`
	Domains   []Domain  `json:"domains"`
	Projects  []Project `json:"projects"`
	Routing   []Routing `json:"routing"`
}

// Company 是公司层。
type Company struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	Platform string `json:"platform"`
	Language string `json:"language"`
	Timezone string `json:"timezone"`
}

// Role 是角色层。不含 allowed_tools / skills —— 那是渲染进 bot 本体的执行面，
// 看板要看的是「谁在做什么」，不是「他手里有哪些工具」。
type Role struct {
	Role       string   `json:"role"`
	Title      string   `json:"title"`
	Mode       string   `json:"mode"`
	Model      string   `json:"model"`
	VaultScope []string `json:"vault_scope"`
}

// Member 是成员层。Model 是三级回退之后的**生效值**（member ∥ role ∥ company）。
type Member struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Role        string   `json:"role"`
	Admin       bool     `json:"admin"`
	Disabled    bool     `json:"disabled"`
	Model       string   `json:"model"`
	Domains     []string `json:"domains"`
}

// Domain 是域表一行。Who 是岗位，WhoLabel 是渲染时派生的「岗位（人名）」。
type Domain struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	What     string `json:"what"`
	Who      string `json:"who"`
	WhoLabel string `json:"who_label"`
	Data     string `json:"data"`
	Sources  string `json:"sources"`
	Terms    string `json:"terms"`
}

// Routing 是 vault 顶层数据目录 → 说明。
type Routing struct {
	Dir     string `json:"dir"`
	Summary string `json:"summary"`
}

// Project 是项目表一行。Owner 是岗位，OwnerLabel 是派生的「岗位（人名）」。
// Period 是**原文照搬**的自由文本 —— ANC 不解读周期，只把它显示出来。
type Project struct {
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	Domain     string `json:"domain"`
	Owner      string `json:"owner"`
	OwnerLabel string `json:"owner_label"`
	Period     string `json:"period"`
	Source     string `json:"source"`
}

// Of 做投影。纯函数：同样的 org + 同样的 now → 同样的字节（可快照、可 diff）。
func Of(o *org.Org, now time.Time) *View {
	v := &View{
		Schema:    Schema,
		Generated: now.UTC().Format(time.RFC3339),
		Company: Company{
			Name:     o.Company.Name,
			ID:       o.Company.ID,
			Platform: o.Company.Platform,
			Language: o.Company.Language,
			Timezone: o.Company.Timezone,
		},
		Roles:    []Role{},
		Members:  []Member{},
		Domains:  []Domain{},
		Projects: []Project{},
		Routing:  []Routing{},
	}
	for _, key := range o.RoleKeys() {
		r := o.Roles[key]
		v.Roles = append(v.Roles, Role{
			Role: r.Role, Title: r.Title, Mode: r.Mode, Model: r.Model,
			VaultScope: strList(r.VaultScope),
		})
	}
	for _, m := range o.Members {
		v.Members = append(v.Members, Member{
			Name: m.Name, DisplayName: m.DisplayName, Role: m.Role,
			Admin: m.Admin, Disabled: m.Disabled,
			Model:   o.ModelFor(m),
			Domains: strList(m.Domains),
		})
	}
	for _, d := range o.Domains {
		v.Domains = append(v.Domains, Domain{
			Slug: d.Slug, Name: d.Name, What: d.What, Who: d.Who,
			WhoLabel: o.WhoLabel(d.Who), Data: d.Data,
			Sources: d.Sources, Terms: d.Terms,
		})
	}
	for _, r := range o.Routing {
		v.Routing = append(v.Routing, Routing{Dir: r.Dir, Summary: r.Summary})
	}
	for _, pr := range o.Projects {
		v.Projects = append(v.Projects, Project{
			Slug: pr.Slug, Name: pr.Name, Domain: pr.Domain,
			Owner: pr.Owner, OwnerLabel: o.WhoLabel(pr.Owner),
			Period: pr.Period, Source: pr.Source,
		})
	}
	return v
}

// JSON 出稳定字节：结构体字段顺序即声明顺序，切片顺序由上一步决定（都已排序）。
func (v *View) JSON() (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

// strList 把 nil 归一成空数组：JSON 里 [] 比 null 好消费（前端少一个判空）。
func strList(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

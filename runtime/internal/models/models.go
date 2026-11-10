// Package models —— 「模型是平台能力」的那两份契约：注册表 + 应用绑定。
//
// 一句话：**应用不拿 key 去调模型，它声明「我要用哪个注册名」，由 ANC 代调。**
//
// 三条硬约束（写进校验，不靠自觉）：
//
//  1. `secret` 只写**名字**（`DASHSCOPE_KEY_PROD`），不是值。真密钥长得不像环境变量名，
//     这条校验就是过滤器：谁把 `sk-xxx` 粘进来，会当场被拒。
//  2. **限额必须为正**，且至少要有一条（rpm / 日 token / 预算）。没有限额的条目 = 无限额授权。
//  3. **egress 必须显式**（域名 + 字节上限）。模型调用是数据外送，
//     默认全通等于把「这块业务的数据能不能出门」交给应用自己决定。
package models

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	RegistrySchema = "anc.models/v1"
	BindingSchema  = "anc.binding/v1"
)

var (
	reID     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	reSecret = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// Registry 是模型注册表：一个端点一行。**不含密文**，只有密钥的名字。
type Registry struct {
	Schema  string  `json:"schema"`
	Note    string  `json:"_readme,omitempty"`
	Entries []Entry `json:"entries"`
}

type Entry struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	Secret   string `json:"secret"` // 名字，不是值
	Limits   Limits `json:"limits"`
	Status   string `json:"status,omitempty"` // enabled（默认）| disabled
}

type Limits struct {
	RPM         int     `json:"rpm,omitempty"`
	DailyTokens int     `json:"daily_tokens,omitempty"`
	BudgetUSD   float64 `json:"budget_usd,omitempty"`
}

// Binding 是「一个挂到 ANC 上的应用」：它要什么、能往外送什么、怎么跑。
type Binding struct {
	Schema string   `json:"schema"`
	App    string   `json:"app"`
	Title  string   `json:"title,omitempty"`
	Models []string `json:"models"`
	Egress Egress   `json:"egress"`
	Run    Run      `json:"run"`
}

type Egress struct {
	Domains  []string `json:"domains"`
	MaxBytes int      `json:"max_bytes"`
}

type Run struct {
	Schedule string `json:"schedule,omitempty"`
	Timeout  string `json:"timeout,omitempty"`
	Sandbox  string `json:"sandbox,omitempty"` // no-net | egress-only
}

// LoadRegistry 读并校验注册表。未知字段报错（契约）。
func LoadRegistry(path string) (*Registry, error) {
	var r Registry
	if err := decodeStrict(path, &r); err != nil {
		return nil, err
	}
	if r.Schema != RegistrySchema {
		return nil, fmt.Errorf("%s：schema=%q，只认 %q", filepath.Base(path), r.Schema, RegistrySchema)
	}
	seen := map[string]bool{}
	for i, e := range r.Entries {
		where := fmt.Sprintf("第 %d 条（%s）", i+1, e.ID)
		if !reID.MatchString(e.ID) {
			return nil, fmt.Errorf("%s：id 必须是小写 ASCII（进票据与记账）", where)
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("%s：id 重复", where)
		}
		seen[e.ID] = true
		if strings.TrimSpace(e.Provider) == "" || strings.TrimSpace(e.Model) == "" {
			return nil, fmt.Errorf("%s：provider / model 不能为空", where)
		}
		if u, err := url.Parse(e.BaseURL); err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("%s：base_url 不是合法 URL", where)
		}
		// 这条就是「打码」的第一道闸：secret 必须是**名字**。
		if !reSecret.MatchString(e.Secret) {
			return nil, fmt.Errorf("%s：secret=%q 不像一个密钥名（应形如 DASHSCOPE_KEY_PROD）。"+
				"**不要把密钥值写进注册表** —— 注册表进 git", where, e.Secret)
		}
		if e.Limits.RPM <= 0 && e.Limits.DailyTokens <= 0 && e.Limits.BudgetUSD <= 0 {
			return nil, fmt.Errorf("%s：至少要有一条限额（rpm / daily_tokens / budget_usd）—— 没有限额等于无限授权", where)
		}
		if e.Status != "" && e.Status != "enabled" && e.Status != "disabled" {
			return nil, fmt.Errorf("%s：status=%q（enabled | disabled）", where, e.Status)
		}
	}
	return &r, nil
}

// Lookup 按 id 取条目。
func (r *Registry) Lookup(id string) (Entry, bool) {
	for _, e := range r.Entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Enabled：不写 status = 启用。
func (e Entry) Enabled() bool { return e.Status == "" || e.Status == "enabled" }

// LoadBinding 读并校验一个应用绑定；registry 用来做「注册名必须存在」的交叉校验。
func LoadBinding(path string, reg *Registry) (*Binding, error) {
	var b Binding
	if err := decodeStrict(path, &b); err != nil {
		return nil, err
	}
	if b.Schema != BindingSchema {
		return nil, fmt.Errorf("%s：schema=%q，只认 %q", filepath.Base(path), b.Schema, BindingSchema)
	}
	if !reID.MatchString(b.App) {
		return nil, fmt.Errorf("%s：app=%q 必须是小写 ASCII", filepath.Base(path), b.App)
	}
	if len(b.Models) == 0 {
		return nil, fmt.Errorf("%s：models 不能为空（挂上来的应用至少要声明用哪个注册名）", filepath.Base(path))
	}
	for _, id := range b.Models {
		if _, ok := reg.Lookup(id); !ok {
			return nil, fmt.Errorf("%s：models 里的 %q 不在注册表里（应用只能按**注册名**绑定）", filepath.Base(path), id)
		}
	}
	// egress 必须显式：没有域名 = 没有允许外送的数据域，那是配置错了，不是「全通」。
	if len(b.Egress.Domains) == 0 {
		return nil, fmt.Errorf("%s：egress.domains 不能为空 —— 模型调用是数据外送，"+
			"这块业务的数据能出门必须逐条写明（不许默认全通）", filepath.Base(path))
	}
	if b.Egress.MaxBytes <= 0 {
		return nil, fmt.Errorf("%s：egress.max_bytes 必须为正（一次外送的上限）", filepath.Base(path))
	}
	return &b, nil
}

// Mask 把密钥值打成 `****abcd`。空值 = 还没配。
// 这是**唯一**允许把密钥相关信息放到界面/日志上的形式。
func Mask(secretValue string) string {
	v := strings.TrimSpace(secretValue)
	if v == "" {
		return "（未配置）"
	}
	if len(v) <= 4 {
		return "****"
	}
	return "****" + v[len(v)-4:]
}

func decodeStrict(path string, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%s：%v（契约里没有的字段不许出现）", filepath.Base(path), err)
	}
	return nil
}

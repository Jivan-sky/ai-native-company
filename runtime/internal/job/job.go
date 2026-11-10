// Package job —— 「一件 ANC 自己会干的事」：契约 + 加载 + 校验。
//
// 为什么先有契约再写调度：定时任务第一次出故障时，最贵的不是没跑，是**跑了两次**。
// 所以 spec 里必须能回答四个问题：触发（什么时候）、动作（调哪个 API、怎么带凭据）、
// 落点（结果写哪）、**幂等键**（凭什么判定「这次和上次是同一件事」）。
//
// 三条口径：
//
//  1. **凭据只按名字引用**（`secret: GITHUB_TOKEN`），永远不写进 spec —— spec 要进 git。
//  2. **幂等键是数据**，不是代码里的 `time.Now()`：同一件事重复触发，只允许一次副作用。
//  3. **认不出的字段一律报错**（不是忽略）：spec 是契约，写错的字段静默失效 = 行为与写的人以为的不一样。
package job

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const Schema = "anc.job/v1"

// Spec 是一份作业。**一 job 一文件**（同 grants：一个 grant 一个文件）。
type Spec struct {
	Schema  string `json:"schema"`
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"` // 不写 = 开

	Schedule Schedule `json:"schedule"`
	Timeout  string   `json:"timeout,omitempty"` // Go duration；默认 60s
	Retry    Retry    `json:"retry,omitempty"`

	HTTP HTTP `json:"http"`
	// App 是另一种动作：跑一个**已审批的脚本应用**（与 http 二选一）。
	// 复用同一套 lease/幂等/记账 —— 调度只有一套，不然就是两本账。
	App *AppAction `json:"app,omitempty"`
	// Params 是会替换进 URL / headers / path 的占位值（`{{repo}}`）。
	// 只有这里列出来的才替换 —— spec 里不许出现「魔法变量」。
	Params map[string]string `json:"params,omitempty"`

	Write       Write  `json:"write"`
	Idempotency string `json:"idempotency,omitempty"` // 占位串；空 = 用日期（默认 {{date}}）

	// File 是它从哪来的（加载时填，不写进 JSON）。
	File string `json:"-"`
}

// Schedule 是「什么时候跑」。刻意只支持两种写法，不做 cron：
//   - every: "24h" / "1h" —— 间隔
//   - at:    "09:00"（配合 every=24h 用）—— 每天这个点
//
// 为什么不做 cron：这个产品要的是「每天/每小时干一件固定的事」，cron 的复杂度
// 换不来价值；真需要复杂表达式时，宁可让客户写一个 launchd/systemd 的 timer。
type Schedule struct {
	Every string `json:"every,omitempty"`
	At    string `json:"at,omitempty"`
	TZ    string `json:"tz,omitempty"`
}

// Retry 是失败时怎么办。max = 重试次数（不含首次）；backoff 是指数退避的基数。
type Retry struct {
	Max     int    `json:"max,omitempty"`
	Backoff string `json:"backoff,omitempty"`
}

// HTTP 是动作：一个 GET/POST。凭据按名字从 secrets 里取，不落 spec。
type HTTP struct {
	Method  string            `json:"method,omitempty"` // 默认 GET
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	// Auth 指向 secrets 里的一个键名。Scheme 决定 header 怎么写：
	//   bearer → Authorization: Bearer <v>；token → Authorization: token <v>；header:<Name> → <Name>: <v>
	Auth *Auth `json:"auth,omitempty"`
	// Expect 是「算成功」的状态码；不写 = 2xx。
	Expect []int `json:"expect,omitempty"`
}

type Auth struct {
	Secret string `json:"secret"`
	Scheme string `json:"scheme,omitempty"` // bearer | token | header:<Name>；默认 bearer
}

// Write 是落点：写一个文件（原子写），或只留痕不写文件。
type Write struct {
	Path   string `json:"path,omitempty"`   // vault 相对路径，支持 {{date}} 等占位
	Render string `json:"render,omitempty"` // raw（原文）| issues-md（GitHub issue 摘要）；默认 raw
}

// Load 读一份 spec 并校验。未知字段**报错**（json.DisallowUnknownFields）。
func Load(path string) (Spec, error) {
	f, err := os.Open(path)
	if err != nil {
		return Spec{}, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var s Spec
	if err := dec.Decode(&s); err != nil {
		return Spec{}, fmt.Errorf("%s：%v（spec 是契约，写错的字段不会静默失效）", filepath.Base(path), err)
	}
	s.File = path
	if err := s.validate(); err != nil {
		return Spec{}, fmt.Errorf("%s：%v", filepath.Base(path), err)
	}
	return s, nil
}

func (s Spec) validate() error {
	if s.Schema != Schema {
		return fmt.Errorf("schema=%q，只认 %q", s.Schema, Schema)
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("缺 name（幂等键与状态文件都靠它）")
	}
	if !reName.MatchString(s.Name) {
		return fmt.Errorf("name=%q 必须是小写 ASCII（会当状态文件名）", s.Name)
	}
	if s.App == nil && strings.TrimSpace(s.HTTP.URL) == "" {
		return fmt.Errorf("缺 http.url（或者写 app.name 跑一个应用）")
	}
	if s.App != nil {
		if strings.TrimSpace(s.App.Name) == "" {
			return fmt.Errorf("app.name 不能为空")
		}
		if strings.TrimSpace(s.HTTP.URL) != "" {
			return fmt.Errorf("http 与 app 只能选一个（一次只能做一件事）")
		}
	}
	if strings.TrimSpace(s.Schedule.Every) == "" && strings.TrimSpace(s.Schedule.At) == "" {
		return fmt.Errorf("缺 schedule.every（至少给一个间隔）")
	}
	if s.Schedule.Every != "" {
		if d, err := time.ParseDuration(s.Schedule.Every); err != nil || d <= 0 {
			return fmt.Errorf("schedule.every=%q 要是个正的时间段（如 24h）", s.Schedule.Every)
		}
	}
	if s.Timeout != "" {
		if d, err := time.ParseDuration(s.Timeout); err != nil || d <= 0 {
			return fmt.Errorf("timeout=%q 不是正的时间段", s.Timeout)
		}
	}
	if s.Retry.Backoff != "" {
		if d, err := time.ParseDuration(s.Retry.Backoff); err != nil || d <= 0 {
			return fmt.Errorf("retry.backoff=%q 不是正的时间段", s.Retry.Backoff)
		}
	}
	if s.HTTP.Auth != nil && strings.TrimSpace(s.HTTP.Auth.Secret) == "" {
		return fmt.Errorf("http.auth 给了却没写 secret")
	}
	return nil
}

// Timeout 取生效超时（默认 60s）。
func (s Spec) TimeoutDur() time.Duration {
	if s.Timeout == "" {
		return 60 * time.Second
	}
	d, _ := time.ParseDuration(s.Timeout)
	return d
}

// EnabledOrDefault：不写 enabled 就是开（关掉要显式写 false）。
func (s Spec) EnabledOrDefault() bool {
	if s.Enabled == nil {
		return true
	}
	return *s.Enabled
}

// AppAction 是「跑一个应用」这个动作。
type AppAction struct {
	Name string `json:"name"`
}

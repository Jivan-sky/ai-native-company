// Package queryexec —— 「以某个主体、按策略查一次数据源」的唯一实现。
//
// CLI（`anc db query`）与 HTTP（`/api/query`）都走这里：判定只有一份，
// 否则两个入口迟早会出现「命令行拦了、接口没拦」的洞。
package queryexec

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/catalog"
	"anc/internal/ds"
	"anc/internal/policy"
)

// Result 是一次受策略约束的查询结果。
type Result struct {
	DataSource string      `json:"datasource"`
	Subject    string      `json:"subject"`
	Columns    []string    `json:"columns"`
	Rows       [][]*string `json:"rows"`
	Returned   int         `json:"returned"`
	Truncated  bool        `json:"truncated"`
	Masked     []string    `json:"masked,omitempty"`
	Derived    []string    `json:"derived,omitempty"`
	Filters    []string    `json:"filters,omitempty"`
	DurationNS int64       `json:"duration_ns"`
}

// Run 以 subject 的身份查一次。错误分两类：
//   - 拒绝（策略/守卫）→ 返回的 error 文本就是理由，调用方据此记 denied；
//   - 执行失败（SQL/网络）→ 同样返回 error，但调用方记 failed。
func Run(ctx context.Context, vault, dsName, subject, sql string, timeout time.Duration) (Result, error) {
	res := Result{DataSource: dsName, Subject: subject}
	src, err := ds.Load(vault, dsName)
	if err != nil {
		return res, err
	}
	pol, err := policy.Load(filepath.Join(filepath.Dir(vault), "policies"), dsName)
	if err != nil {
		return res, fmt.Errorf("策略读不动：%v", err)
	}
	effective, filters, werr := pol.Wrap(sql)
	if werr != nil {
		return res, werr
	}
	res.Filters = filters

	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := ds.Query(cctx, src, passwordFor(vault, src), effective)
	if err != nil {
		return res, err
	}
	dec, aerr := pol.Apply(subject, columnTableMap(vault, dsName), knownColumns(vault, dsName), out.Columns, out.Rows)
	if aerr != nil {
		return res, aerr
	}
	res.Columns, res.Rows = out.Columns, out.Rows
	res.Returned, res.Truncated = out.Returned, out.Truncated
	res.Masked, res.Derived = dec.Masked, dec.Derived
	res.DurationNS = out.Duration.Nanoseconds()
	return res, nil
}

// passwordFor 从 secrets 取密码（调用方也可直接给；这里统一走文件）。
func passwordFor(vault string, src ds.Source) string {
	return Secrets(vault)[src.Secret]
}

// Secrets 读 `<vault>/../secrets.env`；读不到就返回空（缺密钥会在连接时报错）。
func Secrets(vault string) map[string]string {
	return secretsCache(filepath.Join(filepath.Dir(vault), "secrets.env"))
}

// 供 main 包注入（避免两个包各写一份 .env 解析）：默认实现是空的，main 会替换。
var secretsCache = func(string) map[string]string { return map[string]string{} }

// SetSecretsLoader 让 main 注入真正的 .env 解析器。
func SetSecretsLoader(f func(string) map[string]string) { secretsCache = f }

func columnTableMap(vault, dsName string) map[string]string {
	snap, err := catalog.Load(vault, dsName)
	if err != nil {
		return nil
	}
	count := map[string]int{}
	owner := map[string]string{}
	for _, t := range snap.Tables {
		for _, c := range t.Columns {
			count[c.Name]++
			owner[c.Name] = t.Name
		}
	}
	out := map[string]string{}
	for col, n := range count {
		if n == 1 {
			out[col] = owner[col]
		}
	}
	return out
}

func knownColumns(vault, dsName string) map[string]bool {
	snap, err := catalog.Load(vault, dsName)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, t := range snap.Tables {
		for _, c := range t.Columns {
			out[c.Name] = true
		}
	}
	return out
}

var _ = strings.TrimSpace

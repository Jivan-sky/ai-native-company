// Package ds —— 数据源：注册表 + 只读守卫 + 查询。
//
// 三条口径（数据中台的第一根柱子）：
//
//  1. **只连不搬**：查询结果只回给调用方，不落真相源；我们存的是「连接的定义」。
//  2. **只读是两层**：SQL 守卫（只许单条 SELECT/WITH）+ 会话级
//     `default_transaction_read_only=on`（服务端强制）。守卫能被绕过，服务端那层不能。
//  3. **有上限**：行数上限、超时、只读；没有上限的查询会把中台变成拖库工具。
package ds

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"anc/internal/pgwire"
)

const Schema = "anc.ds/v1"

var (
	reName   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	reSecret = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// Source 是一个数据源的定义（**不含密文**，只有密钥的名字）。
type Source struct {
	Schema   string `json:"schema"`
	Name     string `json:"name"`
	Title    string `json:"title,omitempty"`
	Type     string `json:"type"`   // postgres（mysql / file 后续）
	DSN      string `json:"dsn"`    // 例：postgres://user@host:5432/db?sslmode=disable
	Secret   string `json:"secret"` // 密码所在的环境变量名
	ReadOnly bool   `json:"read_only"`
	Timeout  string `json:"timeout,omitempty"`
	MaxRows  int    `json:"max_rows,omitempty"`
}

func Load(vault, name string) (Source, error) {
	p := filepath.Join(filepath.Dir(vault), "datasources", name+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		return Source{}, fmt.Errorf("没有这个数据源：%s", name)
	}
	var s Source
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return s, fmt.Errorf("%s：%v", name, err)
	}
	if s.Schema != Schema {
		return s, fmt.Errorf("%s：schema=%q，只认 %q", name, s.Schema, Schema)
	}
	if !reName.MatchString(s.Name) || s.Name != name {
		return s, fmt.Errorf("%s：name 必须与文件名一致且是小写 ASCII", name)
	}
	if s.Type != "postgres" {
		return s, fmt.Errorf("%s：type=%q（这一版只会 postgres）", name, s.Type)
	}
	if !reSecret.MatchString(s.Secret) {
		return s, fmt.Errorf("%s：secret=%q 必须是一个密钥名（值是密码，别写进注册表）", name, s.Secret)
	}
	if !s.ReadOnly {
		return s, fmt.Errorf("%s：read_only 必须为 true —— 这一版只做只读，写要单独一拍板", name)
	}
	if s.MaxRows <= 0 {
		return s, fmt.Errorf("%s：max_rows 必须为正（没有上限的查询等于拖库）", name)
	}
	return s, nil
}

func (s Source) timeout() time.Duration {
	if s.Timeout == "" {
		return 15 * time.Second
	}
	d, err := time.ParseDuration(s.Timeout)
	if err != nil || d <= 0 {
		return 15 * time.Second
	}
	return d
}

// Guard 是只读守卫：只放行**单条** SELECT / WITH。
// 它挡不住一切（比如函数里的写），所以服务端还有 `default_transaction_read_only=on`。
func Guard(sql string) error {
	s := strings.TrimSpace(sql)
	s = strings.TrimSuffix(s, ";")
	if s == "" {
		return fmt.Errorf("空查询")
	}
	if strings.Contains(s, ";") {
		return fmt.Errorf("只许一条语句（发现分号）")
	}
	upper := strings.ToUpper(s)
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return fmt.Errorf("只读数据源：只许 SELECT / WITH 开头")
	}
	for _, bad := range []string{"INSERT ", "UPDATE ", "DELETE ", "DROP ", "ALTER ", "CREATE ", "TRUNCATE ", "GRANT ", "COPY "} {
		if strings.Contains(upper, bad) {
			return fmt.Errorf("只读数据源：查询里出现了 %q", strings.TrimSpace(bad))
		}
	}
	return nil
}

// Result 是一次查询的结果。
type Result struct {
	Columns   []string      `json:"columns"`
	Rows      [][]*string   `json:"rows"`
	Returned  int           `json:"returned"`
	Truncated bool          `json:"truncated"`
	Duration  time.Duration `json:"duration_ns"`
	SQLHash   string        `json:"sql_hash"`
}

// Query 连上去跑一条只读查询。密码从调用方给的值里取（它从 secrets 来）。
func Query(ctx context.Context, s Source, password, sql string) (Result, error) {
	if err := Guard(sql); err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256([]byte(sql))
	res := Result{SQLHash: hex.EncodeToString(sum[:8])}

	dsn, err := pgwire.ParseDSN(s.DSN)
	if err != nil {
		return res, err
	}
	dsn.Password = password
	c, err := pgwire.Dial(ctx, dsn, s.timeout())
	if err != nil {
		return res, err
	}
	defer c.Close()

	// 服务端强制只读 + 语句超时：守卫是第一层，这层是兜底。
	if err := c.Exec(ctx, "SET default_transaction_read_only = on"); err != nil {
		return res, fmt.Errorf("开不了只读会话：%v", err)
	}
	if err := c.Exec(ctx, fmt.Sprintf("SET statement_timeout = %d", s.timeout().Milliseconds())); err != nil {
		return res, fmt.Errorf("设不了语句超时：%v", err)
	}
	start := time.Now()
	cols, rows, err := c.Query(ctx, sql)
	res.Duration = time.Since(start)
	if err != nil {
		return res, err
	}
	res.Columns = cols
	if len(rows) > s.MaxRows {
		res.Rows = rows[:s.MaxRows]
		res.Truncated = true
	} else {
		res.Rows = rows
	}
	res.Returned = len(res.Rows)
	return res, nil
}

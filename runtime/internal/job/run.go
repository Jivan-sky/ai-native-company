package job

// 跑一次作业：取凭据 → 发请求（带超时与退避重试）→ 渲染 → 原子落盘 → 记账。
//
// 这一层只做**一次执行**；「什么时候该跑」在 tick（调度）那一层 —— 两件事分开，
// 才能让「同一份 spec 手动跑一次」与「被定时器跑一次」走同一条路（证据才对得上）。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Result 是一次执行的结果。**成功与跳过的区分必须显式**：
// 跳过（幂等命中）不是失败，也不能算「又干了一次」。
type Result struct {
	Job      string        `json:"job"`
	Key      string        `json:"key"`    // 幂等键（这一次「是哪件事」）
	Status   string        `json:"status"` // ok | skipped | failed
	Code     int           `json:"code,omitempty"`
	Attempts int           `json:"attempts"`
	Bytes    int           `json:"bytes,omitempty"`
	Path     string        `json:"path,omitempty"` // vault 相对
	SHA256   string        `json:"sha256,omitempty"`
	Duration time.Duration `json:"duration_ns"`
	Why      string        `json:"why,omitempty"`
	At       time.Time     `json:"at"`
	NextAt   time.Time     `json:"next_at,omitempty"` // 调度层填：下一次该跑的时刻（给人看）
}

// State 是这一个作业的运行状态（落在 data 目录，不进 vault）。
type State struct {
	Job       string    `json:"job"`
	LastKey   string    `json:"last_key,omitempty"`
	LastOK    bool      `json:"last_ok,omitempty"`
	LastAt    time.Time `json:"last_at,omitempty"`
	LastCode  int       `json:"last_code,omitempty"`
	LastPath  string    `json:"last_path,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	Runs      int       `json:"runs"`
}

// Options 是执行时需要的外部输入。
type Options struct {
	Vault   string            // 真相源（write.path 相对它）
	DataDir string            // 运行态目录（状态文件）
	Secrets map[string]string // 凭据：键名 → 值
	Force   bool              // 忽略幂等（人工重跑）
	Now     time.Time         // 注入时钟（测试用；零值 = time.Now）
	Client  *http.Client      // 测试可注入
}

func (o Options) now() time.Time {
	if o.Now.IsZero() {
		return time.Now()
	}
	return o.Now
}

// Run 执行一次。返回 Result；error 只在「连结果都记不下来」时返回。
func Run(ctx context.Context, s Spec, opts Options) (Result, error) {
	now := opts.now()
	res := Result{Job: s.Name, At: now, Status: "failed"}
	key := s.expandKey(now)
	res.Key = key

	st, _ := loadState(opts.DataDir, s.Name)
	if !opts.Force && st.LastKey == key && st.LastOK {
		res.Status = "skipped"
		res.Why = "幂等键已跑成功过（" + key + "）—— 同一件事不再做第二遍；要重跑用 --force"
		return res, nil
	}

	req, err := s.buildRequest(ctx, now, opts.Secrets)
	if err != nil {
		res.Why = err.Error()
		return res, nil
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: s.TimeoutDur()}
	}

	start := time.Now()
	body, code, attempts, lastErr := s.do(ctx, client, req)
	res.Duration = time.Since(start)
	res.Attempts, res.Code = attempts, code
	if lastErr != nil {
		res.Why = lastErr.Error()
		st.merge(res, false)
		_ = saveState(opts.DataDir, st)
		return res, nil
	}
	out := render(s.Write.Render, body, s.Name, now)
	res.Bytes = len(out)

	if s.Write.Path != "" {
		rel := expandPath(s.Write.Path, s.Params, now)
		dst := filepath.Join(opts.Vault, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			res.Why = err.Error()
			st.merge(res, false)
			_ = saveState(opts.DataDir, st)
			return res, nil
		}
		// 原子写：中途被杀不会留半个文件（「看起来跑过了」是最坏的假象）。
		if err := writeAtomic(dst, out); err != nil {
			res.Why = err.Error()
			st.merge(res, false)
			_ = saveState(opts.DataDir, st)
			return res, nil
		}
		sum := sha256.Sum256(out)
		res.SHA256 = hex.EncodeToString(sum[:])
		res.Path = rel
	}

	res.Status = "ok"
	st.merge(res, true)
	_ = saveState(opts.DataDir, st)
	return res, nil
}

// do 发请求并重试。**只对「可能自愈」的错误重试**：网络错误 / 5xx / 429；
// 4xx（除 429）是契约问题，重试只会把同一个错误做三遍。
func (s Spec) do(ctx context.Context, c *http.Client, req *http.Request) ([]byte, int, int, error) {
	backoff := 30 * time.Second
	if s.Retry.Backoff != "" {
		backoff, _ = time.ParseDuration(s.Retry.Backoff)
	}
	var lastErr error
	code := 0
	for i := 0; i <= s.Retry.Max; i++ {
		r := req.Clone(ctx)
		resp, err := c.Do(r)
		if err != nil {
			lastErr = err
		} else {
			b, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			resp.Body.Close()
			code = resp.StatusCode
			if rerr != nil {
				lastErr = rerr
			} else if s.HTTP.expects(code) {
				return b, code, i + 1, nil
			} else {
				lastErr = fmt.Errorf("HTTP %d（不在 expect 里，也不是 2xx）", code)
				// 4xx 不重试：契约问题不是抖动。
				if code < 500 && code != 429 {
					return nil, code, i + 1, lastErr
				}
			}
		}
		if i < s.Retry.Max {
			select {
			case <-ctx.Done():
				return nil, code, i + 1, ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
			}
		}
	}
	return nil, code, s.Retry.Max + 1, lastErr
}

func (h HTTP) expects(code int) bool {
	if len(h.Expect) == 0 {
		return code >= 200 && code < 300
	}
	for _, c := range h.Expect {
		if c == code {
			return true
		}
	}
	return false
}

// buildRequest 按 spec 拼请求：占位替换 + 凭据注入。
func (s Spec) buildRequest(ctx context.Context, now time.Time, secrets map[string]string) (*http.Request, error) {
	url := expand(s.HTTP.URL, s.Params, now)
	method := strings.ToUpper(strings.TrimSpace(s.HTTP.Method))
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("URL 拼不出来：%v", err)
	}
	for k, v := range s.HTTP.Headers {
		req.Header.Set(k, expand(v, s.Params, now))
	}
	if s.HTTP.Auth != nil {
		name := s.HTTP.Auth.Secret
		v, ok := secrets[name]
		if !ok || strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("凭据 %q 不在 secrets 里（把它写进 ~/.anc/secrets.env，别写进 spec）", name)
		}
		switch scheme := strings.TrimSpace(s.HTTP.Auth.Scheme); {
		case scheme == "" || scheme == "bearer":
			req.Header.Set("Authorization", "Bearer "+v)
		case scheme == "token":
			req.Header.Set("Authorization", "token "+v)
		case strings.HasPrefix(scheme, "header:"):
			req.Header.Set(strings.TrimPrefix(scheme, "header:"), v)
		default:
			return nil, fmt.Errorf("auth.scheme=%q 不认识（bearer | token | header:<Name>）", scheme)
		}
	}
	return req, nil
}

// expandKey 算幂等键：默认 {{date}}（同一天只做一次）。
func (s Spec) expandKey(now time.Time) string {
	k := strings.TrimSpace(s.Idempotency)
	if k == "" {
		k = "{{date}}"
	}
	return expand(k, s.Params, now)
}

func expandPath(p string, params map[string]string, now time.Time) string {
	return filepath.ToSlash(expand(p, params, now))
}

// expand 替换 `{{name}}` 与 `{{date}}` / `{{datetime}}`。
// 只认这三个（params 里列出的 + 两个时间占位）—— 不认识的占位原样留着，
// 并在落盘路径里会体现出来（不静默替换成空）。
func expand(s string, params map[string]string, now time.Time) string {
	out := s
	for k, v := range params {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	out = strings.ReplaceAll(out, "{{date}}", now.Format("2006-01-02"))
	out = strings.ReplaceAll(out, "{{datetime}}", now.Format("2006-01-02T15:04:05Z07:00"))
	return out
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadState 读一个作业的运行状态（没有就返回 nil）。
func ReadState(dataDir, name string) (*State, bool) {
	st, err := loadState(dataDir, name)
	if err != nil {
		return nil, false
	}
	return &st, true
}

func statePath(dataDir, name string) string {
	return filepath.Join(dataDir, "jobs", name+".json")
}

func loadState(dataDir, name string) (State, error) {
	var st State
	b, err := os.ReadFile(statePath(dataDir, name))
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(b, &st)
	return st, err
}

func saveState(dataDir string, st State) error {
	p := statePath(dataDir, st.Job)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(p, append(b, '\n'))
}

func (st *State) merge(res Result, ok bool) {
	st.Job = res.Job
	st.Runs++
	if ok {
		st.LastKey = res.Key
		st.LastOK = true
		st.LastCode = res.Code
		st.LastPath = res.Path
		st.LastError = ""
	} else {
		// 失败**不算跑过**：幂等键不推进，否则一次偶发失败会让今天这件事永远不做。
		st.LastOK = false
		st.LastCode = res.Code
		st.LastError = res.Why
	}
	st.LastAt = res.At
}

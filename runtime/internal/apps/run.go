package apps

// 跑一个应用：**先验身份，再进沙箱**。
//
// 拒绝优先于执行，三条：
//   - 未审批 / 已撤销 → 不跑；
//   - 目录内容与安装时 hash 对不上 → 不跑（审批的是那一个内容）；
//   - 没有沙箱（找不到 docker）→ 不跑。**绝不退化成裸跑**：
//     裸跑别人上传的脚本，等于把「上传」做成任意代码执行。

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Output 是一次沙箱执行的结果。
type Output struct {
	Code   int
	Stdout string
	Stderr string
}

// Runner 是「跑一段脚本」的抽象（测试注入假实现，不依赖 docker）。
type Runner interface {
	Run(ctx context.Context, dir, entry, image string, timeout time.Duration) (Output, error)
}

// DockerRunner 用 docker 跑：无网络、只读根、丢掉全部 capability、临时 /tmp、非 root。
type DockerRunner struct{}

func (DockerRunner) Run(ctx context.Context, dir, entry, image string, timeout time.Duration) (Output, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return Output{}, fmt.Errorf("没有沙箱（找不到 docker）—— 拒绝执行别人上传的脚本")
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "docker", "run", "--rm",
		"--network=none",
		"--read-only",
		"--tmpfs", "/tmp:rw,size=16m",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--user", "65534:65534",
		"-v", dir+":/app:ro",
		"-w", "/app",
		image, "/bin/sh", "-c", "./"+entry,
	)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	res := Output{Stdout: out.String(), Stderr: errb.String()}
	if cctx.Err() == context.DeadlineExceeded {
		return res, fmt.Errorf("超时（%s）——沙箱被强杀", timeout)
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.Code = ee.ExitCode()
			return res, nil
		}
		return res, err
	}
	return res, nil
}

// Result 是一次应用运行的结果。
type Result struct {
	App      string        `json:"app"`
	Status   string        `json:"status"` // ok | refused | failed
	Why      string        `json:"why,omitempty"`
	Code     int           `json:"code,omitempty"`
	Output   string        `json:"output,omitempty"`
	Duration time.Duration `json:"duration_ns"`
	At       time.Time     `json:"at"`
}

type Options struct {
	Runner Runner
	Now    func() time.Time
	Record func(Result, Installed) // 记账（CLI 注入；测试可为 nil）
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Run 跑一个已审批的应用。拒绝时 Status=refused，**不执行**。
func Run(ctx context.Context, vault, name string, opts Options) (Result, error) {
	res := Result{App: name, At: opts.now()}
	st, err := Load(vault, name)
	if err != nil {
		res.Status, res.Why = "refused", err.Error()
		return res, nil
	}
	switch st.State {
	case "approved":
	case "revoked":
		res.Status, res.Why = "refused", "已撤销 —— 撤销是立刻生效的"
		opts.record(res, st)
		return res, nil
	default:
		res.Status, res.Why = "refused", "还没审批（state="+st.State+"）—— 未审批的应用不许跑"
		opts.record(res, st)
		return res, nil
	}
	// 身份：跑之前重算一次内容哈希。
	sum, err := HashDir(st.Dir)
	if err != nil {
		res.Status, res.Why = "failed", "读不到应用目录："+err.Error()
		opts.record(res, st)
		return res, nil
	}
	if sum != st.SHA256 {
		res.Status = "refused"
		res.Why = fmt.Sprintf("内容与安装时不一致（%s… != %s…）—— 审批的是那一个版本，不是这个目录", sum[:12], st.SHA256[:12])
		opts.record(res, st)
		return res, nil
	}
	m, err := LoadManifest(st.Dir)
	if err != nil {
		res.Status, res.Why = "failed", "清单读不出来："+err.Error()
		opts.record(res, st)
		return res, nil
	}
	runner := opts.Runner
	if runner == nil {
		runner = DockerRunner{}
	}
	start := time.Now()
	out, err := runner.Run(ctx, st.Dir, m.Entrypoint, m.Image(), m.Timeout())
	res.Duration = time.Since(start)
	res.Code = out.Code
	res.Output = strings.TrimSpace(out.Stdout + "\n" + out.Stderr)
	switch {
	case err != nil:
		res.Status, res.Why = "failed", err.Error()
	case out.Code != 0:
		res.Status, res.Why = "failed", fmt.Sprintf("退出码 %d", out.Code)
	default:
		res.Status = "ok"
	}
	opts.record(res, st)
	return res, nil
}

func (o Options) record(res Result, st Installed) {
	if o.Record != nil {
		o.Record(res, st)
	}
}

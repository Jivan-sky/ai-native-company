package board

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"anc/internal/job"
)

// 接入 = 写口。这是看板上**唯一**会落盘的地方，所以它按「生产变更」做，不按「开关」做：
//
//	预演（不写盘） → diff（改了什么） → 校验（写成 spec 还能不能读回来）
//	→ 备份旧版 → 原子落盘 → 失败就什么都不动
//
// 三件事一起才叫事务：**要么完整成功，要么完全不动**。
// 中间态（文件写了半截 / 写了但读不回来）比不写更糟 —— 调度器会在半小时后按那份坏 spec 去跑。

// intakeDir 是作业目录：`<vault>/../jobs`，与 CLI 的默认口径一致（见 board/jobs.go）。
func (s *Server) intakeDir() string {
	return filepath.Join(filepath.Dir(s.Vault), "jobs")
}

// nameOK：spec 名字同时也是文件名，所以只收小写字母、数字、短横线。
// 这一条同时挡住路径穿越（`../`、绝对路径）—— 文件名是我们拼的，不是用户拼的。
var nameOK = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type intakeReq struct {
	Spec json.RawMessage `json:"spec"`
}

// intakeFacts 是「把它跑起来会发生什么」—— 全部由 spec 本身推出，不猜。
func intakeFacts(sp job.Spec) []string {
	var out []string
	sched := sp.Schedule.Every
	if strings.TrimSpace(sched) == "" {
		sched = "（没写间隔）"
	}
	if at := strings.TrimSpace(sp.Schedule.At); at != "" {
		sched += " 的 " + at
	}
	tz := strings.TrimSpace(sp.Schedule.TZ)
	if tz == "" {
		tz = "没写时区"
	}
	out = append(out, "每 "+sched+"（"+tz+"）跑一次")
	if sp.App != nil && strings.TrimSpace(sp.App.Name) != "" {
		out = append(out, "跑已审批的应用 "+sp.App.Name+"（审批与沙箱由 anc app 管）")
	} else {
		m := strings.ToUpper(strings.TrimSpace(sp.HTTP.Method))
		if m == "" {
			m = "GET"
		}
		out = append(out, "请求 "+m+" "+strings.TrimSpace(sp.HTTP.URL))
		if sp.HTTP.Auth != nil && strings.TrimSpace(sp.HTTP.Auth.Secret) != "" {
			out = append(out, "用密钥名 "+sp.HTTP.Auth.Secret+"（值在 secrets.env，不经过看板）")
		} else {
			out = append(out, "不带凭据")
		}
	}
	to := strings.TrimSpace(sp.Timeout)
	if to == "" {
		to = "默认 60s"
	}
	if strings.TrimSpace(sp.Write.Path) == "" {
		out = append(out, "超时 "+to+"；不落盘")
	} else {
		out = append(out, "超时 "+to+"；写到 "+sp.Write.Path)
	}
	return out
}

// flatSpec 把 spec 摊成 路径→值，用来做逐字段 diff。
func flatSpec(v any, prefix string, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			flatSpec(t[k], p, out)
		}
	case []any:
		b, _ := json.Marshal(t)
		out[prefix] = string(b)
	case string:
		out[prefix] = t
	case float64, bool, nil:
		out[prefix] = fmt.Sprint(t)
	default:
		b, _ := json.Marshal(t)
		out[prefix] = string(b)
	}
}

// specDiff 给出「改了什么」。三态：new（新建）/ replace（会替换旧版）/ same（一字不差）。
func specDiff(oldSpec, newSpec map[string]any) (string, []string) {
	of := map[string]string{}
	nf := map[string]string{}
	flatSpec(oldSpec, "", of)
	flatSpec(newSpec, "", nf)
	keys := map[string]bool{}
	for k := range of {
		keys[k] = true
	}
	for k := range nf {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	var lines []string
	for _, k := range sorted {
		o, inOld := of[k]
		n, inNew := nf[k]
		switch {
		case inOld && inNew && o == n:
			continue
		case inOld && !inNew:
			lines = append(lines, "- "+k+": "+o)
		case !inOld && inNew:
			lines = append(lines, "+ "+k+": "+n)
		default:
			lines = append(lines, "~ "+k+": "+o+" → "+n)
		}
	}
	if len(lines) == 0 {
		return "same", nil
	}
	return "replace", lines
}

// intakePreviewBody 是预演的全部输出。前端照着画，服务端不改口。
type intakePreviewBody struct {
	OK       bool            `json:"ok"`
	Spec     json.RawMessage `json:"spec,omitempty"`
	Facts    []string        `json:"facts,omitempty"`
	Diff     string          `json:"diff,omitempty"`
	Changes  []string        `json:"changes,omitempty"`
	Problems []string        `json:"problems,omitempty"`
	Path     string          `json:"path,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// parseAndCheck 是预演与落盘共用的第一段：读 spec、体检、算出落点。
func (s *Server) parseAndCheck(raw json.RawMessage) (job.Spec, string, []string, error) {
	var sp job.Spec
	if err := json.Unmarshal(raw, &sp); err != nil {
		return sp, "", nil, fmt.Errorf("这份 spec 读不懂：%v", err)
	}
	sp.Name = strings.TrimSpace(sp.Name)
	if !nameOK.MatchString(sp.Name) {
		return sp, "", nil, fmt.Errorf("名字 %q 不能当文件名：只收小写字母、数字、短横线，且以字母开头", sp.Name)
	}
	if strings.TrimSpace(sp.Title) == "" {
		return sp, "", nil, fmt.Errorf("缺一句话（title）—— 以后在作业列表里靠它认人")
	}
	// 动作二选一：HTTP 或 app。两个都空 = 跑起来什么也不干，那是坏 spec 不是「都可以」。
	hasHTTP := strings.TrimSpace(sp.HTTP.URL) != ""
	hasApp := sp.App != nil && strings.TrimSpace(sp.App.Name) != ""
	if hasHTTP == hasApp {
		return sp, "", nil, fmt.Errorf("动作要二选一：写 http.url 或 app.name，不能都写也不能都不写")
	}
	if strings.TrimSpace(sp.Schedule.Every) == "" {
		return sp, "", nil, fmt.Errorf("缺 schedule.every（例如 24h）—— 没有间隔就不知道什么时候跑")
	}
	p := filepath.Join(s.intakeDir(), sp.Name+".json")
	return sp, p, nil, nil
}

// handleIntakePreview 只算不写。它必须能被反复点 —— 预演产生副作用就不叫预演了。
func (s *Server) handleIntakePreview(w http.ResponseWriter, r *http.Request) {
	var req intakeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 200, intakePreviewBody{Error: "请求体读不懂：" + err.Error()})
		return
	}
	sp, path, problems, err := s.parseAndCheck(req.Spec)
	if err != nil {
		writeJSON(w, 200, intakePreviewBody{Problems: append(problems, err.Error())})
		return
	}
	// 规范化后的 spec：前端拿到的就是将来落盘的那一份，不是它自己拼的那一份。
	norm, _ := json.MarshalIndent(sp, "", "  ")
	body := intakePreviewBody{OK: true, Spec: norm, Facts: intakeFacts(sp), Path: path}

	newMap := map[string]any{}
	_ = json.Unmarshal(norm, &newMap)
	body.Diff = "new"
	if old, rerr := os.ReadFile(path); rerr == nil {
		var oldSpec job.Spec
		if json.Unmarshal(old, &oldSpec) != nil {
			body.Problems = append(body.Problems,
				"落点上已经有一个不是 spec 的文件 —— 不覆盖它（换个名字，或先自己处理）")
		} else {
			oldNorm, _ := json.Marshal(oldSpec)
			oldMap := map[string]any{}
			_ = json.Unmarshal(oldNorm, &oldMap)
			body.Diff, body.Changes = specDiff(oldMap, newMap)
			// 保留 enabled：替换一个已停用的作业，不该把它悄悄启回来。
			if oldSpec.Enabled != nil && !*oldSpec.Enabled && sp.Enabled == nil {
				body.Problems = append(body.Problems,
					"这一条现在是**停用**的；这份 spec 没写 enabled，落盘后会变成启用 —— 确认要这样再点生效")
			}
		}
	}
	writeJSON(w, 200, body)
}

// handleIntakeApply 落盘。顺序不可换：校验 → 写临时 → **读回来验一遍** → 备份旧版 → 原子改名。
func (s *Server) handleIntakeApply(w http.ResponseWriter, r *http.Request) {
	var req intakeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 200, intakePreviewBody{Error: "请求体读不懂：" + err.Error()})
		return
	}
	sp, path, _, err := s.parseAndCheck(req.Spec)
	if err != nil {
		writeJSON(w, 200, intakePreviewBody{Problems: []string{err.Error()}})
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		writeJSON(w, 200, intakePreviewBody{Error: "建不了作业目录：" + err.Error()})
		return
	}
	norm, _ := json.MarshalIndent(sp, "", "  ")
	norm = append(norm, '\n')

	// 1) 写到同目录的临时文件（同一次 rename 才能原子）
	tmp := filepath.Join(filepath.Dir(path), ".tmp-"+sp.Name+"-"+fmt.Sprint(os.Getpid()))
	if err := os.WriteFile(tmp, norm, 0o644); err != nil {
		writeJSON(w, 200, intakePreviewBody{Error: "写临时文件失败：" + err.Error()})
		return
	}
	// 2) 读回来验一遍：写得进去不等于读得回来（未知字段、类型错都要在这一步暴露）
	if _, lerr := job.Load(tmp); lerr != nil {
		_ = os.Remove(tmp)
		writeJSON(w, 200, intakePreviewBody{Problems: []string{"写出来的 spec 自己读不回来：" + lerr.Error()}})
		return
	}
	// 3) 旧版备份：替换必须能退回去
	backup := ""
	if _, serr := os.Stat(path); serr == nil {
		dir := filepath.Join(filepath.Dir(path), ".backup")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			_ = os.Remove(tmp)
			writeJSON(w, 200, intakePreviewBody{Error: "建不了备份目录：" + err.Error()})
			return
		}
		backup = filepath.Join(dir, sp.Name+"."+time.Now().Format("20060102-150405")+".json")
		if err := os.Rename(path, backup); err != nil {
			_ = os.Remove(tmp)
			writeJSON(w, 200, intakePreviewBody{Error: "备份旧版失败（没有它就替换是耍流氓）：" + err.Error()})
			return
		}
	}
	// 4) 原子落地。失败就把备份放回去 —— 不留「备份走了、新文件没来」的中间态。
	if err := os.Rename(tmp, path); err != nil {
		if backup != "" {
			_ = os.Rename(backup, path)
		}
		_ = os.Remove(tmp)
		writeJSON(w, 200, intakePreviewBody{Error: "落盘失败，已回滚：" + err.Error()})
		return
	}
	writeJSON(w, 200, intakePreviewBody{
		OK:    true,
		Spec:  norm,
		Facts: intakeFacts(sp),
		Path:  path,
		Diff:  map[bool]string{true: "replaced", false: "created"}[backup != ""],
		Changes: func() []string {
			if backup == "" {
				return nil
			}
			return []string{"上一版备份在 " + backup}
		}(),
	})
}

// handleIntakeStop 停用：把 enabled 改成 false，不删文件。
//
// 为什么不是删除：删掉就看不见「曾经接过什么」。停用留着 spec 与它的历史，
// 要真删，人去命令行删 —— 那是一个更重的动作，不该在网页上一个按钮完成。
func (s *Server) handleIntakeStop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 200, intakePreviewBody{Error: "请求体读不懂：" + err.Error()})
		return
	}
	name := strings.TrimSpace(req.Name)
	if !nameOK.MatchString(name) {
		writeJSON(w, 200, intakePreviewBody{Problems: []string{"名字不合法：" + name}})
		return
	}
	path := filepath.Join(s.intakeDir(), name+".json")
	sp, err := job.Load(path)
	if err != nil {
		writeJSON(w, 200, intakePreviewBody{Error: "读不到这一条：" + err.Error()})
		return
	}
	off := false
	sp.Enabled = &off
	norm, _ := json.MarshalIndent(sp, "", "  ")
	norm = append(norm, '\n')
	tmp := filepath.Join(filepath.Dir(path), ".tmp-"+name+"-"+fmt.Sprint(os.Getpid()))
	if err := os.WriteFile(tmp, norm, 0o644); err != nil {
		writeJSON(w, 200, intakePreviewBody{Error: "写临时文件失败：" + err.Error()})
		return
	}
	if _, lerr := job.Load(tmp); lerr != nil {
		_ = os.Remove(tmp)
		writeJSON(w, 200, intakePreviewBody{Problems: []string{"停用后读不回来：" + lerr.Error()}})
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		writeJSON(w, 200, intakePreviewBody{Error: "停用失败：" + err.Error()})
		return
	}
	writeJSON(w, 200, intakePreviewBody{OK: true, Path: path, Facts: []string{name + " 已停用（spec 留着，enabled=false）"}})
}

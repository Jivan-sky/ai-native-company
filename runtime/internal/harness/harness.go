// Package harness 是「外部 harness 接入口径」的那张表 —— 以及照着它取数的口径。
//
// 为什么在数据里，不在代码里：每接一家 harness，要回答的都是同样几个问题 ——
// 记录根在哪（哪个环境变量、哪个默认目录）、项目目录怎么起名、主记录叫什么、
// 子任务记录在哪、会话句柄是什么、记录是什么格式。这些是**外部世界的事实**，
// 不是我们的逻辑；而外部事实一定会变（第三家的目录布局 / 压缩格式 / 云端落点）。
//
// 写进代码的代价在下游才显出来：改一家就要改一趟代码、重编、重测、重发。
// 所以：**口径是数据**（`harnesses.json`，可用 `--harnesses <文件>` 整份替换，
// 与 judge 的判据表同一套做法），**代码只读表**。
//
// 加一家 = 加一行数据。只有「读的语义」真的不一样时才需要加一个 reader；
// 表里 `reader` 为空的家族，代码会明确拒绝读它（**明说读不到，不当成 0**）。
package harness

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Schema 是这张表的版本。
const Schema = "anc.harnesses/v1"

// D irRule* 是「项目目录怎么起名」的已知规则。规则名进数据，规则实现只此一份。
const (
	// RuleNonalnumDash：每个非字母数字字符 → '-'（实测：claude 与 codebuddy 都是这一条）。
	RuleNonalnumDash = "nonalnum-to-dash"
)

//go:embed harnesses.json
var embedded []byte

// Layout 是记录在记录根下面怎么摆。字段全是**相对路径模式**，不是绝对路径。
type Layout struct {
	// DirRule 见 Rule* 常量。
	DirRule string `json:"dir_rule"`
	// ProjectsDir 是「一家一个目录」的上一层（claude 是 projects/）。
	ProjectsDir string `json:"projects_dir"`
	// MainFile 是主记录的文件名模式；`<id>` 会被换成会话句柄。
	MainFile string `json:"main_file"`
	// SubagentDir 是子任务记录的目录模式（相对主记录所在目录）；空 = 这家没有子任务记录。
	SubagentDir string `json:"subagent_dir,omitempty"`
	// SubagentGlob 是子任务记录的 glob（在 SubagentDir 下）。空 = 同上。
	SubagentGlob string `json:"subagent_glob,omitempty"`
	// SubagentMetaSuffix 是子任务记录旁边的元数据后缀（claude 是 .meta.json）。
	SubagentMetaSuffix string `json:"subagent_meta_suffix,omitempty"`
}

// Family 是一家的接入口径。
type Family struct {
	ID      string `json:"id"`
	Display string `json:"display"`
	// RootEnv 是记录根的环境变量名，按顺序取第一个有值的。
	RootEnv []string `json:"root_env,omitempty"`
	// RootDir 是兜底：都取不到时，用 <用户目录>/RootDir。
	RootDir string `json:"root_dir,omitempty"`
	// RecordFormat 是记录形态（jsonl-plain / jsonl-zstd / none…）。**描述**，不是承诺。
	RecordFormat string `json:"record_format"`
	// SessionHandle 是句柄从哪来（file-stem / parent-dir-name / index-id…）。**描述**。
	SessionHandle string `json:"session_handle,omitempty"`
	// Reader 是读取器名；空 = 这家只登记了口径、还没有读取器，代码必须拒绝读。
	Reader string `json:"reader,omitempty"`
	Layout Layout `json:"layout"`
	// Note 给读表的人：这条口径是从哪来的、哪些还没验。
	Note string `json:"note,omitempty"`
}

// Table 是整张表。
type Table struct {
	Schema   string   `json:"schema"`
	Families []Family `json:"families"`
}

// ErrNoReader 说明为什么这家读不了：它只登记了口径。
type ErrNoReader struct {
	ID      string
	Display string
	Format  string
	Note    string
}

func (e ErrNoReader) Error() string {
	why := fmt.Sprintf("%s（%s）只登记了口径，没有读取器", e.Display, e.ID)
	if e.Format != "" {
		why += "（记录形态 " + e.Format + "）"
	}
	if e.Note != "" {
		why += " —— " + e.Note
	}
	return why
}

// Load 读表。path 为空 = 用内置那一份；给了路径 = **整份替换**（与 --rules 同一套做法：
// 不做逐项合并，避免「一半来自内置、一半来自文件」这种事后说不清的状态）。
func Load(path string) (Table, error) {
	raw := embedded
	from := "内置 harnesses.json"
	if p := strings.TrimSpace(path); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return Table{}, fmt.Errorf("读不到接入口径表 %s: %w", p, err)
		}
		raw, from = b, p
	}
	var t Table
	if err := json.Unmarshal(raw, &t); err != nil {
		return Table{}, fmt.Errorf("接入口径表解析失败（%s）: %w", from, err)
	}
	if err := t.validate(); err != nil {
		return Table{}, fmt.Errorf("接入口径表不合法（%s）: %w", from, err)
	}
	return t, nil
}

func (t Table) validate() error {
	if t.Schema != Schema {
		return fmt.Errorf("schema 是 %q，想要 %q", t.Schema, Schema)
	}
	if len(t.Families) == 0 {
		return fmt.Errorf("families 是空的")
	}
	seen := map[string]bool{}
	for _, f := range t.Families {
		if strings.TrimSpace(f.ID) == "" {
			return fmt.Errorf("有一家的 id 是空的")
		}
		if seen[f.ID] {
			return fmt.Errorf("id 重复：%s", f.ID)
		}
		seen[f.ID] = true
		if strings.TrimSpace(f.Display) == "" {
			return fmt.Errorf("%s 的 display 是空的", f.ID)
		}
		if strings.TrimSpace(f.RecordFormat) == "" {
			return fmt.Errorf("%s 的 record_format 是空的", f.ID)
		}
		if f.RecordFormat == "none" {
			continue // 没有记录的家：布局本就不该有
		}
		if strings.TrimSpace(f.Layout.ProjectsDir) == "" {
			return fmt.Errorf("%s 的 layout.projects_dir 是空的", f.ID)
		}
		if strings.TrimSpace(f.Layout.MainFile) == "" {
			return fmt.Errorf("%s 的 layout.main_file 是空的", f.ID)
		}
		if _, err := ruleFunc(f.Layout.DirRule); err != nil {
			return fmt.Errorf("%s：%w", f.ID, err)
		}
	}
	return nil
}

// Lookup 按 id 取一家。
func (t Table) Lookup(id string) (Family, bool) {
	want := strings.ToLower(strings.TrimSpace(id))
	for _, f := range t.Families {
		if strings.ToLower(f.ID) == want {
			return f, true
		}
	}
	return Family{}, false
}

// IDs 列出表里所有 id（稳定顺序，给报错信息用）。
func (t Table) IDs() []string {
	out := make([]string, 0, len(t.Families))
	for _, f := range t.Families {
		out = append(out, f.ID)
	}
	sort.Strings(out)
	return out
}

// DefaultID 是没指定 --harness 时用哪一家。
//
// 它写在这里、不是散在调用点上：这是**当前只有 claude 有读取器**这一个事实的投影，
// 等第二家的读取器落地，改这一行就够。
const DefaultID = "claude"

// Default 是内置表（解析不了就是构建坏了，直接 panic —— 那属于开发期错误）。
func Default() Table {
	t, err := Load("")
	if err != nil {
		panic("内置接入口径表不合法: " + err.Error())
	}
	return t
}

// DefaultFamily 是内置表里的默认那一家。
func DefaultFamily() Family {
	f, ok := Default().Lookup(DefaultID)
	if !ok {
		panic("内置接入口径表里没有 " + DefaultID)
	}
	return f
}

// ResolveRoot 按「旗标 > 环境变量 > 用户目录下的 RootDir」定出记录根。
// 第二个返回值是**理由**：给人看「这个路径是哪来的」，路径对不对得先看这句。
func (f Family) ResolveRoot(flagVal string) (string, string) {
	if v := strings.TrimSpace(flagVal); v != "" {
		return v, "旗标指定"
	}
	for _, env := range f.RootEnv {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v, "$" + env
		}
	}
	if f.RootDir == "" {
		return "", f.Display + " 没有声明记录根（表里 root_dir 是空的）"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "拿不到用户目录"
	}
	return filepath.Join(home, filepath.FromSlash(f.RootDir)), "用户目录下的 " + f.RootDir
}

// Slug 按规则给项目目录起名。规则名进数据，实现只此一份 —— 上游（如 render 的
// work_dir 反推）和下游（找记录）必须用同一条，不然就是两份会漂的规则。
func Slug(rule, workDir string) (string, error) {
	fn, err := ruleFunc(rule)
	if err != nil {
		return "", err
	}
	return fn(workDir), nil
}

func ruleFunc(rule string) (func(string) string, error) {
	switch rule {
	case RuleNonalnumDash:
		return func(s string) string {
			var b strings.Builder
			b.Grow(len(s))
			for _, r := range s {
				switch {
				case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
					b.WriteRune(r)
				default:
					b.WriteByte('-')
				}
			}
			return b.String()
		}, nil
	case "":
		return nil, fmt.Errorf("dir_rule 是空的")
	default:
		return nil, fmt.Errorf("不认识的 dir_rule：%q", rule)
	}
}

// CanRead 说清这家能不能被读。**不能读就必须明说** —— 静默少报和假绿是同一类错误。
func (f Family) CanRead() error {
	if strings.TrimSpace(f.Reader) == "" {
		return ErrNoReader{ID: f.ID, Display: f.Display, Format: f.RecordFormat, Note: f.Note}
	}
	return nil
}

// TranscriptDir 是某段会话的原生记录落在哪：<root>/<ProjectsDir>/<slug(workDir)>。
func (f Family) TranscriptDir(root, workDir string) (string, error) {
	slug, err := Slug(f.Layout.DirRule, workDir)
	if err != nil {
		return "", fmt.Errorf("%s：%w", f.ID, err)
	}
	return filepath.Join(root, filepath.FromSlash(f.Layout.ProjectsDir), slug), nil
}

// MainPath 是主记录的文件路径。`<id>` 是句柄，**不是**文件名占位符之外的任何东西。
func (f Family) MainPath(dir, id string) string {
	return filepath.Join(dir, strings.ReplaceAll(f.Layout.MainFile, "<id>", id))
}

// SubagentPaths 列出子任务记录的 glob 与旁边的元数据路径。
// 这家没有子任务记录时，glob 为空串。
func (f Family) SubagentPaths(dir, id string) (glob string, metaSuffix string) {
	if f.Layout.SubagentDir == "" || f.Layout.SubagentGlob == "" {
		return "", ""
	}
	sub := filepath.Join(dir, strings.ReplaceAll(f.Layout.SubagentDir, "<id>", id))
	return filepath.Join(sub, f.Layout.SubagentGlob), f.Layout.SubagentMetaSuffix
}

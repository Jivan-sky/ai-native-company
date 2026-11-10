// Package apps —— 挂到 ANC 上的「脚本应用」：装进来、审批、跑、撤销。
//
// 三条口径：
//
//  1. **内容先定身份**：装的时候对整个目录算 sha256，之后每次跑都重算；
//     对不上就拒绝 —— 审批的是那一个版本，不是这个目录。
//  2. **未审批不许跑**：install 之后状态是 pending；approve 记谁、何时；revoke 立刻生效。
//  3. **没有沙箱就不跑**：本机没有 docker（或沙箱起不来）时**拒绝执行**，不是「退化成裸跑」。
//     裸跑一个别人上传的脚本，等于把「上传」做成任意代码执行。
package apps

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const Schema = "anc.app/v1"

// Manifest 是应用自报的东西（进包，随 hash 一起被审批）。
type Manifest struct {
	Schema     string       `json:"schema"`
	Name       string       `json:"name"`
	Title      string       `json:"title,omitempty"`
	Version    string       `json:"version"`
	Entrypoint string       `json:"entrypoint"` // 相对包根，例如 run.sh
	Models     []string     `json:"models,omitempty"`
	Egress     modelsEgress `json:"egress,omitempty"`
	Run        RunSpec      `json:"run,omitempty"`
}

// modelsEgress 与注册表那套同名同义（这里只做结构，不引入依赖环）。
type modelsEgress struct {
	Domains  []string `json:"domains,omitempty"`
	MaxBytes int      `json:"max_bytes,omitempty"`
}

type RunSpec struct {
	Timeout string `json:"timeout,omitempty"` // 默认 60s
	Image   string `json:"image,omitempty"`   // 默认 alpine:3 —— 必须显式钉版本，不许 latest
}

// Installed 是本机的安装记录（**不在包内**，应用自己改不到）。
type Installed struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	SHA256      string `json:"sha256"`
	Dir         string `json:"dir"`
	State       string `json:"state"` // pending | approved | revoked
	ApprovedBy  string `json:"approved_by,omitempty"`
	ApprovedAt  string `json:"approved_at,omitempty"`
	InstalledAt string `json:"installed_at"`
}

// HashDir 对目录算内容哈希：按相对路径排序，路径与内容一起进哈希
// （只哈希内容的话，把 a.sh 改名成 b.sh 就绕过去了）。
func HashDir(dir string) (string, error) {
	var files []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		fmt.Fprintf(h, "%s\x00", rel)
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// LoadManifest 读并校验 app.json（未知字段报错）。
func LoadManifest(dir string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, "app.json"))
	if err != nil {
		return m, fmt.Errorf("没有 app.json：%v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("app.json：%v", err)
	}
	if m.Schema != Schema {
		return m, fmt.Errorf("schema=%q，只认 %q", m.Schema, Schema)
	}
	if !reName.MatchString(m.Name) || strings.TrimSpace(m.Version) == "" {
		return m, fmt.Errorf("name=%q / version=%q 不合法", m.Name, m.Version)
	}
	entry := filepath.Clean(m.Entrypoint)
	if entry == "" || entry == "." || strings.HasPrefix(entry, "..") || filepath.IsAbs(entry) {
		return m, fmt.Errorf("entrypoint=%q 必须是包内相对路径", m.Entrypoint)
	}
	if _, err := os.Stat(filepath.Join(dir, entry)); err != nil {
		return m, fmt.Errorf("entrypoint 不存在：%s", entry)
	}
	if m.Run.Image != "" && strings.HasSuffix(m.Run.Image, ":latest") {
		return m, fmt.Errorf("run.image 不许用 latest（要能复现，就得钉版本）")
	}
	return m, nil
}

// Timeout 取生效超时（默认 60s）。
func (m Manifest) Timeout() time.Duration {
	if m.Run.Timeout == "" {
		return 60 * time.Second
	}
	d, err := time.ParseDuration(m.Run.Timeout)
	if err != nil || d <= 0 {
		return 60 * time.Second
	}
	return d
}

// Image 取生效镜像（默认 alpine:3）。
func (m Manifest) Image() string {
	if m.Run.Image == "" {
		return "alpine:3"
	}
	return m.Run.Image
}

func appsDir(vault string) string  { return filepath.Join(filepath.Dir(vault), "apps") }
func stateDir(vault string) string { return filepath.Join(filepath.Dir(vault), "data", "apps") }

func statePath(vault, name string) string { return filepath.Join(stateDir(vault), name+".json") }

// Install 把一个目录装进来：校验清单 → 算哈希 → 复制到 apps/<name>/<hash> → 记 pending。
// 同名重复装**不覆盖**旧版本（旧版本可能正被审批过、跑过，替换等于绕过审批）。
func Install(vault, src string) (Installed, error) {
	m, err := LoadManifest(src)
	if err != nil {
		return Installed{}, err
	}
	sum, err := HashDir(src)
	if err != nil {
		return Installed{}, err
	}
	dst := filepath.Join(appsDir(vault), m.Name, sum)
	if _, err := os.Stat(dst); err == nil {
		return Installed{}, fmt.Errorf("%s@%s 已经装过了（同一个 hash）", m.Name, sum[:12])
	}
	if err := copyDir(src, dst); err != nil {
		return Installed{}, err
	}
	st := Installed{
		Name: m.Name, Version: m.Version, SHA256: sum, Dir: dst,
		State: "pending", InstalledAt: time.Now().Format(time.RFC3339),
	}
	return st, saveState(vault, st)
}

// Approve 审批一个已安装的应用（记录是谁、何时）。
func Approve(vault, name, by string) (Installed, error) {
	st, err := Load(vault, name)
	if err != nil {
		return st, err
	}
	if st.State == "revoked" {
		return st, fmt.Errorf("%s 已被撤销，要重新装一个新版本再审批", name)
	}
	// 审批时再算一次：审批的是那一个内容，不是目录名。
	sum, err := HashDir(st.Dir)
	if err != nil {
		return st, err
	}
	if sum != st.SHA256 {
		return st, fmt.Errorf("内容与安装时不一致（%s != %s）—— 审批前先重新安装", sum[:12], st.SHA256[:12])
	}
	st.State = "approved"
	st.ApprovedBy = by
	st.ApprovedAt = time.Now().Format(time.RFC3339)
	return st, saveState(vault, st)
}

// Revoke 撤销：立刻生效（下一次 run 就拒）。
func Revoke(vault, name string) (Installed, error) {
	st, err := Load(vault, name)
	if err != nil {
		return st, err
	}
	st.State = "revoked"
	return st, saveState(vault, st)
}

// Load 读安装记录。
func Load(vault, name string) (Installed, error) {
	var st Installed
	b, err := os.ReadFile(statePath(vault, name))
	if err != nil {
		return st, fmt.Errorf("没装过这个应用：%s", name)
	}
	return st, json.Unmarshal(b, &st)
}

// List 列出所有安装记录。
func List(vault string) ([]Installed, error) {
	ents, err := os.ReadDir(stateDir(vault))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Installed
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var st Installed
		b, err := os.ReadFile(filepath.Join(stateDir(vault), e.Name()))
		if err != nil {
			continue
		}
		if json.Unmarshal(b, &st) == nil {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func saveState(vault string, st Installed) error {
	if err := os.MkdirAll(stateDir(vault), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(vault, st.Name) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(vault, st.Name))
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		return os.WriteFile(target, b, mode)
	})
}

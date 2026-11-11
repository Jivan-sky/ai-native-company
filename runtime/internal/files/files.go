// Package files —— 本地文件目录数据源：列目录、读文件（像 ls + cat）。
//
// 与数据库不同，文件源没有 SQL，所以三条安全约束必须做死：
//
//  1. **不许穿越**：`../`、绝对路径、`..` 编码，一律拒绝；
//  2. **不许借符号链接跑出去**：解析真实路径后必须仍在根目录下；
//  3. **不许无限读**：单文件字节上限、目录条目上限；超了就说「截断」，不静默吞。
//
// 只读：本包没有任何写文件的代码路径。
package files

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const Schema = "anc.files/v1"

// Source 是一个文件目录数据源。
type Source struct {
	Schema     string `json:"schema"`
	Name       string `json:"name"`
	Title      string `json:"title,omitempty"`
	Type       string `json:"type"` // file
	Root       string `json:"root"` // 客户自己指定的目录
	MaxBytes   int    `json:"max_bytes,omitempty"`
	MaxEntries int    `json:"max_entries,omitempty"`
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
	if s.Type != "file" {
		return s, fmt.Errorf("%s：type=%q（只支持 file）", name, s.Type)
	}
	if s.Name != name || strings.TrimSpace(s.Root) == "" {
		return s, fmt.Errorf("%s：name 要与文件名一致，root 不能为空", name)
	}
	root, err := filepath.Abs(s.Root)
	if err != nil {
		return s, err
	}
	if _, err := os.Stat(root); err != nil {
		return s, fmt.Errorf("%s：根目录读不到：%v", name, err)
	}
	s.Root = root
	if s.MaxBytes <= 0 {
		s.MaxBytes = 1 << 20 // 1 MiB
	}
	if s.MaxEntries <= 0 {
		s.MaxEntries = 500
	}
	return s, nil
}

// Entry 是目录里的一项。
type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"` // 相对根
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime string `json:"mtime"`
	Link  bool   `json:"symlink,omitempty"` // 符号链接一律标出来，不跟随
}

// List 列一个目录（不跟随符号链接）。
func List(s Source, rel string) ([]Entry, bool, error) {
	dir, err := safeJoin(s.Root, rel)
	if err != nil {
		return nil, false, err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, false, fmt.Errorf("目录不存在：%s", rel)
	}
	if !st.IsDir() {
		return nil, false, fmt.Errorf("不是目录：%s", rel)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, false, err
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	out := make([]Entry, 0, len(ents))
	truncated := false
	for _, e := range ents {
		if len(out) >= s.MaxEntries {
			truncated = true
			break
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		link := info.Mode()&os.ModeSymlink != 0
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // 隐藏文件不列：.git 那些不是「知识」
		}
		out = append(out, Entry{
			Name: name, Path: filepath.ToSlash(filepath.Join(rel, name)),
			Dir: e.IsDir(), Size: info.Size(), MTime: info.ModTime().Format("2006-01-02 15:04"), Link: link,
		})
	}
	return out, truncated, nil
}

// Read 读一个文件（带上限）。第二个返回值表示是否被截断。
func Read(s Source, rel string) ([]byte, bool, error) {
	p, err := safeJoin(s.Root, rel)
	if err != nil {
		return nil, false, err
	}
	st, err := os.Lstat(p)
	if err != nil {
		return nil, false, fmt.Errorf("文件不存在：%s", rel)
	}
	if st.IsDir() {
		return nil, false, fmt.Errorf("这是一个目录：%s", rel)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("符号链接不读：%s", rel)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	buf := make([]byte, s.MaxBytes)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, false, err
	}
	truncated := false
	if st.Size() > int64(n) {
		truncated = true
	}
	return buf[:n], truncated, nil
}

// safeJoin 把相对路径接到根上，并挡住一切逃逸。
func safeJoin(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("路径必须是相对根目录的")
	}
	clean := filepath.Clean("/" + rel) // 先钉在 / 上，消掉 ..
	full := filepath.Join(root, clean)
	// 解析真实路径（处理符号链接），再确认仍在根下
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("路径不存在：%s", rel)
		}
		return "", err
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if real != rootReal && !strings.HasPrefix(real, rootReal+string(os.PathSeparator)) {
		return "", fmt.Errorf("越界：%s 不在根目录下", rel)
	}
	return real, nil
}

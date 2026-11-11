package files

// 文件目录的快照与漂移：与数据库目录同一条口径（只看结构，不搬内容）。
//
// 指纹由「路径 + 大小 + 内容哈希（有上限）」组成：
//   - 只看大小会漏掉「改了同一长度的内容」；
//   - 全量哈希在大目录上很贵，所以只对 <= 256 KiB 的文件算内容哈希，更大的只记大小。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const SnapshotSchema = "anc.filecatalog/v1"

type Snapshot struct {
	Schema      string      `json:"schema"`
	DataSource  string      `json:"datasource"`
	Root        string      `json:"root"`
	ScannedAt   time.Time   `json:"scanned_at"`
	Fingerprint string      `json:"fingerprint"`
	Files       []FileEntry `json:"files"`
	Dirs        []string    `json:"dirs"`
}

type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"` // 只对小文件算
	MTime  string `json:"mtime"`
}

const hashLimit = 256 << 10

// Scan 扫描整棵树（不跟随符号链接）。
func Scan(s Source) (Snapshot, error) {
	snap := Snapshot{Schema: SnapshotSchema, DataSource: s.Name, Root: s.Root, ScannedAt: time.Now().UTC()}
	rootReal, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		return snap, err
	}
	err = filepath.WalkDir(rootReal, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 读不了的项跳过（不因为一个坏文件毁掉整次扫描）
		}
		if p == rootReal {
			return nil
		}
		rel, err := filepath.Rel(rootReal, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			snap.Dirs = append(snap.Dirs, rel)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil // 软链不跟随、不入快照
		}
		fe := FileEntry{Path: rel, Size: info.Size(), MTime: info.ModTime().UTC().Format(time.RFC3339)}
		if info.Size() <= hashLimit {
			if sum, err := hashFile(p); err == nil {
				fe.SHA256 = sum
			}
		}
		snap.Files = append(snap.Files, fe)
		return nil
	})
	if err != nil {
		return snap, err
	}
	sort.Slice(snap.Files, func(i, j int) bool { return snap.Files[i].Path < snap.Files[j].Path })
	sort.Strings(snap.Dirs)
	snap.Fingerprint = fileFingerprint(snap)
	return snap, nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, hashLimit)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func fileFingerprint(s Snapshot) string {
	h := sha256.New()
	for _, d := range s.Dirs {
		fmt.Fprintf(h, "D:%s\n", d)
	}
	for _, f := range s.Files {
		fmt.Fprintf(h, "F:%s:%d:%s\n", f.Path, f.Size, f.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Save/Load 走控制面目录（不进客户目录）。
func SaveSnapshot(vault string, s Snapshot) error {
	dir := filepath.Join(filepath.Dir(vault), "catalog")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, s.DataSource+".files.json.tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, s.DataSource+".files.json"))
}

func LoadSnapshot(vault, name string) (Snapshot, error) {
	var s Snapshot
	b, err := os.ReadFile(filepath.Join(filepath.Dir(vault), "catalog", name+".files.json"))
	if err != nil {
		return s, fmt.Errorf("还没有这个文件目录的快照：%s", name)
	}
	return s, json.Unmarshal(b, &s)
}

// FileChange 是一条漂移。
type FileChange struct {
	Kind   string `json:"kind"` // added | removed | changed
	Path   string `json:"path"`
	Detail string `json:"detail,omitempty"`
}

func DiffSnapshots(old, cur Snapshot) []FileChange {
	oldM := map[string]FileEntry{}
	for _, f := range old.Files {
		oldM[f.Path] = f
	}
	curM := map[string]FileEntry{}
	for _, f := range cur.Files {
		curM[f.Path] = f
	}
	var out []FileChange
	for p, c := range curM {
		o, ok := oldM[p]
		if !ok {
			out = append(out, FileChange{Kind: "added", Path: p})
			continue
		}
		if o.Size != c.Size || (o.SHA256 != "" && c.SHA256 != "" && o.SHA256 != c.SHA256) {
			out = append(out, FileChange{Kind: "changed", Path: p,
				Detail: fmt.Sprintf("%d → %d 字节", o.Size, c.Size)})
		}
	}
	for p := range oldM {
		if _, ok := curM[p]; !ok {
			out = append(out, FileChange{Kind: "removed", Path: p})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

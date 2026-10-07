package board

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"anc/internal/org"
)

// AssetsSchema 是这一份视图的版本号（同 /api/dataflow 的理由）。
const AssetsSchema = "anc.assets/v1"

// maxScan 是每个数据目录最多数多少个文件 —— 防一个塞了几十万文件的目录把看板拖死。
// 到顶就翻 truncated 旗，如实说「只数到这里」，不假装数完了。
const maxScan = 4000

// recentKeep 是每个目录回显几个「最近动的文件」。
const recentKeep = 5

// AssetsView 是 `/api/assets` 的形状 —— 「数据目录里现在有什么」。
//
// 先说清它**不是**什么：这不是「沉淀」。沉淀层（条目化的产出、来源、还能不能复用）见
// 议题 #26 / #27，还没定。这一页只回答「原料现在有哪些、多久没动了」——
// 「有目录 / 有文件」离「有资产」差着一整层，所以标题就叫原料。
//
// 只出**相对路径**：看板是观测面，不该泄漏本机布局。
type AssetsView struct {
	Schema   string      `json:"schema"`
	Wired    bool        `json:"wired"`
	Dirs     []AssetsDir `json:"dirs"`
	Charters int         `json:"charters"` // 立项书副本目录数（真源在客户侧，这里是副本）
	Note     string      `json:"note"`
	Error    string      `json:"error,omitempty"`
}

// AssetsDir 是真相源里那张「数据路由」表的一行 + 这一行现在底下有多少东西。
type AssetsDir struct {
	Dir       string       `json:"dir"`
	Summary   string       `json:"summary"`
	Files     int          `json:"files"`
	Bytes     int64        `json:"bytes"`
	Newest    string       `json:"newest,omitempty"`
	Recent    []AssetsFile `json:"recent"`
	Truncated bool         `json:"truncated"`
}

type AssetsFile struct {
	Path     string `json:"path"` // 相对数据目录
	Bytes    int64  `json:"bytes"`
	Modified string `json:"modified"`
}

// handleAssets 数一遍每个数据目录。目录不存在不是错（刚 init 的库就是空的），
// 照回 0 —— 看板要能显示「还没开始沉淀」这个真实状态。
func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	view := AssetsView{
		Schema: AssetsSchema, Dirs: []AssetsDir{},
		Note: "这是**原料**清单，不是沉淀：目录里有文件 ≠ 有可复用的产出。" +
			"沉淀层（条目化、来源、可复用性）见议题 #26 / #27，未定。",
	}
	o, err := org.Load(s.Vault)
	if err != nil {
		view.Error = "真相源读不动，算不出数据目录（不猜）：" + s.scrub(firstLine(err.Error()))
		writeJSON(w, http.StatusOK, view)
		return
	}
	view.Wired = true
	for _, rt := range o.Routing {
		view.Dirs = append(view.Dirs, scanDir(filepath.Join(s.Vault, rt.Dir), rt.Dir, rt.Summary, maxScan))
	}
	view.Charters = countSubdirs(filepath.Join(s.Vault, "charters"))
	writeJSON(w, http.StatusOK, view)
}

// scanDir 数一个数据目录：文件数 / 体积 / 最近变动 / 最近几个文件。
// 不跟符号链接（跟出去就出了 vault）；单条读不动就跳过，不让整页塌掉。
func scanDir(root, name, summary string, limit int) AssetsDir {
	d := AssetsDir{Dir: name, Summary: summary, Recent: []AssetsFile{}}
	var all []AssetsFile
	_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || e.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, ierr := e.Info()
		if ierr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		d.Files++
		d.Bytes += info.Size()
		all = append(all, AssetsFile{
			Path:     filepath.ToSlash(rel),
			Bytes:    info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339),
		})
		if d.Files >= limit {
			d.Truncated = true
			return fs.SkipAll
		}
		return nil
	})
	// 时间倒序；同一秒的按路径排 —— 同一份现场两次刷新要逐字节相同。
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Modified != all[j].Modified {
			return all[i].Modified > all[j].Modified
		}
		return all[i].Path < all[j].Path
	})
	if n := min(recentKeep, len(all)); n > 0 {
		d.Recent = all[:n]
		d.Newest = all[0].Modified
	}
	return d
}

func countSubdirs(root string) int {
	ents, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if e.IsDir() {
			n++
		}
	}
	return n
}

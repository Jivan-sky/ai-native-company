package board

// /api/ref —— 让 agent **真的能打开 refs 指向的材料**。
//
// 15' 实测里 agent 说了句很实在的话：「refs 有，但我打不开；所以我把 r15 往前排的唯一理由
// 是提案人说'供应商已比价'」—— 那是信话，不是核账。
//
// 这一层只做"把 vault 里那个文件读出来"，不做解释、不做摘要：
// 路径限制在 vault 内（防穿越、防软链逃逸），大小有上限，行范围可选。

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"anc/internal/files"
)

const refMaxBytes = 64 * 1024

// RefView 是一次读取的结果。
type RefView struct {
	Schema    string `json:"schema"`
	Path      string `json:"path"`
	From      int    `json:"from,omitempty"` // 起始行（1 基）
	To        int    `json:"to,omitempty"`
	Lines     int    `json:"lines"`
	Total     int    `json:"total_lines"`
	Truncated bool   `json:"truncated,omitempty"`
	Content   string `json:"content,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (s *Server) handleRefRead(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw := strings.TrimSpace(q.Get("path"))
	view := RefView{Schema: "anc.ref/v1"}

	// 支持 `path.md#12` 与 `path.md#12-20` 两种锚点
	path := raw
	if i := strings.Index(raw, "#"); i >= 0 {
		path = strings.TrimSpace(raw[:i])
		view.From, view.To = parseLineRange(raw[i+1:])
	}
	view.Path = path
	if path == "" {
		view.Error = "缺 path —— 给 vault 内的相对路径，例如 domains/trade/contract.md"
		writeJSON(w, http.StatusBadRequest, view)
		return
	}

	// 复用 files.Read 的同一套规则（防穿越、不读软链、有上限），只是把 root 换成 vault
	src := files.Source{Root: s.Vault, MaxBytes: refMaxBytes}
	data, truncated, err := files.Read(src, path)
	if err != nil {
		view.Error = "读不了：" + s.scrub(firstLine(err.Error()))
		writeJSON(w, http.StatusOK, view)
		return
	}
	view.Truncated = truncated
	// 结尾那个换行不代表多一行 —— 不然"文件 4 行"会被算成 5 行
	text := strings.TrimSuffix(string(data), "\n")
	lines := strings.Split(text, "\n")
	view.Total = len(lines)
	if view.From > 0 {
		from := view.From
		if from > len(lines) {
			from = len(lines)
		}
		to := view.To
		if to <= 0 || to > len(lines) {
			to = len(lines)
		}
		view.Lines = to - from + 1
		view.Content = strings.Join(lines[from-1:to], "\n")
		if view.To == 0 {
			view.To = to
		}
	} else {
		view.Lines = len(lines)
		view.Content = text
	}
	// 只允许读 vault 内的文本；二进制给一句人话，不倒字节
	if strings.ContainsRune(view.Content, 0) {
		view.Content = ""
		view.Error = "这不是文本文件 —— 拒绝把二进制倒给调用方（" + filepath.Base(path) + "）"
	}
	writeJSON(w, http.StatusOK, view)
}

func parseLineRange(s string) (int, int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0
	}
	if i := strings.IndexAny(s, "-~"); i > 0 {
		a, _ := strconv.Atoi(strings.TrimSpace(s[:i]))
		b, _ := strconv.Atoi(strings.TrimSpace(s[i+1:]))
		return a, b
	}
	n, _ := strconv.Atoi(s)
	return n, 0
}

// checkRefs 逐条检查 refs 能不能打开（去掉 #锚点后按 vault 相对路径 stat）。
//
// 为什么在**读**这一侧查、不在入队时落盘：文件可能晚一点才创建，
// 落盘会把"当时的缺失"冻结成永久结论。读时查，永远反映此刻。
func (s *Server) checkRefs(refs []string) (ok int, missing []string) {
	src := files.Source{Root: s.Vault, MaxBytes: refMaxBytes}
	for _, r := range refs {
		path := strings.TrimSpace(r)
		if i := strings.Index(path, "#"); i >= 0 {
			path = strings.TrimSpace(path[:i])
		}
		if path == "" {
			continue
		}
		if _, _, err := files.Read(src, path); err != nil {
			missing = append(missing, r)
			continue
		}
		ok++
	}
	return ok, missing
}

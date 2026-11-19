package exp

// 固化：把**已确认**的经验写成 skills/<name>/SKILL.md。
//
// 为什么放在 deploy 里而不是单独一步：deploy 的语义就是"这条经验从现在起生效"，
// 而"生效"的物理含义就是 agent 下次装配时能读到它。分成两步会出现
// "账本说已部署、skills/ 里却没有"的假状态。
//
// 三条纪律：
//  1 只有 approved 才能 deploy（判据 7，已在 Deploy 里守）
//  2 覆盖前先把旧版归档到 skills/<name>/.history/v<N>.md（可回滚，判据 8）
//  3 正文里带来源：from[]、approved_by、version、digest —— 读的人能一路回溯

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const skillsDir = "skills"

func skillPath(vault, name string) string {
	return filepath.Join(vault, skillsDir, name, "SKILL.md")
}

func historyDir(vault, name string) string {
	return filepath.Join(vault, skillsDir, name, ".history")
}

// writeSkill 把经验固化成 SKILL.md，返回这次写入的内容指纹。
func writeSkill(vault string, e Experience) (string, error) {
	dst := skillPath(vault, e.ID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	// 覆盖前归档旧版：可回滚的前提是"旧字节还在"
	if old, err := os.ReadFile(dst); err == nil {
		hd := historyDir(vault, e.ID)
		if err := os.MkdirAll(hd, 0o755); err != nil {
			return "", err
		}
		// 归档名用**当前版本号**（还没 +1 的那一版）
		ver := e.Version
		if ver <= 0 {
			ver = 1
		}
		if err := os.WriteFile(filepath.Join(hd, "v"+strconv.Itoa(ver)+".md"), old, 0o644); err != nil {
			return "", err
		}
	}
	body := renderSkill(e)
	if err := os.WriteFile(dst, []byte(body), 0o644); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:]), nil
}

func renderSkill(e Experience) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", e.ID)
	fmt.Fprintf(&b, "description: %s\n", firstLine(e.Claim))
	fmt.Fprintf(&b, "scope: %s\n", e.Scope)
	fmt.Fprintf(&b, "version: %d\n", e.Version)
	fmt.Fprintf(&b, "approved_by: %s\n", e.ApprovedBy)
	fmt.Fprintf(&b, "deployed_at: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "from: %s\n", strings.Join(e.From, ", "))
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n%s\n", e.Title, e.Claim)
	if e.Note != "" {
		fmt.Fprintf(&b, "\n## 备注\n\n%s\n", e.Note)
	}
	b.WriteString("\n## 出处\n\n")
	for _, f := range e.From {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	return b.String()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n"); i > 0 {
		s = s[:i]
	}
	if len([]rune(s)) > 120 {
		s = string([]rune(s)[:120]) + "…"
	}
	return s
}

// Rollback 把 skills/<name>/SKILL.md 恢复到上一个归档版本。
//
// 语义：版本号继续往前走（账本不倒退），但**内容**回到上一版。
// 这样"回滚过"本身也是可查的事实，而不是把历史抹掉。
func Rollback(vault, id, by string) (Experience, error) {
	e, err := Get(vault, id)
	if err != nil {
		return Experience{}, err
	}
	if e.Status != StatusDeployed {
		return Experience{}, fmt.Errorf("%w：当前 %s —— 只有已部署的才能回滚", ErrBadState, e.Status)
	}
	hd := historyDir(vault, id)
	entries, err := os.ReadDir(hd)
	if err != nil || len(entries) == 0 {
		return Experience{}, fmt.Errorf("没有可回滚的旧版（skills/%s/.history 为空）—— 第一次部署之前没有旧字节", id)
	}
	// 取版本号最大的那一份
	best, bestVer := "", 0
	for _, en := range entries {
		n := strings.TrimSuffix(strings.TrimPrefix(en.Name(), "v"), ".md")
		if v, err := strconv.Atoi(n); err == nil && v > bestVer {
			bestVer, best = v, en.Name()
		}
	}
	if best == "" {
		return Experience{}, fmt.Errorf("归档文件名读不懂 —— 期望 v<N>.md")
	}
	old, err := os.ReadFile(filepath.Join(hd, best))
	if err != nil {
		return Experience{}, err
	}
	dst := skillPath(vault, id)
	if err := os.WriteFile(dst, old, 0o644); err != nil {
		return Experience{}, err
	}
	sum := sha256.Sum256(old)
	from := e.Status
	e.Version++
	e.At = time.Now().Format(time.RFC3339)
	if err := save(vault, e); err != nil {
		return Experience{}, err
	}
	return e, appendLedger(vault, Entry{At: e.At, Action: "rollback", ID: id, By: by,
		Why: "恢复到 v" + strconv.Itoa(bestVer), FromStatus: from, ToStatus: e.Status,
		Version: e.Version, Digest: hex.EncodeToString(sum[:])})
}

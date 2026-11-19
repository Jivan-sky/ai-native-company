package exp

// 回流：**拉取 + 征询**，不做强制推送。
//
// 为什么不做推送（用户 2026-11-19 定的）：
//   - 推送等于替每个 agent 做决定。技能会改行为，替别人改行为是越权。
//   - 强行注入还会让人分不清"这是公司让我用的"还是"这是我自己选的"。
//
// 所以这一层只做两件事：
//  1 让 agent **自己查到**有哪些技能、哪些是新的（check）
//  2 把"要不要用"变成一句**明说要问人**的话（由 agent 去问，人点头才采纳）
//
// 采纳（adopt）本身会改真相源（members/*.md 的 skills 列），那是人的动作，不在这一层做。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CatalogItem 是一个可被发现的技能。
type CatalogItem struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scope       string   `json:"scope"`
	Version     int      `json:"version"`
	Digest      string   `json:"digest"`
	ApprovedBy  string   `json:"approved_by"`
	From        []string `json:"from,omitempty"`
	Path        string   `json:"path"`
	DeployedAt  string   `json:"deployed_at,omitempty"`
}

const seenDir = ".seen"

// Catalog 列出全部**已部署**的技能（只读）。
func Catalog(vault string) ([]CatalogItem, error) {
	all, err := List(vault)
	if err != nil {
		return nil, err
	}
	var out []CatalogItem
	for _, e := range all {
		if e.Status != StatusDeployed {
			continue
		}
		body, err := os.ReadFile(skillPath(vault, e.ID))
		if err != nil {
			continue // 账本说部署了、盘上没有：由 check 报出来，不在目录里假装有
		}
		sum := sha256.Sum256(body)
		out = append(out, CatalogItem{
			Name: e.ID, Description: firstLine(e.Claim), Scope: e.Scope, Version: e.Version,
			Digest: hex.EncodeToString(sum[:]), ApprovedBy: e.ApprovedBy, From: e.From,
			Path: skillPath(vault, e.ID), DeployedAt: e.At,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// NewSince 返回这个主体**上次看之后**新增或有变化的技能。
//
// 为什么按人记"看过哪一版"：没有这个状态，每次 check 都会把全部技能重报一遍，
// 人就学会忽略它 —— 那等于没有回流。
func NewSince(vault, who string) ([]CatalogItem, error) {
	all, err := Catalog(vault)
	if err != nil {
		return nil, err
	}
	seen, _ := loadSeen(vault, who) // 读不到 = 没看过，全部算新
	var out []CatalogItem
	for _, it := range all {
		if seen[it.Name] != it.Digest {
			out = append(out, it)
		}
	}
	return out, nil
}

// MarkSeen 记下"这个主体看过这些技能了"。由 agent 在 check 之后自己调。
func MarkSeen(vault, who string, items []CatalogItem) error {
	if strings.TrimSpace(who) == "" {
		return nil
	}
	seen, _ := loadSeen(vault, who)
	for _, it := range items {
		seen[it.Name] = it.Digest
	}
	d := filepath.Join(vault, skillsDir, seenDir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(map[string]any{"who": who, "at": time.Now().Format(time.RFC3339), "seen": seen}, "", "  ")
	return os.WriteFile(filepath.Join(d, seenName(who)+".json"), append(b, '\n'), 0o644)
}

func seenName(who string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(who) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('_')
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func loadSeen(vault, who string) (map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(vault, skillsDir, seenDir, seenName(who)+".json"))
	if err != nil {
		return map[string]string{}, err
	}
	var doc struct {
		Seen map[string]string `json:"seen"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || doc.Seen == nil {
		return map[string]string{}, err
	}
	return doc.Seen, nil
}

// MissingSkillFiles 查出"账本说已部署、skills/ 里却没有"的条目。
// 这一类假状态必须能被看见 —— 否则 agent 读不到，人还以为已经生效。
func MissingSkillFiles(vault string) ([]string, error) {
	all, err := List(vault)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range all {
		if e.Status != StatusDeployed {
			continue
		}
		if _, err := os.Stat(skillPath(vault, e.ID)); err != nil {
			out = append(out, e.ID)
		}
	}
	sort.Strings(out)
	return out, nil
}

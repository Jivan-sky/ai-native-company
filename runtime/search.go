package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"anc/internal/audit"
	"anc/internal/org"
	"anc/internal/retrieval"
)

// maxDocReplyBytes 是 MCP 取正文的上限：再大的文档塞进 harness 上下文只会把真正的问题挤掉。
// 截断**明说**（truncated / total_bytes），不静默给半篇冒充全文。
const maxDocReplyBytes = 64 << 10

// ---------- 知识检索（D13：接口抽象 + 默认 BM25 + 权限以 org 为准） ----------
//
// 这里只做三件事：解「这次能看哪些域」、调检索包、如实回话。索引怎么建、怎么打分
// 在 internal/retrieval；谁能看哪块在 org（这里不另造一套 ACL）。

const searchUsage = `anc search —— 按域可见范围搜 vault 里的知识

用法：
  anc search <vault 目录> --q "要查什么" [选项]
  anc search <vault 目录> --get <vault 相对路径> [选项]

选项：
  --q <词>         要查的问题 / 关键词（--get 时可省）
  --get <路径>     取一篇正文（只认索引里且可见的路径）
  --who <编号>     以谁的身份搜：成员名 / 业务 agent 的 slug；不写 = 本机操作者（全部域）
  --domain <slug>  只问这一块业务（domains.md 的 slug）
  --roots <目录>   这次索引哪些 vault 相对目录，逗号分隔；不写 = 10-knowledge / knowledge + 各域 data
  --backend <名>   检索后端：bm25（默认，零依赖）；其他名字明确报错，不静默回落
  --limit <n>      返回几条（默认 8，上限 50）
  --json           机器读的出口

口径：
  - 默认件是本地 BM25：索引每次从 markdown 重建，**不是真相源**，删掉不影响答案。
  - 权限只认 org：成员看自己 domains；管理员或 company.md 的 access.cross_domain=visible
    看全部；业务 agent 只按 agents.md 的 domain 看。看不见的文档不参与打分、不计数。
  - 公司级资料（frontmatter 没写 domain、或显式写空）谁都能看 —— 同看板首页。
  - 检索给的是**指针 + 片段**，不是正文结论；查不到就如实说没入库，不按记忆补。
`

type searchOut struct {
	Vault     string          `json:"vault"`
	Query     string          `json:"query,omitempty"`
	Who       string          `json:"who,omitempty"`
	Scope     []string        `json:"scope"` // ["*"] = 全部域
	ScopeNote string          `json:"scope_note,omitempty"`
	Backend   string          `json:"backend,omitempty"`
	Indexed   int             `json:"indexed"`
	Visible   int             `json:"visible"`
	Count     int             `json:"count"`
	Hits      []retrieval.Hit `json:"hits"`
	Doc       *searchDoc      `json:"doc,omitempty"`
	Warnings  []string        `json:"warnings,omitempty"`
	Note      string          `json:"note,omitempty"`
	Refused   *searchRefusal  `json:"refused,omitempty"`
	At        string          `json:"at"`
}

// searchDoc 是 --get 的回话形状。不直接序列化 retrieval.Doc：正文这一格要显式命名，
// 免得以后给 Doc 加内部字段时悄悄漏进回话。
type searchDoc struct {
	Path    string `json:"path"`
	Domain  string `json:"domain,omitempty"`
	Title   string `json:"title,omitempty"`
	Body    string `json:"body"`
	Updated string `json:"updated,omitempty"`
}

type searchRefusal = retrieval.Refusal

func cmdSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	q := fs.String("q", "", "要查什么")
	get := fs.String("get", "", "取一篇正文（vault 相对路径）")
	who := fs.String("who", "", "以谁的身份搜")
	domain := fs.String("domain", "", "只问这一块业务")
	roots := fs.String("roots", "", "这次索引哪些 vault 相对目录，逗号分隔")
	backend := fs.String("backend", envOr("ANC_SEARCH_BACKEND", "bm25"), "检索后端（默认 bm25）")
	limit := fs.Int("limit", retrieval.DefaultLimit, "返回几条")
	asJSON := fs.Bool("json", false, "机器读的出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 {
		fmt.Fprint(os.Stderr, searchUsage)
		return 2
	}
	if strings.TrimSpace(*q) == "" && strings.TrimSpace(*get) == "" {
		fmt.Fprint(os.Stderr, "错误：--q 与 --get 至少给一个\n\n")
		fmt.Fprint(os.Stderr, searchUsage)
		return 2
	}
	abs, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 2
	}
	o, err := org.Load(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n（先修 org：anc org check <vault>）\n", err)
		return 1
	}

	out := searchOut{
		Vault: abs, Query: strings.TrimSpace(*q), Who: strings.TrimSpace(*who),
		At: time.Now().Format(time.RFC3339),
	}
	sc, refuse := retrieval.ScopeFor(o, out.Who)
	if refuse != nil {
		out.Refused = refuse
		return printSearch(out, *asJSON, true)
	}
	out.Scope = retrieval.ScopeLabels(sc)

	if d := strings.TrimSpace(*domain); d != "" {
		var narrowed *retrieval.Refusal
		sc, narrowed = retrieval.NarrowToDomain(o, sc, d)
		if narrowed != nil {
			out.Refused = narrowed
			return printSearch(out, *asJSON, true)
		}
		// 「只问这一块业务」也包含公司级资料 —— 与 Viewer.CanSeeDomain("") 同一口径。
		out.Scope = []string{d}
		out.ScopeNote = "含公司级资料"
	}

	cfg := retrieval.Config{Backend: *backend}
	if r := strings.TrimSpace(*roots); r != "" {
		cfg.Roots = org.SplitList(r)
	}
	ix, warns, err := retrieval.Open(abs, o, cfg)
	out.Warnings = warns
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：索引建不起来：%v\n", err)
		return 1
	}
	out.Backend = ix.Name()
	out.Indexed, out.Visible = ix.Len(), ix.Visible(sc)

	if p := strings.TrimSpace(*get); p != "" {
		d, found, err := ix.Get(p, sc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		if !found {
			out.Refused = &searchRefusal{Reason: "取不到这篇：要么不在索引面里，要么不在你的可见范围"}
			return printSearch(out, *asJSON, true)
		}
		out.Doc = &searchDoc{
			Path: d.Path, Domain: d.Domain, Title: d.Title, Body: d.Body,
			Updated: d.ModTime.UTC().Format(time.RFC3339),
		}
		return printSearch(out, *asJSON, false)
	}

	hits, err := ix.Search(out.Query, sc, *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	out.Hits = hits
	out.Count = len(out.Hits)
	if out.Count == 0 {
		out.Note = "没有命中 —— 可能是还没入库；不要按记忆补（查不到就说查不到）"
	}
	return printSearch(out, *asJSON, false)
}

func printSearch(out searchOut, asJSON bool, refused bool) int {
	if asJSON {
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		if refused {
			return 1
		}
		return 0
	}
	for _, w := range out.Warnings {
		fmt.Fprintf(os.Stderr, "注意：%s\n", w)
	}
	if out.Refused != nil {
		fmt.Printf("anc search —— 拒绝\n")
		fmt.Printf("  为什么  %s\n", out.Refused.Reason)
		if out.Refused.Ask != "" {
			fmt.Printf("  找谁    %s\n", out.Refused.Ask)
		}
		return 1
	}
	fmt.Printf("anc search —— 按域可见范围搜知识\n")
	fmt.Printf("  vault    %s\n", out.Vault)
	if out.Who != "" {
		scope := strings.Join(out.Scope, "、")
		if out.ScopeNote != "" {
			scope += "（" + out.ScopeNote + "）"
		}
		fmt.Printf("  谁       %s（可见：%s）\n", out.Who, scope)
	} else {
		fmt.Printf("  谁       本机操作者（全部域）\n")
	}
	fmt.Printf("  后端     %s\n", out.Backend)
	fmt.Printf("  索引     %d 篇（可见 %d 篇）\n", out.Indexed, out.Visible)
	if out.Doc != nil {
		fmt.Printf("  文档     %s  [%s]\n", out.Doc.Path, dashOr(out.Doc.Domain))
		fmt.Printf("  标题     %s\n", dashOr(out.Doc.Title))
		fmt.Printf("  正文     \n%s\n", out.Doc.Body)
		return 0
	}
	fmt.Printf("  问       %s\n", out.Query)
	fmt.Printf("  命中     %d 条\n", out.Count)
	for i, h := range out.Hits {
		fmt.Printf("  %d) %s  [%s]  score %.4f  %s\n", i+1, h.Path, dashOr(h.Domain), h.Score, h.Updated)
		if h.Title != "" {
			fmt.Printf("     %s\n", h.Title)
		}
		fmt.Printf("     %s\n", h.Snippet)
	}
	if out.Note != "" {
		fmt.Printf("  说明     %s\n", out.Note)
	}
	return 0
}

func dashOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// ---------- MCP 工具：anc_search ----------

// searchKnowledge 是 anc_search 的实现：与 CLI 同一套口径（resolveSearchScope / retrieval.Load），
// 每次调用留一条痕。看不见的域不是「零结果」，是**拒**并指路。
// srcRef 是「这份整理稿的原件」的可核验状态。
//
// 状态名按 A7 的提案：unlinked / missing / available / unavailable。
// 这一版**不提供「打开原件字节」的能力** —— 原件权限模型还没有，
// 所以 available 只表示「登记在案、在原位、能算出版本」，不表示「你有权读它」。
type srcRef struct {
	Status  string `json:"status"`
	Ref     string `json:"ref,omitempty"`     // vault 相对路径；vault 之外一律不报
	Version string `json:"version,omitempty"` // 原件内容摘要前 12 位
	Note    string `json:"note,omitempty"`
}

func (g *envelopeIngress) sourceRefOf(raw string) srcRef {
	s := strings.TrimSpace(raw)
	if s == "" {
		return srcRef{Status: "unlinked", Note: "整理稿没有登记 source_file —— 无法指回原件"}
	}
	p := s
	if !filepath.IsAbs(p) {
		p = filepath.Join(g.Vault, filepath.FromSlash(p))
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return srcRef{Status: "unavailable"}
	}
	vabs, err := filepath.Abs(g.Vault)
	if err != nil {
		return srcRef{Status: "unavailable"}
	}
	rel, err := filepath.Rel(vabs, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		// 原件登记在 vault 之外：不暴露本机路径（看板与 MCP 都只出相对指针）。
		return srcRef{Status: "unavailable", Note: "原件登记在 vault 之外 —— 这里不报本机路径"}
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return srcRef{Status: "missing", Note: "有来源登记，但指定原件不可得 —— 请维护人核实"}
		}
		return srcRef{Status: "unavailable", Note: err.Error()}
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return srcRef{Status: "unavailable", Note: err.Error()}
	}
	return srcRef{Status: "available", Ref: filepath.ToSlash(rel), Version: hash12(b)}
}

func (g *envelopeIngress) searchKnowledge(args map[string]any) (string, bool) {
	now := g.now()
	who := strArg(args, "who")
	q := strings.TrimSpace(strArg(args, "q"))
	ask := strings.TrimSpace(strArg(args, "domain"))
	limit := intArg(args, "limit", retrieval.DefaultLimit)

	o, err := org.Load(g.Vault)
	if err != nil {
		g.auditTool("anc_search", readActor(who), ask, audit.ResultFailed, "真相源加载失败", now)
		return "真相源加载失败，搜不了：" + err.Error() + "\n（先修 org：anc org check <vault>）", true
	}
	sc, refuse := retrieval.ScopeFor(o, who)
	if refuse != nil {
		g.auditTool("anc_search", readActor(who), ask, audit.ResultDenied, refuse.Reason, now)
		return mustJSON(map[string]any{"refused": refuse, "at": now.Format(time.RFC3339)}), true
	}
	if q == "" {
		g.auditTool("anc_search", who, ask, audit.ResultFailed, "q 为空", now)
		return "q 为空 —— 要查什么？", true
	}
	if ask != "" {
		var narrowed *retrieval.Refusal
		sc, narrowed = retrieval.NarrowToDomain(o, sc, ask)
		if narrowed != nil {
			g.auditTool("anc_search", who, ask, audit.ResultDenied, narrowed.Reason, now)
			return mustJSON(map[string]any{
				"refused": narrowed,
				"at":      now.Format(time.RFC3339),
			}), true
		}
	}

	ix, warns, err := retrieval.Open(g.Vault, o, retrieval.Config{
		Backend: os.Getenv("ANC_SEARCH_BACKEND"),
		Roots:   org.SplitList(os.Getenv("ANC_SEARCH_ROOTS")),
	})
	if err != nil {
		g.auditTool("anc_search", who, ask, audit.ResultFailed, "索引建不起来", now)
		return "索引建不起来：" + err.Error(), true
	}
	object := ask
	if object == "" {
		object = "knowledge"
	}
	hits, err := ix.Search(q, sc, limit)
	if err != nil {
		g.auditTool("anc_search", who, object, audit.ResultFailed, "检索失败", now)
		return "检索失败：" + err.Error(), true
	}
	// 同 A5：痕没写上就不释放结果。检索结果同样是「被授权才能看」的数据。
	if err := g.auditTool("anc_search", who, object, audit.ResultOK, "", now); err != nil {
		return "没有返回检索结果：审计暂时写不进去（" + err.Error() + "）。\n修好后重搜 —— 重搜会重新判权。", true
	}

	out := map[string]any{
		"who": who, "scope": retrieval.ScopeLabels(sc), "backend": ix.Name(),
		"indexed": ix.Len(), "visible": ix.Visible(sc),
		"query": q, "count": len(hits), "hits": hits, "at": now.Format(time.RFC3339),
	}
	if len(hits) == 0 {
		out["note"] = "没有命中 —— 可能是还没入库；不要按记忆补（查不到就说查不到）"
	}
	if len(warns) > 0 {
		out["warnings"] = warns
	}
	return mustJSON(out), false
}

// getDocument 是 anc_get 的实现：按 anc_search 给的 path 取正文。
// path 只认索引里、且可见的（目录穿越没有入口）；看不见 = 拒，并说清为什么。
func (g *envelopeIngress) getDocument(args map[string]any) (string, bool) {
	now := g.now()
	who := strArg(args, "who")
	path := strings.TrimSpace(strArg(args, "path"))

	o, err := org.Load(g.Vault)
	if err != nil {
		g.auditTool("anc_get", readActor(who), path, audit.ResultFailed, "真相源加载失败", now)
		return "真相源加载失败，取不了：" + err.Error() + "\n（先修 org：anc org check <vault>）", true
	}
	sc, refuse := retrieval.ScopeFor(o, who)
	if refuse != nil {
		g.auditTool("anc_get", readActor(who), path, audit.ResultDenied, refuse.Reason, now)
		return mustJSON(map[string]any{"refused": refuse, "at": now.Format(time.RFC3339)}), true
	}
	if path == "" {
		g.auditTool("anc_get", who, path, audit.ResultFailed, "path 为空", now)
		return "path 为空 —— 要取哪一篇？（用 anc_search 回话里的 path）", true
	}
	ix, warns, err := retrieval.Open(g.Vault, o, retrieval.Config{
		Backend: os.Getenv("ANC_SEARCH_BACKEND"),
		Roots:   org.SplitList(os.Getenv("ANC_SEARCH_ROOTS")),
	})
	if err != nil {
		g.auditTool("anc_get", who, path, audit.ResultFailed, "索引建不起来", now)
		return "索引建不起来：" + err.Error(), true
	}
	d, found, err := ix.Get(path, sc)
	if err != nil {
		g.auditTool("anc_get", who, path, audit.ResultFailed, "取正文失败", now)
		return "取正文失败：" + err.Error(), true
	}
	if !found {
		reason := "取不到这篇：要么不在索引面里，要么不在你的可见范围"
		g.auditTool("anc_get", who, path, audit.ResultDenied, reason, now)
		return mustJSON(map[string]any{
			"refused": map[string]any{"reason": reason},
			"at":      now.Format(time.RFC3339),
		}), true
	}
	g.auditTool("anc_get", who, path, audit.ResultOK, "", now)
	body, truncated := clipUTF8(d.Body, maxDocReplyBytes)
	out := map[string]any{
		"path": d.Path, "title": d.Title, "domain": d.Domain, "body": body,
		"total_bytes": len(d.Body), "truncated": truncated, "at": now.Format(time.RFC3339),
		// A7：正文之外还要能指回**签发原件**，并且给出「这是哪一版整理稿」。
		// 读者据此判断「我看到的这一段对应哪份原件」，而不是拿到一个整理稿路径就完事。
		"document_version": d.Version,
		"source":           g.sourceRefOf(d.SourceFile),
	}
	if len(warns) > 0 {
		out["warnings"] = warns
	}
	return mustJSON(out), false
}

// clipUTF8 按字节上限截断，但**不切半个字**（中文一个字 3 字节；切一半会变成非法 UTF-8）。
func clipUTF8(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// hash12 与 retrieval 里那份同一个口径：够区分版本，又不把响应当日志用。
func hash12(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

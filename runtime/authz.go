package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/org"
)

const authzUsage = `anc authz —— 权威源与授权的操作面（C1/C2）

用法：
  anc authz init <vault> --issuer member:<名> [--note <一句话>]
                                建权威源 v1。第一笔根授权只能由它签发
  anc authz grant <vault> --slug <slug> --to <谁> --action read|write|invoke
                                 --object domain:<slug> [--from <谁>] [--by member:<签发人>]
                                 [--expires <RFC3339>] [--parent <上游 slug>] [--reason <一句话>]
                                签一条授权（写 grants/<slug>.md；同名不覆盖）
  anc authz revoke <vault> <slug|grants/<slug>.md> [--by member:<签发人>]
                                撤一条授权：追加撤权记录并把权威版本 +1（撤权从这一版起生效）
  anc authz check <vault>       逐条判「现在还能不能用」：OK / 拒绝（原因）/ 无法判定（原因）
`

func cmdAuthz(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, authzUsage)
		return 2
	}
	switch args[0] {
	case "init":
		return authzInit(args[1:])
	case "grant":
		return authzGrant(args[1:])
	case "revoke":
		return authzRevoke(args[1:])
	case "check":
		return authzCheck(args[1:])
	}
	fmt.Fprintf(os.Stderr, "错误：不认识子命令 %q\n\n", args[0])
	fmt.Fprint(os.Stderr, authzUsage)
	return 2
}

func authzVault(pos []string) (string, int) {
	if len(pos) > 1 {
		fmt.Fprint(os.Stderr, authzUsage)
		return "", 2
	}
	v := "."
	if len(pos) == 1 {
		v = pos[0]
	}
	abs, err := filepath.Abs(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return "", 2
	}
	return abs, 0
}

func authzInit(args []string) int {
	fs := flag.NewFlagSet("authz init", flag.ContinueOnError)
	issuer := fs.String("issuer", "", "受托签发人（member:<名>）")
	note := fs.String("note", "", "这份委任从哪来（一句话）")
	fa, pos := splitArgs(args, map[string]bool{})
	if err := fs.Parse(fa); err != nil {
		return 2
	}
	vault, code := authzVault(pos)
	if code != 0 {
		return code
	}
	a, err := org.InitAuthority(vault, *issuer, *note)
	if err != nil {
		fmt.Fprintf(os.Stderr, "建不起来：%v\n", err)
		return 1
	}
	printJSON(map[string]any{"ok": true, "version": a.Version, "issuer": *issuer,
		"path": filepath.Join(vault, org.AuthorityFile)})
	return 0
}

func authzGrant(args []string) int {
	fs := flag.NewFlagSet("authz grant", flag.ContinueOnError)
	slug := fs.String("slug", "", "授权标识（小写 ascii，当文件名）")
	from := fs.String("from", "", "谁给的（默认 = 权威源的签发人）")
	to := fs.String("to", "", "给谁")
	action := fs.String("action", "read", "read | write | invoke")
	object := fs.String("object", "", "客体（domain:<slug>）")
	by := fs.String("by", "", "签发人（默认 = 权威源的 issuer）")
	expires := fs.String("expires", "", "到期时刻（RFC3339）；空 = 不设自身期限")
	parent := fs.String("parent", "", "上游授权 slug（转授时写）")
	reason := fs.String("reason", "", "一句话：为什么给")
	fa, pos := splitArgs(args, map[string]bool{})
	if err := fs.Parse(fa); err != nil {
		return 2
	}
	vault, code := authzVault(pos)
	if code != 0 {
		return code
	}
	auth, ok, err := org.LoadAuthority(vault)
	if err != nil || !ok {
		fmt.Fprintf(os.Stderr, "先建权威源：anc authz init %s --issuer member:<名>\n", vault)
		return 1
	}
	issuedBy := firstNonEmpty(*by, auth.Issuer)
	g := org.Grant{
		Slug: *slug, From: firstNonEmpty(*from, issuedBy), To: *to, Action: *action,
		Object: *object, IssuedBy: issuedBy, ExpiresAt: *expires, Parent: *parent,
		Reason: *reason, IssuedAt: time.Now().Format(time.RFC3339),
	}
	path, err := org.AppendGrant(vault, g)
	if err != nil {
		fmt.Fprintf(os.Stderr, "签不成：%v\n", err)
		return 1
	}
	printJSON(map[string]any{"ok": true, "path": path, "issued_by": issuedBy})
	return 0
}

func authzRevoke(args []string) int {
	fs := flag.NewFlagSet("authz revoke", flag.ContinueOnError)
	by := fs.String("by", "", "谁撤的（只能是对受托签发人）")
	fa, pos := splitArgs(args, map[string]bool{})
	if err := fs.Parse(fa); err != nil {
		return 2
	}
	if len(pos) != 2 {
		fmt.Fprint(os.Stderr, authzUsage)
		return 2
	}
	vault, err := filepath.Abs(pos[0])
	if err != nil {
		return 2
	}
	who := *by
	if strings.TrimSpace(who) == "" {
		if a, ok, _ := org.LoadAuthority(vault); ok {
			who = a.Issuer
		}
	}
	a, err := org.Revoke(vault, pos[1], who)
	if err != nil {
		fmt.Fprintf(os.Stderr, "撤不成：%v\n", err)
		return 1
	}
	printJSON(map[string]any{"ok": true, "version": a.Version, "revoked": a.Revoked})
	return 0
}

// authzCheck 逐条判「现在还能不能用」。只读。
//
// 输出三态而不是两态：判不出来（没权威源 / 缺 issued_by / 链断）必须与"明确拒绝"分开，
// 否则运维会把"我不知道"当成"她不配"。
func authzCheck(args []string) int {
	fa, pos := splitArgs(args, map[string]bool{})
	_ = fa
	vault, code := authzVault(pos)
	if code != 0 {
		return code
	}
	o, err := org.Load(vault)
	if err != nil {
		fmt.Fprintf(os.Stderr, "真相源加载失败：%v\n", err)
		return 1
	}
	auth, wired, aerr := org.LoadAuthority(vault)
	if aerr != nil {
		fmt.Fprintf(os.Stderr, "权威源读不动：%v\n", aerr)
		return 1
	}
	type row struct {
		Slug    string   `json:"slug"`
		OK      bool     `json:"ok"`
		Unknown bool     `json:"unknown"`
		Reason  string   `json:"reason,omitempty"`
		Chain   []string `json:"chain,omitempty"`
		Version int      `json:"version"`
	}
	rows := []row{}
	bad := 0
	for _, g := range o.Grants {
		v := o.Exercise(g, auth, wired, time.Now())
		rows = append(rows, row{Slug: g.Slug, OK: v.OK, Unknown: v.Unknown,
			Reason: v.Reason, Chain: v.Chain, Version: v.Version})
		if !v.OK {
			bad++
		}
	}
	b, _ := json.MarshalIndent(map[string]any{
		"ok": bad == 0, "wired": wired, "authority_version": auth.Version,
		"issuer": auth.Issuer, "grants": rows, "not_ok": bad,
	}, "", "  ")
	fmt.Println(string(b))
	if bad > 0 {
		return 1
	}
	return 0
}

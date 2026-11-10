package main

// anc delegate —— 发/撤委派令牌。
//
// 令牌是**签名过的数据**（EdDSA JWS），不是数据库里的一行：拿着它就能离线验。
// 私钥住 `<vault>/../data/authz/ed25519.key`（600），公钥给看板/服务用。
//
// 三条口径：
//   - 令牌只在签发那一刻打印一次（stdout），别写进日志/仓库；
//   - 没有 `jti` 不许签（撤不了）；寿命至少 60 秒；
//   - 撤销是**追加**一条 jti → 到期时间，过了到期时间自动清掉。

import (
	"crypto/ed25519"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/authz"
)

const delegateUsage = `anc delegate —— 发 / 撤委派令牌

用法：
  anc delegate issue  <vault> --app <名> [--aud chat] [--scope a,b] [--budget 5] [--ttl 10m]
  anc delegate revoke <vault> --jti <jti> [--exp <RFC3339>]

选项：
  --keys <目录>   密钥目录（默认 <vault>/../data/authz）
  --kid <名>      header 里的密钥 id（默认 k1）

口径：
  · 令牌打印一次；它等于一张有时限的通行证，别落到日志里；
  · 没有 jti 不签（撤不了）；寿命下限 60 秒。
`

func cmdDelegate(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, delegateUsage)
		return 2
	}
	switch args[0] {
	case "issue":
		return cmdDelegateIssue(args[1:])
	case "revoke":
		return cmdDelegateRevoke(args[1:])
	}
	fmt.Fprint(os.Stderr, delegateUsage)
	return 2
}

func authzDir(vault, dir string) string {
	if strings.TrimSpace(dir) != "" {
		return dir
	}
	abs, err := filepath.Abs(vault)
	if err != nil {
		abs = vault
	}
	return filepath.Join(filepath.Dir(abs), "data", "authz")
}

func cmdDelegateIssue(args []string) int {
	fs := flag.NewFlagSet("delegate issue", flag.ContinueOnError)
	app := fs.String("app", "", "应用 id（必填）")
	aud := fs.String("aud", "chat", "目标出口")
	scope := fs.String("scope", "", "允许的注册名，逗号分隔（必填）")
	budget := fs.Float64("budget", 0, "金额上限（美元）")
	ttl := fs.Duration("ttl", 10*time.Minute, "有效期")
	keysDir := fs.String("keys", "", "密钥目录")
	kid := fs.String("kid", "k1", "密钥 id")
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 || strings.TrimSpace(*app) == "" || strings.TrimSpace(*scope) == "" || *budget <= 0 {
		fmt.Fprint(os.Stderr, delegateUsage)
		return 2
	}
	vault := posArgs[0]
	keys := authz.Keys{Dir: authzDir(vault, *keysDir)}
	_, privRaw, err := keys.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：密钥读不出来：%v\n", err)
		return 1
	}
	now := time.Now()
	jti := fmt.Sprintf("%s-%d", *app, now.UnixNano())
	tok, err := authz.Sign(ed25519.PrivateKey(privRaw), *kid, authz.Claims{
		Sub: *app, Aud: *aud, Scope: splitCSV(*scope), BudgetUSD: *budget,
		IAT: now.Unix(), Exp: now.Add(*ttl).Unix(), JTI: jti,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	fmt.Printf("✅ 已签发\n   app %s · aud %s · scope %s · 预算 %.2f · 到 %s\n",
		*app, *aud, *scope, *budget, now.Add(*ttl).Format(time.RFC3339))
	fmt.Printf("   jti %s\n\n%s\n", jti, tok)
	return 0
}

func cmdDelegateRevoke(args []string) int {
	fs := flag.NewFlagSet("delegate revoke", flag.ContinueOnError)
	jti := fs.String("jti", "", "要撤销的令牌 id")
	exp := fs.String("exp", "", "原令牌到期时间（RFC3339；不写就按 24h 后清）")
	keysDir := fs.String("keys", "", "密钥目录")
	flagArgs, posArgs := splitArgs(args, map[string]bool{})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 || strings.TrimSpace(*jti) == "" {
		fmt.Fprint(os.Stderr, delegateUsage)
		return 2
	}
	dir := authzDir(posArgs[0], *keysDir)
	until := time.Now().Add(24 * time.Hour)
	if strings.TrimSpace(*exp) != "" {
		if t, err := time.Parse(time.RFC3339, *exp); err == nil {
			until = t
		} else {
			fmt.Fprintf(os.Stderr, "错误：--exp 不是 RFC3339：%v\n", err)
			return 2
		}
	}
	if err := (authz.Revocations{Path: filepath.Join(dir, "revoked.json")}).Revoke(*jti, until); err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	fmt.Printf("✅ 已撤销 %s（保留到 %s）\n", *jti, until.Format(time.RFC3339))
	return 0
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

package main

// anc audit config —— 装机凭据体检（只读，不改任何文件）。
//
// 为什么要有它：`anc org check` 看的是**真相源**对不对，`anc doctor` 看的是**这台机器能不能装**，
// 而「装完之后凭据有没有摊在明面上」两处都不管。今天（2026-11-09）刚因为脱敏那条（#50）收过口，
// 这类问题不会自己报警 —— 它只会安静地留在盘上。
//
// 判据只有一条：**一个拿到这台机器文件系统的人，还需不需要猜**。
// 要猜 = 通过；不用猜（能直接读到密钥 / 秘钥文件在 git 里 / 权限对所有人可读）= 报出来。
//
// 为什么只读、不做 --fix：改权限 / 搬文件都是**有影响的动作**（可能让正在跑的 daemon 读不到凭据），
// 得由人按现场判断。这一版只把事实摆出来，每条都带一句能照抄的修法。

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"anc/internal/audit"
)

const auditConfigUsage = `anc audit config —— 装机凭据体检（只读；不改任何文件）

用法：
  anc audit config <vault> [--home <目录>] [--json]

选项：
  --home <目录>   ~/.anc 在哪（默认当前用户的 home；测试与多用户机器上用）
  --json          出 JSON

查七项：
  1) ~/.anc 目录权限         期望 0700（组 / 其他人一个权限位都不给）
  2) ~/.anc/secrets.env      期望 0600
  3) ~/.anc/host.json        期望 0600
  4) gateway/config.toml     期望 0600
  5) 产物里的明文            用进程级脱敏表扫 gateway config：扫得出东西 = 那里躺着密钥
  6) 真相源里的秘密文件      vault 里不该有 secrets.env / .env / *.pem / *.key / *.p12 / id_rsa*
  7) 越权模式                bypassPermissions 只许 devbot 用（其余角色要人拍）

退出码：0 = 没有需要处理的项（不存在的东西按「跳过」算，不算失败）；1 = 有 ⚠️。
`

// secItem 是一项体检结果。Skip 与 OK 分开：**「没有这个东西」不是「检查通过」**，
// 两者混在一起，读的人会把「还没装」看成「装得很好」。
type secItem struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Skip   bool   `json:"skip,omitempty"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func cmdAuditConfig(args []string) int {
	fs := flag.NewFlagSet("audit config", flag.ContinueOnError)
	home := fs.String("home", "", "~/.anc 所在的家目录（默认当前用户）")
	asJSON := fs.Bool("json", false, "出 JSON")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(posArgs) != 1 {
		fmt.Fprint(os.Stderr, auditConfigUsage)
		return 2
	}
	vault := posArgs[0]
	if *home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：取不到 home 目录（可用 --home 指定）：%v\n", err)
			return 1
		}
		*home = h
	}

	items := auditConfigItems(vault, *home)
	if *asJSON {
		b, err := json.MarshalIndent(struct {
			Vault string    `json:"vault"`
			Home  string    `json:"home"`
			Items []secItem `json:"items"`
		}{vault, *home, items}, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			return 1
		}
		fmt.Println(string(b))
	} else {
		printAuditConfig(vault, *home, items)
	}
	for _, it := range items {
		if !it.OK && !it.Skip {
			return 1
		}
	}
	return 0
}

// auditConfigItems 是体检的全部判据。纯函数（只读盘、不打印、不退出），单测直接喂 temp 目录。
func auditConfigItems(vault, home string) []secItem {
	var out []secItem

	// 1-4：权限。Windows 上 POSIX 权限位没有意义，一律跳过（不假装查过）。
	if runtime.GOOS == "windows" {
		out = append(out, secItem{Name: "文件权限", Skip: true, OK: true,
			Detail: "Windows 上不用 POSIX 权限位判（ACL 是另一套）—— 这一项本次不适用"})
	} else {
		ancDir := filepath.Join(home, ".anc")
		out = append(out, permItem("~/.anc 目录", ancDir, 0o700))
		out = append(out, permItem("secrets.env", filepath.Join(ancDir, "secrets.env"), 0o600))
		out = append(out, permItem("host.json", filepath.Join(ancDir, "host.json"), 0o600))
		// gateway config 与 vault 是兄弟目录（D5 / 议题 #24 的落点），不在 vault 里。
		out = append(out, permItem("gateway config",
			filepath.Join(filepath.Dir(vault), "gateway", "config.toml"), 0o600))
	}

	// 5：产物里的明文。扫的是 gateway config —— 那份文件里每个 bot 都带着平台参数，
	// 是「凭据可能被摊开」的第一现场。扫不动（文件不在 / 读不了）就跳过，不编结论。
	out = append(out, plaintextItem(filepath.Join(filepath.Dir(vault), "gateway", "config.toml")))

	// 6：真相源里不该出现秘密文件。
	if files, err := secretFilesIn(vault); err != nil {
		out = append(out, secItem{Name: "真相源里的秘密文件", Detail: "扫不动：" + err.Error()})
	} else if len(files) == 0 {
		out = append(out, secItem{Name: "真相源里的秘密文件", OK: true, Detail: "没有"})
	} else {
		out = append(out, secItem{Name: "真相源里的秘密文件",
			Detail: strings.Join(files, " · "),
			Fix:    "凭据不进真相源：挪到 ~/.anc/secrets.env（真相源是 git 里的东西，会被同步、被复制、被备份）"})
	}

	// 7：越权模式。org 规则已经会把「非 devbot 用 bypass」报红，这里再对着**文件**查一遍 ——
	// 防的是「字段写歪了、规则没认出来」那种漏（比如大小写、缩进、写在正文里）。
	if roles, err := bypassRoles(vault); err != nil {
		out = append(out, secItem{Name: "越权模式", Detail: "扫不动：" + err.Error()})
	} else if len(roles) == 0 {
		out = append(out, secItem{Name: "越权模式", OK: true, Detail: "只有 devbot（或没人）用 bypassPermissions"})
	} else {
		out = append(out, secItem{Name: "越权模式",
			Detail: "这些角色写了 bypassPermissions：" + strings.Join(roles, " · "),
			Fix:    "SPEC §6-5：只有 devbot 可以用 bypassPermissions；其余角色改回 dontAsk 并在 allowed_tools 里显式列工具"})
	}
	return out
}

// permItem 判一个路径的权限位。不存在 = 跳过（还没装过，不是错）。
func permItem(name, path string, want os.FileMode) secItem {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return secItem{Name: name, OK: true, Skip: true, Detail: "不存在（还没装过）—— 跳过"}
		}
		return secItem{Name: name, Detail: err.Error()}
	}
	got := st.Mode().Perm()
	if got&^want == 0 && got != 0 {
		return secItem{Name: name, OK: true, Detail: fmt.Sprintf("0%o", got)}
	}
	return secItem{Name: name,
		Detail: fmt.Sprintf("0%o（期望 0%o —— 组 / 其他人不该有权限）", got, want),
		Fix:    "chmod " + fmt.Sprintf("%o", want) + " " + path}
}

// plaintextItem 拿进程级脱敏表扫一遍产物：扫得出东西，说明那里躺着**像密钥**的内容。
// 扫的是「脱敏之后会不会变」——与审计落盘同一张表、同一把尺子（议题 #50）。
func plaintextItem(path string) secItem {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return secItem{Name: "产物里的明文", OK: true, Skip: true, Detail: "gateway config 还没生成（还没 render）—— 跳过"}
		}
		return secItem{Name: "产物里的明文", Detail: err.Error()}
	}
	_, hits := audit.Effective().Apply(string(b))
	if len(hits) == 0 {
		return secItem{Name: "产物里的明文", OK: true, Detail: "没扫出密钥形态"}
	}
	return secItem{Name: "产物里的明文",
		Detail: fmt.Sprintf("扫出 %d 处像密钥的内容（按脱敏规则 %s）", len(hits), strings.Join(hits, " · ")),
		Fix:    "凭据由凭据桥在拉起时读进进程环境，不进 config；先按上面那些位置清一遍，再 `anc render --apply`"}
}

// secretName 判一个文件名是不是「装秘密的那种」。宁可多报也不漏：
// 这条的代价只是多看一行，放过一次的代价是密钥进了 git。
func secretName(base string) bool {
	b := strings.ToLower(base)
	switch b {
	case "secrets.env", ".env", ".env.local", "id_rsa", "id_ed25519":
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx"} {
		if strings.HasSuffix(b, ext) {
			return true
		}
	}
	return strings.HasPrefix(b, "id_rsa") || strings.HasPrefix(b, "id_ed25519")
}

// secretFilesIn 扫真相源（跳过 .git）。返回 vault 相对路径，排序（同输入同输出）。
func secretFilesIn(vault string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(vault, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 读不动的目录跳过：体检不该因为一个坏权限的目录整条失败
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if secretName(d.Name()) {
			if rel, err := filepath.Rel(vault, p); err == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// bypassRoles 扫 roles/*/persona.md 的 mode 行。返回**不是 devbot** 却写了 bypass 的角色。
func bypassRoles(vault string) ([]string, error) {
	dir := filepath.Join(vault, "roles")
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() || e.Name() == "devbot" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name(), "persona.md"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "mode:") && strings.Contains(strings.ToLower(t), "bypass") {
				out = append(out, "roles/"+e.Name())
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func printAuditConfig(vault, home string, items []secItem) {
	fmt.Println("anc audit config —— 装机凭据体检（只读；不改任何文件）")
	fmt.Printf("  vault    %s\n", vault)
	fmt.Printf("  home     %s\n\n", home)
	bad := 0
	for _, it := range items {
		mark := "✅"
		switch {
		case it.Skip:
			mark = "…"
		case !it.OK:
			mark = "⚠️"
			bad++
		}
		fmt.Printf("  %s %s %s\n", mark, padRight(it.Name, 22), it.Detail)
		if it.Fix != "" && !it.OK {
			fmt.Printf("       FIX: %s\n", it.Fix)
		}
	}
	fmt.Println()
	if bad == 0 {
		fmt.Println("结论：没有需要处理的项。")
		return
	}
	fmt.Printf("结论：%d 项需要处理（只报不改 —— 改权限 / 搬文件都可能让在跑的 daemon 读不到凭据，得人按现场判断）。\n", bad)
}

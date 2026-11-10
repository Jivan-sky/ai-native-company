package main

// anc models —— 看模型注册表与应用绑定（**密钥只以打码形式出现**）。

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"anc/internal/apply"
	"anc/internal/models"
)

const modelsUsage = `anc models —— 模型注册表 + 应用绑定（只读；密钥只有打码形态）

用法：
  anc models ls    <vault> [--registry <文件>] [--apps <目录>] [--secrets <文件>] [--json]
  anc models check <vault> [--registry <文件>] [--apps <目录>] [--secrets <文件>]

默认落点（与部署产物同层，不进真相源）：
  registry  <vault>/../registry.json
  apps      <vault>/../apps/*.json
  secrets   ~/.anc/secrets.env

口径：
  · 注册表里 secret 只写**名字**；真值住 secrets（600），本命令只输出 ****尾巴四位；
  · 应用只按**注册名**绑定（models 里写 id，不写 key、不写 base_url）；
  · egress 必须显式：这块业务的数据能不能出门，是一条策略，不许默认全通。
`

func cmdModels(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, modelsUsage)
		return 2
	}
	switch args[0] {
	case "ls":
		return cmdModelsLs(args[1:])
	case "check":
		return cmdModelsCheck(args[1:])
	}
	fmt.Fprint(os.Stderr, modelsUsage)
	return 2
}

type modelsPaths struct{ vault, registry, apps, secrets string }

func parseModelsArgs(name string, args []string, asJSON *bool) (modelsPaths, int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	reg := fs.String("registry", "", "注册表文件（默认 <vault>/../registry.json）")
	apps := fs.String("apps", "", "应用绑定目录（默认 <vault>/../apps）")
	sec := fs.String("secrets", "", "凭据文件（默认 ~/.anc/secrets.env）")
	jsonFlag := fs.Bool("json", false, "JSON 出口")
	flagArgs, posArgs := splitArgs(args, map[string]bool{"json": true})
	if err := fs.Parse(flagArgs); err != nil {
		return modelsPaths{}, 2
	}
	if len(posArgs) != 1 {
		fmt.Fprint(os.Stderr, modelsUsage)
		return modelsPaths{}, 2
	}
	abs, err := filepath.Abs(posArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return modelsPaths{}, 2
	}
	parent := filepath.Dir(abs)
	p := modelsPaths{vault: abs, registry: *reg, apps: *apps, secrets: *sec}
	if p.registry == "" {
		p.registry = filepath.Join(parent, "registry.json")
	}
	if p.apps == "" {
		p.apps = filepath.Join(parent, "apps")
	}
	if p.secrets == "" {
		home, _ := os.UserHomeDir()
		p.secrets = filepath.Join(home, ".anc", "secrets.env")
	}
	*asJSON = *jsonFlag
	return p, 0
}

func loadSecretValues(path string) map[string]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	return apply.ParseSecrets(path, string(b)).Values
}

func loadBindings(dir string, reg *models.Registry) ([]*models.Binding, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*models.Binding
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := models.LoadBinding(filepath.Join(dir, e.Name()), reg)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].App < out[j].App })
	return out, nil
}

func cmdModelsLs(args []string) int {
	var asJSON bool
	p, code := parseModelsArgs("models ls", args, &asJSON)
	if code != 0 {
		return code
	}
	reg, err := models.LoadRegistry(p.registry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	bindings, err := loadBindings(p.apps, reg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	values := loadSecretValues(p.secrets)

	type row struct {
		ID       string   `json:"id"`
		Provider string   `json:"provider"`
		Model    string   `json:"model"`
		Secret   string   `json:"secret"`
		Masked   string   `json:"masked"`
		Status   string   `json:"status"`
		Apps     []string `json:"apps"`
	}
	var rows []row
	for _, e := range reg.Entries {
		status := "启用"
		if !e.Enabled() {
			status = "停用"
		}
		var apps []string
		for _, b := range bindings {
			for _, id := range b.Models {
				if id == e.ID {
					apps = append(apps, b.App)
				}
			}
		}
		rows = append(rows, row{e.ID, e.Provider, e.Model, e.Secret,
			models.Mask(values[e.Secret]), status, apps})
	}
	if asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	fmt.Printf("anc models —— 注册表 %s\n\n", p.registry)
	fmt.Printf("  %-18s %-10s %-14s %-24s %-12s %s\n", "注册名", "提供方", "模型", "密钥（打码）", "状态", "被谁绑定")
	for _, r := range rows {
		apps := strings.Join(r.Apps, "、")
		if apps == "" {
			apps = "—"
		}
		fmt.Printf("  %-18s %-10s %-14s %-24s %-12s %s\n",
			r.ID, r.Provider, r.Model, r.Secret+" "+r.Masked, r.Status, apps)
	}
	if len(rows) == 0 {
		fmt.Println("  （注册表是空的）")
	}
	fmt.Printf("\n  应用 %d 个 · 密钥值只以 **** 形态出现\n", len(bindings))
	return 0
}

func cmdModelsCheck(args []string) int {
	var asJSON bool
	p, code := parseModelsArgs("models check", args, &asJSON)
	if code != 0 {
		return code
	}
	reg, err := models.LoadRegistry(p.registry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：注册表 %v\n", err)
		return 1
	}
	bindings, err := loadBindings(p.apps, reg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：绑定 %v\n", err)
		return 1
	}
	values := loadSecretValues(p.secrets)
	missing := 0
	for _, e := range reg.Entries {
		if strings.TrimSpace(values[e.Secret]) == "" {
			fmt.Printf("  ⚠️  %s：密钥 %s 还没配（secrets 里没有）\n", e.ID, e.Secret)
			missing++
		}
	}
	fmt.Printf("✅ 注册表 %d 条 · 绑定 %d 个 · 未配密钥 %d 条\n", len(reg.Entries), len(bindings), missing)
	if missing > 0 {
		return 1
	}
	return 0
}

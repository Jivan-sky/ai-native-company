package org

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setCompanyKey 往 company.md 的 frontmatter 末尾插一个键（测试用）。
func setCompanyKey(t *testing.T, vault, key, val string) {
	t.Helper()
	path := filepath.Join(vault, "company", "company.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	i := strings.Index(text, "\n---")
	if i < 0 {
		t.Fatalf("%s 里找不到 frontmatter 结束符", path)
	}
	if err := os.WriteFile(path, []byte(text[:i]+"\n"+key+": "+val+text[i:]), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 这个值直接进 cc-connect 的 [display].mode，上游是**大小写敏感的精确匹配**；
// 写错它拒绝启动 = 全部 bot 下线，所以是红档，不许静默放行。
func TestDisplayInvalidIsFatal(t *testing.T) {
	for _, bad := range []string{"normal", "silent", "FULL"} {
		v := copyVault(t, "one")
		setCompanyKey(t, v, "display", bad)
		_, err := Load(v)
		assertRule(t, err, "company.display.invalid")
	}
}

// 合法值照常加载；不写 = 出厂默认 quiet（聊天窗只出结果）。
func TestDisplayValidLoads(t *testing.T) {
	for _, ok := range []string{DisplayQuiet, DisplayCompact, DisplayFull} {
		v := copyVault(t, "one")
		setCompanyKey(t, v, "display", ok)
		o, err := Load(v)
		if err != nil {
			t.Fatalf("display=%s 应当加载通过：%v", ok, err)
		}
		if o.Company.Display != ok || o.Company.DisplayMode() != ok {
			t.Fatalf("display=%s 读回 %q / 生效 %q", ok, o.Company.Display, o.Company.DisplayMode())
		}
	}
	o, err := Load(copyVault(t, "one"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Company.DisplayMode() != DisplayQuiet {
		t.Fatalf("没写 display 时生效档位应 %s，实际 %s", DisplayQuiet, o.Company.DisplayMode())
	}
}

// 呈现档改了必须让指纹变 —— 否则 `anc render` 会以为「没变」而不重渲染，
// 现场就还是老档位（这次全量生成的判据全靠这个指纹）。
func TestDisplayEntersInputsHash(t *testing.T) {
	host := Host{VaultRoot: "/vault", HomesRoot: "/homes", DataDir: "/data"}
	quiet, err := Load(copyVault(t, "one"))
	if err != nil {
		t.Fatal(err)
	}
	full := *quiet
	full.Company.Display = DisplayFull
	if quiet.InputsHash(host) == full.InputsHash(host) {
		t.Fatal("display 没进指纹：改档位不会触发重渲染")
	}
}

package apply

import (
	"strings"
	"testing"
)

func TestParseSecrets(t *testing.T) {
	f := ParseSecrets("secrets.env", "# 注释\n\nANC_A=1\nANC_B=abc=def\nANC_A=2\n坏行\n")
	if f.Values["ANC_B"] != "abc=def" {
		t.Errorf("ANC_B = %q，想要 abc=def（只按第一个 = 切）", f.Values["ANC_B"])
	}
	if f.Values["ANC_A"] != "2" {
		t.Errorf("ANC_A = %q，想要 2（后者覆盖前者）", f.Values["ANC_A"])
	}
	if got, want := strings.Join(f.Keys, ","), "ANC_A,ANC_B"; got != want {
		t.Errorf("键顺序 = %q，想要 %q", got, want)
	}
	if len(f.Bad) != 1 {
		t.Fatalf("坏行 %d 条", len(f.Bad))
	}
}

const upstreamPS1 = `$ErrorActionPreference = 'Stop'
$env:CC_LOG_FILE = 'C:\Users\x\.cc-connect\logs\cc-connect.log'
Set-Location -LiteralPath 'D:\gw'
while ($true) {
  & 'C:\Users\x\.anc\bin\cc-connect.exe'
  $exitCode = $LASTEXITCODE
  if ($exitCode -eq 0) { exit 0 }
  Start-Sleep -Seconds 10
}
`

func TestInjectLoaderWrapsUpstreamScript(t *testing.T) {
	block := LoaderBlock(`C:\Users\x\.anc\secrets.env`, "0.1.0")
	out, action := InjectLoader(upstreamPS1, block)
	if action != ActionAdded {
		t.Fatalf("首次注入 = %q，想要 %q", action, ActionAdded)
	}
	if !strings.HasPrefix(out, LoaderBegin) {
		t.Error("托管区没插在最前面（它要在 gateway 被拉起之前跑完）")
	}
	if !strings.Contains(out, "Start-Sleep -Seconds 10") {
		t.Error("上游脚本被改坏了：尾部丢了")
	}
	l := InspectLoader(out)
	if !l.Found || !l.Intact() {
		t.Fatalf("回读不一致：%+v", l)
	}
	if l.Secrets != `C:\Users\x\.anc\secrets.env` {
		t.Errorf("回读到的凭据路径 = %q", l.Secrets)
	}
	// 幂等：同样的输入重跑，不该产生 diff。
	again, action2 := InjectLoader(out, block)
	if action2 != ActionUnchanged {
		t.Errorf("重跑 = %q，想要 %q", action2, ActionUnchanged)
	}
	if again != out {
		t.Error("重跑内容变了（重跑 apply 不该产生 diff）")
	}
}

// 键名两边带空格是粘贴常见的样子，要按 shell 的直觉吃掉空格。
func TestParseSecretsTrimsKeyAndValue(t *testing.T) {
	f := ParseSecrets("secrets.env", "  ANC_X  =  v  \n")
	if f.Values["ANC_X"] != "v" {
		t.Errorf("键/值没 trim：%#v", f.Values)
	}
}

func TestMissingAndBlank(t *testing.T) {
	f := ParseSecrets("secrets.env", "ANC_A=1\nANC_B=\n")
	refs := []string{"ANC_A", "ANC_B", "ANC_C", "ANC_C"}
	miss := Missing(refs, f)
	if got, want := strings.Join(miss, ","), "ANC_C"; got != want {
		t.Errorf("缺键 = %q，想要 %q（去重）", got, want)
	}
	if got, want := strings.Join(f.Blank(), ","), "ANC_B"; got != want {
		t.Errorf("空值键 = %q，想要 %q", got, want)
	}
	if !f.Has("ANC_B") || f.Has("ANC_C") {
		t.Error("Has 判断错")
	}
}

func TestInjectLoaderReplacesExistingBlock(t *testing.T) {
	out, _ := InjectLoader(upstreamPS1, LoaderBlock(`C:\a\secrets.env`, "0.1.0"))
	out2, action := InjectLoader(out, LoaderBlock(`D:\b\secrets.env`, "0.1.0"))
	if action != ActionUpdated {
		t.Fatalf("换路径 = %q，想要 %q", action, ActionUpdated)
	}
	if n := strings.Count(out2, LoaderBegin); n != 1 {
		t.Errorf("托管区有 %d 份，想要 1 份", n)
	}
	if strings.Contains(out2, `C:\a\secrets.env`) {
		t.Error("旧托管区没被整段换掉")
	}
	if l := InspectLoader(out2); l.Secrets != `D:\b\secrets.env` {
		t.Errorf("回读到的凭据路径 = %q", l.Secrets)
	}
}

// 上游重装会整份重写这个文件 —— 这时托管区就没了，必须看得出来（否则凭据悄悄不生效）。
func TestInspectLoaderOnPristineUpstream(t *testing.T) {
	if l := InspectLoader(upstreamPS1); l.Found {
		t.Errorf("上游脚本里没有托管区，不该报 Found：%+v", l)
	}
}

// 手改托管区（比如把凭据路径指向别处）必须被看出来。
func TestInspectLoaderDetectsTamper(t *testing.T) {
	out, _ := InjectLoader(upstreamPS1, LoaderBlock(`C:\a\secrets.env`, "0.1.0"))
	tampered := strings.Replace(out, "$ancEq -lt 1", "$ancEq -lt 2", 1)
	l := InspectLoader(tampered)
	if !l.Found {
		t.Fatal("托管区还在，不该报 Found=false")
	}
	if l.Intact() {
		t.Error("手改过还报完好")
	}
}

// 上游在 Windows 上写的是 CRLF，注入不该把整份文件的行尾改掉。
func TestInjectLoaderKeepsCRLF(t *testing.T) {
	crlf := strings.ReplaceAll(upstreamPS1, "\n", "\r\n")
	out, _ := InjectLoader(crlf, LoaderBlock(`C:\x\secrets.env`, "0.1.0"))
	if strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
		t.Error("注入后混进了裸 LF")
	}
	if l := InspectLoader(out); !l.Intact() {
		t.Errorf("CRLF 文件回读不一致：%+v", l)
	}
}

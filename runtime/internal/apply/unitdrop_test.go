package apply

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUnitDropInPaths(t *testing.T) {
	unit := filepath.Join("unitdir", "cc-connect.service")
	wantDir := filepath.Join("unitdir", "cc-connect.service.d")
	if got := UnitDropInDir(unit); got != wantDir {
		t.Errorf("UnitDropInDir = %q，想要 %q", got, wantDir)
	}
	wantFile := filepath.Join(wantDir, "anc-secrets.conf")
	if got := UnitDropInFile(unit); got != wantFile {
		t.Errorf("UnitDropInFile = %q，想要 %q", got, wantFile)
	}
}

// drop-in 必须落进 [Service] 段：EnvironmentFile 是 Service 指令，写在 [Unit] / [Install]
// 里 systemd 直接拒收 —— 这是回落读时最容易踩的一处。
func TestUnitBlockTargetsServiceSection(t *testing.T) {
	block := UnitBlock("/home/dev/.anc/secrets.env", "0.1.0")
	if !strings.Contains(block, "\n[Service]\n") {
		t.Fatalf("没有 [Service] 段：\n%s", block)
	}
	// 不加引号是硬要求：systemd 的 EnvironmentFile= 不去引号，加了引号整串会被当成
	// 非绝对路径而整条忽略（实测报 `path is not absolute, ignoring`）—— 那样凭据桥等于没做。
	if !strings.Contains(block, "\nEnvironmentFile=/home/dev/.anc/secrets.env\n") {
		t.Fatalf("EnvironmentFile 行不对（必须不带引号）：\n%s", block)
	}
	for _, ln := range strings.Split(block, "\n") {
		if strings.HasPrefix(ln, "EnvironmentFile=") && strings.ContainsAny(ln, "\"'") {
			t.Fatalf("EnvironmentFile 带了引号：%q", ln)
		}
	}
	if !strings.HasPrefix(block, LoaderBegin) || !strings.HasSuffix(block, LoaderEnd) {
		t.Fatalf("边界标记不对：\n%s", block)
	}
}

// 回读：写进去的路径要能原样取回来（回显与「被改过」判据都靠它）。
func TestInspectUnitRoundTrip(t *testing.T) {
	path := "/home/dev/.anc/secrets.env"
	out, action := InjectLoader("", UnitBlock(path, "0.1.0"))
	if action != ActionAdded {
		t.Errorf("空文件注入 action = %q，想要 %q", action, ActionAdded)
	}
	l := InspectUnit(out)
	if !l.Found || !l.Intact() {
		t.Fatalf("回读不完整：%+v", l)
	}
	if l.Secrets != path {
		t.Errorf("回读路径 = %q，想要 %q", l.Secrets, path)
	}
}

// 幂等：同一份内容再注入一次，一个字节都不许变（重跑 apply 不该产生 diff）。
func TestUnitBlockIdempotent(t *testing.T) {
	block := UnitBlock("/x/.anc/secrets.env", "0.1.0")
	first, _ := InjectLoader("", block)
	second, action := InjectLoader(first, block)
	if action != ActionUnchanged {
		t.Errorf("重注 action = %q，想要 %q", action, ActionUnchanged)
	}
	if second != first {
		t.Error("重注改了内容")
	}
}

// 被手改过要看得出来：换了 EnvironmentFile 的路径，指纹就对不上。
func TestInspectUnitDetectsTamper(t *testing.T) {
	out, _ := InjectLoader("", UnitBlock("/x/.anc/secrets.env", "0.1.0"))
	tampered := strings.Replace(out, "/x/.anc/secrets.env", "/tmp/evil.env", 1)
	if l := InspectUnit(tampered); l.Intact() {
		t.Errorf("被改过却判成完好：%+v", l)
	}
}

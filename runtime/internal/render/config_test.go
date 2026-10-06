package render

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anc/internal/org"
)

// 本机层输入用合成值：persona 段 3 会把 vault_root 写进去，
// 用真实临时路径会让快照和哈希随机器变 —— 那就不是契约了。
func fixtureOrg(t *testing.T, name string) (*org.Org, org.Host) {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "orgs", name))
	if err != nil {
		t.Fatal(err)
	}
	o, err := org.Load(abs)
	if err != nil {
		t.Fatalf("加载 %s: %v", name, err)
	}
	return o, org.Host{VaultRoot: "/vault", HomesRoot: "/homes", DataDir: "/data"}
}

var fixedNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// 确定性：同一真相源 + 同一时刻，两次渲染必须逐字节相同。
// 这条红了，漂移检测就是在拿噪声报警。
func TestBuildDeterministic(t *testing.T) {
	o, host := fixtureOrg(t, "six")
	opt := Options{Host: host, Version: "test", Now: fixedNow}
	a, err := Build(o, opt)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(o, opt)
	if err != nil {
		t.Fatal(err)
	}
	if a.Text != b.Text {
		t.Fatal("两次渲染结果不一致（map 遍历或排序不稳定）")
	}
	if strings.Join(a.Projects, ",") != strings.Join(b.Projects, ",") {
		t.Fatal("两次渲染的 project 顺序不一致")
	}
}

// 时间只该影响指纹头，不该影响正文 —— 这是 --check 只报「人或 org 变了」的前提。
func TestTimeOnlyAffectsHeader(t *testing.T) {
	o, host := fixtureOrg(t, "one")
	a, err := Build(o, Options{Host: host, Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(o, Options{Host: host, Version: "test", Now: fixedNow.Add(72 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if a.Text == b.Text {
		t.Fatal("指纹头没有时间戳，就分不出是哪一轮生成的")
	}
	if StripFingerprint(a.Text) != StripFingerprint(b.Text) {
		t.Fatal("时间渗进了正文：剥掉指纹头后两次渲染应当一致")
	}
}

// 往返回读：生成物必须能被自己的读回器完整读出来。
// 读不回 = 漂移检测是假的（它比的是两份都读错了的东西）。
func TestRoundTripReadBack(t *testing.T) {
	o, host := fixtureOrg(t, "six")
	p, err := Build(o, Options{Host: host, Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}

	ver, inputs, _, ok := Fingerprint(p.Text)
	if !ok {
		t.Fatal("生成物没有 anc 指纹头")
	}
	if ver != "test" || inputs != p.InputsHash {
		t.Fatalf("指纹读回不符：v=%q inputs=%q，期望 v=test inputs=%q", ver, inputs, p.InputsHash)
	}

	got := ProjectsIn(p.Text)
	if strings.Join(got, ",") != strings.Join(p.Projects, ",") {
		t.Fatalf("project 读回不符\n  实际 %v\n  期望 %v", got, p.Projects)
	}
	hashes := PersonaHashesIn(p.Text)
	if len(hashes) != len(p.Projects) {
		t.Fatalf("读回 %d 个 persona，期望 %d 个", len(hashes), len(p.Projects))
	}
	for _, name := range p.Projects {
		if hashes[name] != p.PersonaHash[name] {
			t.Fatalf("%s 的 persona 哈希读回不符：%s ≠ %s", name, hashes[name], p.PersonaHash[name])
		}
	}
	if n := strings.Count(p.Text, "\n[[projects]]\n"); n != len(p.Projects) {
		t.Fatalf("配置里有 %d 个 [[projects]] 段，期望 %d 个", n, len(p.Projects))
	}
}

func TestStripFingerprintKeepsEverythingElse(t *testing.T) {
	o, host := fixtureOrg(t, "one")
	p, err := Build(o, Options{Host: host, Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	stripped := StripFingerprint(p.Text)
	if strings.Contains(stripped, "anc:generated") {
		t.Fatal("剥指纹没剥干净")
	}
	if _, _, _, ok := Fingerprint(stripped); ok {
		t.Fatal("剥过指纹后还能读出指纹，说明只剥了一部分")
	}
	if strings.Count(stripped, "\n[[projects]]\n") != len(p.Projects) {
		t.Fatal("剥指纹动到了正文")
	}
}

// 未实测的字段不许静默丢掉：配了就要有话说。
func TestUnverifiedSectionsWarnInsteadOfSilentlyDropping(t *testing.T) {
	o, host := fixtureOrg(t, "one")
	o.Company.Fallback = &org.FallbackProvider{Name: "x", BaseURL: "https://example.invalid", Model: "m"}
	o.Company.Defaults.AutoCompressMaxTokens = 100000
	p, err := Build(o, Options{Host: host, Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(p.Warns, "\n")
	if !strings.Contains(joined, "providers") || !strings.Contains(joined, "auto_compress") {
		t.Fatalf("未实测字段被静默丢掉了，warns=%v", p.Warns)
	}
}

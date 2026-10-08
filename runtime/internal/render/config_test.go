package render

import (
	"os"
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

// v1 口径：bot 间通道的机制保留，但默认零绑定（SPEC §6 不变量 3 / §13 Q13）。
// 上游对这个段自带默认值且默认是「开着」，所以产物必须显式覆盖它 —— 不写就等于静默放行。
func TestRelayExplicitlyOffByDefault(t *testing.T) {
	o, host := fixtureOrg(t, "six")
	p, err := Build(o, Options{Host: host, Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	n, ok := RelayIn(p.Text)
	if !ok {
		t.Fatal("产物没有显式的 [relay] 段：等于沿用上游默认 timeout_secs=120，bot 间通道被静默放行")
	}
	if n != 0 {
		t.Fatalf("v1 口径是零绑定，产物却写了 timeout_secs=%d", n)
	}
	if !strings.Contains(p.Text, `visibility = "summary"`) {
		t.Fatal("「通道只传摘要」这个默认档没有显式写出来")
	}
	if gaps := SecurityGaps(p.Text); len(gaps) != 0 {
		t.Fatalf("自己的产物不该有安全缺口，却报了 %v", gaps)
	}
}

// 负向断言：产物里 relay 段被抹掉、或值被改开，都必须被逮住。
// 这是 GitHub #33 的验收 —— 「渲染器的负向断言能拦住有人把 relay 打开」。
func TestSecurityGapsCatchesRelayHoles(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"段被整个抹掉", "data_dir = \"/data\"\n\n[display]\nmode = \"full\"\n"},
		{"段在但没写 timeout_secs", "[relay]\nvisibility = \"summary\"\n"},
		{"被改回上游默认（开着）", "[relay]\ntimeout_secs = 120\n"},
		{"被改成任意非零", "[relay]\ntimeout_secs = 60\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if gaps := SecurityGaps(c.text); len(gaps) == 0 {
				t.Fatal("这个产物有安全缺口，却被放过了")
			}
			if err := AssertSecurityExplicit(c.text); err == nil {
				t.Fatal("AssertSecurityExplicit 没拦住")
			}
		})
	}
}

// 读回器本身要有准头：写了合法值时必须读得出来，否则上面两条断言都是在猜。
func TestRelayInReadsExplicitValue(t *testing.T) {
	n, ok := RelayIn("[relay]\ntimeout_secs = 0\nvisibility = \"summary\"\n")
	if !ok || n != 0 {
		t.Fatalf("读回 (n=%d, ok=%v)，期望 (0, true)", n, ok)
	}
	if _, ok := RelayIn("[[projects]]\nname = \"x\"\n\n[projects.agent]\ntype = \"claudecode\"\n"); ok {
		t.Fatal("没有 [relay] 段却读出值来，说明扫到别的段里去了")
	}
}

// copyOrgFixture 把 fixture 拷到临时目录 —— 只有这样才能「客户改一行数据」再渲染。
func copyOrgFixture(t *testing.T, name string) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "..", "testdata", "orgs", name))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	return dst
}

// 空闲重置出厂就是**关掉**（0）—— 2026-10-07 拍板：换新会话由人显式发 /new。
// 这条锁的是产品行为本身：它曾经是代码里写死的 30，既没有拍板依据，客户也改不了。
func TestResetOnIdleOffByDefault(t *testing.T) {
	o, host := fixtureOrg(t, "one")
	plan, err := Build(o, Options{Host: host, Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Text, "\nreset_on_idle_mins = 0\n") {
		t.Fatalf("默认应当是 0（关掉），产物里没有：\n%s", plan.Text)
	}
}

// 客户写多少就渲染多少 —— 这条保证「定制只动数据、不动代码」。
func TestResetOnIdleFollowsData(t *testing.T) {
	v := copyOrgFixture(t, "one")
	path := filepath.Join(v, "company", "company.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	const anchor = "  mode: dontAsk\n"
	i := strings.Index(text, anchor)
	if i < 0 {
		t.Fatalf("fixture 的 defaults 段找不到：\n%s", text)
	}
	text = text[:i+len(anchor)] + "  reset_on_idle_mins: 45\n" + text[i+len(anchor):]
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := org.Load(v)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build(o, Options{Host: org.Host{VaultRoot: "/vault", HomesRoot: "/homes", DataDir: "/data"}, Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Text, "\nreset_on_idle_mins = 45\n") {
		t.Fatalf("数据写 45，产物应当也是 45：\n%s", plan.Text)
	}
}

// 产物要哪些凭据键，是写 app_secret 那一行当场记下的账（键名跟着成员名走）。
// 账和「回读产物文本」必须一致 —— 不一致就说明账记错了，体检会去要一个不存在的键。
func TestSecretKeysAreRecorded(t *testing.T) {
	o, host := fixtureOrg(t, "six")
	p, err := Build(o, Options{Host: host, Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	const want = "ANC_FEISHU_SECRET_ALICE,ANC_FEISHU_SECRET_BOB,ANC_FEISHU_SECRET_CAROL,ANC_FEISHU_SECRET_DAVE,ANC_FEISHU_SECRET_ERIN,ANC_FEISHU_SECRET_DEVBOT"
	if got := strings.Join(p.SecretKeys, ","); got != want {
		t.Fatalf("SecretKeys = %s\n        想要 %s（顺序 = project 顺序）", got, want)
	}
	accounted := map[string]bool{}
	for _, k := range p.SecretKeys {
		accounted[k] = true
	}
	refs := EnvRefs(p.Text)
	if len(refs) != len(accounted) {
		t.Fatalf("产物引用 %d 个键，账上是 %d 个：%v vs %v", len(refs), len(accounted), refs, p.SecretKeys)
	}
	for _, ref := range refs {
		if !accounted[ref] {
			t.Fatalf("产物引用了 %s，账上没有", ref)
		}
	}
}

// 成员名带中文时，账必须照样如实记下那个键 —— 哪怕它写不进 secrets.env。
//
// 回读器看不见它（那把正则只认 ASCII），这正是「账要记、不能回读」的现场：
// 回读会把「这个 bot 永远要不到凭据」说成「✅ 齐」。
func TestSecretKeysKeepUnwritableNames(t *testing.T) {
	o, host := fixtureOrg(t, "one")
	for i := range o.Members {
		if o.Members[i].Name == "alice" {
			o.Members[i].Name = "张三"
		}
	}
	p, err := Build(o, Options{Host: host, Version: "test", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	const key = "ANC_FEISHU_SECRET_张三"
	if !strings.Contains(p.Text, "${"+key+"}") {
		t.Fatalf("产物里没有 ${%s}：%v", key, p.SecretKeys)
	}
	found := false
	for _, k := range p.SecretKeys {
		if k == key {
			found = true
		}
	}
	if !found {
		t.Fatalf("账上没有 %s：%v", key, p.SecretKeys)
	}
	for _, ref := range EnvRefs(p.Text) {
		if ref == key {
			t.Fatalf("回读器居然看得见 %s —— 这条用例的前提变了", key)
		}
	}
}

package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anc/internal/apply"
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

// 非 ASCII 名（中文）也要拿到**写得出**的键名：派生是全函数 —— ASCII 直通，其余按码点转义。
//
// 2026-10-08 之前这条用例钉的是「账要记、回读看不见」；现在钉相反的事实：键名是 ASCII，
// 账与回读**一致**（回读看不见 = 当年那个「要不到凭据、静默起不来」的形状）。
func TestSecretKeysEncodeNonASCIINames(t *testing.T) {
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
	const key = "ANC_FEISHU_SECRET_x005f20x004e09"
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
			return
		}
	}
	t.Fatalf("回读看不见 %s —— 键名该是 ASCII 了：%v", key, EnvRefs(p.Text))
}

// 键名的派生：存量 ASCII 名一个字节都不变；转义段定长、且与直通段按构造不相交。
//
// 「写得出写不出」由装载侧那把尺子说了算（apply.LegalKey），这儿不另写一条正则 —— 两条尺子迟早漂。
func TestSecretKeyDerivation(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"alice", "ANC_FEISHU_SECRET_ALICE"},
		{"devbot", "ANC_FEISHU_SECRET_DEVBOT"},
		{"a_b", "ANC_FEISHU_SECRET_A_B"},
		{"alice-2", "ANC_FEISHU_SECRET_ALICEx00002d2"},
		{"a.b", "ANC_FEISHU_SECRET_Ax00002eB"},
		{"张三", "ANC_FEISHU_SECRET_x005f20x004e09"},
		{"白嘉伟", "ANC_FEISHU_SECRET_x00767dx005609x004f1f"},
	} {
		got := FeishuSecretKey(tc.name)
		if got != tc.want {
			t.Errorf("FeishuSecretKey(%q) = %s，想要 %s", tc.name, got, tc.want)
		}
		if !apply.LegalKey(got) {
			t.Errorf("FeishuSecretKey(%q) = %s 不是合法的环境变量名 —— 装载侧写不进去", tc.name, got)
		}
	}
	// 不同的名不许撞成同一个键（撞了就是两个人共用一个凭据槽）。
	seen := map[string]string{}
	for _, name := range []string{"alice", "alice.", "alice-2", "张三", "白嘉伟", "张 三", "手冢治虫", "a_b", "a.b"} {
		k := FeishuSecretKey(name)
		if prev, dup := seen[k]; dup {
			t.Errorf("%q 与 %q 撞成同一个键 %s", name, prev, k)
		}
		seen[k] = name
	}
}

// 已知残留：名只差大小写（alice / ALICE）会撞成同一个键 —— 存量键就是大写折叠出来的，
// 要修得动存量 vault。这条把它钉成已知事实：哪天改了，就该红，逼着同步 DESIGN §7.1.16 的残留清单。
func TestSecretKeyCaseFoldingResidual(t *testing.T) {
	if FeishuSecretKey("Alice") != FeishuSecretKey("alice") {
		t.Fatal("大小写折叠的残留没了 —— 同步 runtime/DESIGN.md §7.1.16 的「残留」一节，再改这条用例")
	}
}

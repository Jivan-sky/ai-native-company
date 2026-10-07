package render

import "testing"

// WorkDirs 的坑在区间判断：work_dir 住在 [projects.agent.options] 里，
// 也就是**项目名之后还有别的段头**。按「遇到 [ 就重置」写会把它整段丢掉。
func TestWorkDirsFindsWorkDirAfterSectionHeaders(t *testing.T) {
	cfg := `
data_dir = "D:/data"

[[projects]]
name = "demo-alice"
admin_from = "ou_x"

[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = "D:\\homes/alice"
mode = "dontAsk"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_x"

[[projects]]
name = "demo-bob"

[projects.agent.options]
work_dir = "D:\\homes/bob"
`
	got := WorkDirs(cfg)
	if len(got) != 2 {
		t.Fatalf("该有 2 个 project 的 work_dir，拿到 %+v", got)
	}
	if got["demo-alice"] != `D:\homes/alice` {
		t.Errorf("demo-alice 的 work_dir 不对：%q", got["demo-alice"])
	}
	if got["demo-bob"] != `D:\homes/bob` {
		t.Errorf("demo-bob 的 work_dir 不对：%q", got["demo-bob"])
	}
}

// work_dir 出现在项目名之前 = 不属于任何 project，不许瞎认。
func TestWorkDirsIgnoresWorkDirBeforeAnyProject(t *testing.T) {
	cfg := `
work_dir = "D:\\rogue"

[[projects]]
name = "demo-alice"
`
	got := WorkDirs(cfg)
	if len(got) != 0 {
		t.Fatalf("项目名之前的 work_dir 不该被认领，拿到 %+v", got)
	}
}

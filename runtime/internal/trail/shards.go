package trail

// 没有 cc-connect 桥的机器上，按记录自带的 cwd 把分片认领到项目。
//
// **为什么要有它**：`anc trail` 原来只走桥（`<data>/sessions/<project>_*.json`）——
// 桥是 cc-connect 写的。一台**没装 gateway** 的机器上桥一个都没有，于是 trail 报
// 「0 个会话」，而同一批字节 `anc audit collect` 靠分片枚举读得出 27 次行使。
// 同一个读取器、两个消费面，一个报 0 —— 那不是「没有会话」，是**看不见**
// （2026-11-09 在本机真记录上撞到，记在 DESIGN §7.1.84）。
//
// 口径三条：
//
//  1. **只在没有桥的时候兜底**：调用方先走桥；桥给出了会话，就不再用分片补
//     （否则同一次会话会被算两遍 —— 那是比看不见更坏的错）。
//  2. **归属靠记录自己说的 cwd**：与 `audit collect` 同一把尺子（Clean + ToSlash，
//     Windows 上再折大小写），不猜、不从目录名反推。
//  3. **认不出归属也摆出来**：cwd 为空的分片照样返回，带一条 Problem ——
//     它对不上任何项目，但「有一条记录读不懂归属」是事实，不能悄悄丢掉。

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"anc/internal/harness"
)

// ShardSessions 枚举一家的全部分片，挑出 cwd 落在 workDir 里的那些，标成 project。
// 第二个返回值是「这家根本没有分片枚举」（不是错误，就是没有这条路）。
func ShardSessions(fam harness.Family, root, project, workDir string) ([]Session, bool) {
	shards, ok, err := fam.EnumerateShards(root)
	if !ok {
		return nil, false
	}
	want := normDir(workDir)
	var out []Session
	for _, path := range shards {
		s := ReadShard(fam, path)
		if !s.Found {
			// 读不到也摆出来：它可能是我们的、也可能是别人的，不静默跳过。
			out = append(out, s)
			continue
		}
		switch {
		case strings.TrimSpace(s.Cwd) == "":
			s.Problems = append(s.Problems,
				"这份分片里没有 cwd —— 认不出它属于哪个项目（不是「没有」）")
			out = append(out, s)
		case normDir(s.Cwd) == want:
			s.Project = project
			out = append(out, s)
		}
	}
	if err != nil {
		return out, true
	}
	return out, true
}

// normDir 与 audit 的 normPath 同一把尺子：Clean + ToSlash，Windows 上折大小写。
// 两处各写一份是不好，但跨包共享它得先有个中性落点（见 §7.1.84 的待办）。
func normDir(p string) string {
	p = filepath.ToSlash(filepath.Clean(strings.TrimSpace(p)))
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// DirSessions 按「项目目录」认记录：`<记录根>/<projects_dir>/<slug(work_dir)>/*.jsonl`。
//
// 与 ShardSessions 是同一件事的两条路：claude / codebuddy 这类把每个项目放一个目录的家，
// 目录名就是从 work_dir 推出来的，不需要按 cwd 认领。同样只在**没有桥**时兜底。
//
// 目录不在 = 这个项目还没有过会话（或没跑在这台机器上）—— 返回空 + ok，调用方不该报错。
func DirSessions(fam harness.Family, root, project, workDir string) ([]Session, bool) {
	dir, err := fam.TranscriptDir(root, workDir)
	if err != nil {
		return nil, false
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, true
	}
	out := make([]Session, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		s := ReadShard(fam, filepath.Join(dir, e.Name()))
		s.Project = project
		out = append(out, s)
	}
	return out, true
}

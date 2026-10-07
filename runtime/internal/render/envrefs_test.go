package render

import (
	"strings"
	"testing"
)

func TestEnvRefs(t *testing.T) {
	got := EnvRefs("app_secret = \"${ANC_B}\"\nkey = \"${ANC_A}\"\nagain = \"${ANC_A}\"\n")
	if want := "ANC_A,ANC_B"; strings.Join(got, ",") != want {
		t.Errorf("EnvRefs = %v，想要 %s（排序 + 去重）", got, want)
	}
}

// 带默认值的引用不算「缺了起不来」，所以不该进这份清单（进了就是误报）。
func TestEnvRefsIgnoresDefaults(t *testing.T) {
	if got := EnvRefs(`k = "${ANC_A:-fallback}"`); len(got) != 0 {
		t.Errorf("带默认值的引用不该算：%v", got)
	}
}

// 产物里没有引用（全 inline 的坏配置）时给空 —— 不是「全都缺」。
func TestEnvRefsEmpty(t *testing.T) {
	if got := EnvRefs("data_dir = \"D:\\\\x\"\n"); len(got) != 0 {
		t.Errorf("没有引用却报出 %v", got)
	}
}

// 产物头部那行说明里就写着 ${ENV}（讲口径用的）。把它当成真实的键名，
// 现场就会跑去 secrets.env 里找一个叫 ENV 的东西 —— 实测在沙箱里踩到过。
func TestEnvRefsSkipsCommentLines(t *testing.T) {
	text := "# 凭据一律 ${ENV} 引用，明文只住在 secrets.env。\napp_secret = \"${ANC_A}\"\n"
	if got := EnvRefs(text); strings.Join(got, ",") != "ANC_A" {
		t.Errorf("EnvRefs = %v，想要 [ANC_A]（注释里的不算）", got)
	}
}

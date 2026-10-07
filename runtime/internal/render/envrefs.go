package render

import (
	"regexp"
	"sort"
	"strings"
)

// 注意别和 persona.go 里的 reEnvRef 混了：那个只认「出现了 ${」这个前缀，
// 用来拦截 persona 里的美元大括号（会被 gateway 吞掉）；这个要取**名字**。
var reEnvName = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// EnvRefs 返回 config 文本里引用的环境变量名（${NAME} 形式），排序 + 去重。
//
// 用途只有一个：装载前体检。产物里凭据一律写成 ${ENV} 引用（明文只住 secrets.env），
// 而「引用了但没定义」不表现为配置报错，表现为 gateway **起不来** ——
// 所以这一层提前把「产物要哪些键」算出来，交给装载侧去比对那份凭据文件。
//
// 只认裸的 ${NAME}：带默认值的 ${NAME:-x} 不算引用（缺了也起得来），因此不报。
//
// **整行注释不算引用**：产物头部那行说明里就写着 `${ENV}`（讲口径用的），
// 把它当成一个真实的键名，现场就会去 secrets.env 里找一个叫 ENV 的东西（实测踩到过）。
func EnvRefs(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range reEnvName.FindAllStringSubmatch(line, -1) {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

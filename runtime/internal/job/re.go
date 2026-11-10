package job

import "regexp"

// reName 是作业名的字符集：它会当状态文件名与 launchd label 的一部分。
var reName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

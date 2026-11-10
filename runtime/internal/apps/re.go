package apps

import "regexp"

var reName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

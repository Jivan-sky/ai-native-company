package org

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCapTableAllow(t *testing.T) {
	vault := t.TempDir()
	body := `# 能力表

| role | module | action | scope |
|---|---|---|---|
| manager | audit | read | all |
| manager | crm | admin | all |
| sales | crm | read | self |
| devbot | * | read | all |
`
	if err := os.WriteFile(filepath.Join(vault, CapFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	caps, err := LoadCapabilities(vault)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		role, module, action string
		want                 bool
	}{
		{"manager", "audit", "read", true},
		{"manager", "crm", "write", true}, // admin 是通配动作
		{"manager", "crm", "read", true},  // admin 覆盖 read
		{"sales", "crm", "read", true},
		{"sales", "crm", "write", false},  // 只有 read
		{"sales", "audit", "read", false}, // 没有这块
		{"devbot", "audit", "read", true}, // * 是通配模块
		{"devbot", "audit", "write", false},
		{"nobody", "audit", "read", false}, // 没这个角色 = 全拒
		{"", "audit", "read", false},       // 匿名 = 全拒
	}
	for _, c := range cases {
		if got := caps.Allow(c.role, c.module, c.action); got != c.want {
			t.Errorf("Allow(%q,%q,%q) = %v，要 %v", c.role, c.module, c.action, got, c.want)
		}
	}
}

// 没有能力表 = 空表 = 全拒，但不是错误：刚 init 的机器不该红。
func TestCapTableMissingFile(t *testing.T) {
	caps, err := LoadCapabilities(t.TempDir())
	if err != nil {
		t.Fatalf("文件不在不该报错：%v", err)
	}
	if caps.Allow("manager", "audit", "read") {
		t.Error("没有能力表就该全拒")
	}
}

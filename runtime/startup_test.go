package main

import (
	"runtime"
	"testing"
	"unicode/utf16"
)

// 用例是真机抓的：Windows 计划任务 cc-connect 的定义（2026-10-08，笔记本，未接电源）。
// 抓下来是 UTF-16、只有登录触发器、两条电源设置都是 true —— 三样都进了下面的用例。
const taskXML = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.3" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Principals>
    <Principal id="Author">
      <UserId>S-1-5-21-2766059460-2847692454-1436359435-1001</UserId>
      <LogonType>InteractiveToken</LogonType>
    </Principal>
  </Principals>
  <Settings>
    <DisallowStartIfOnBatteries>true</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>true</StopIfGoingOnBatteries>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
  </Settings>
  <Triggers>
    <LogonTrigger>
      <UserId>LAPTOP-XXXXXXXX\dev</UserId>
    </LogonTrigger>
  </Triggers>
</Task>`

// utf16LE 把字面量编成 schtasks 那种「BOM + UTF-16LE」的字节，走一遍真解码路径。
func utf16LE(s string) []byte {
	b := []byte{0xFF, 0xFE}
	for _, r := range utf16.Encode([]rune(s)) {
		b = append(b, byte(r), byte(r>>8))
	}
	return b
}

func TestParseTaskXMLUTF16(t *testing.T) {
	f, err := parseTaskXML(utf16LE(taskXML))
	if err != nil {
		t.Fatalf("解析真机抓下来的 UTF-16 任务定义报错：%v", err)
	}
	if len(f.Triggers) != 1 || f.Triggers[0] != "LogonTrigger" {
		t.Errorf("触发器 = %v，想要 [LogonTrigger]", f.Triggers)
	}
	if !f.NoStartOnBatt || !f.StopOnBatt {
		t.Errorf("两条电源设置该都是 true，实际 NoStartOnBatt=%v StopOnBatt=%v", f.NoStartOnBatt, f.StopOnBatt)
	}
}

func TestParseTaskXMLUTF8PassThrough(t *testing.T) {
	// 没有 BOM 的那条路也要能走（schtasks 换成输出 UTF-8 时不许哑掉）
	f, err := parseTaskXML([]byte(taskXML))
	if err != nil {
		t.Fatalf("无 BOM 的 UTF-8 任务定义解析报错：%v", err)
	}
	if len(f.Triggers) != 1 {
		t.Errorf("触发器 = %v，想要 1 条", f.Triggers)
	}
}

// 判据只认 BootTrigger：登录触发器和开机触发器长得很像，不能混为一谈。
func TestHasBootTrigger(t *testing.T) {
	if hasBootTrigger([]string{"LogonTrigger"}) {
		t.Error("只有登录触发器不该算「开机就起」")
	}
	if !hasBootTrigger([]string{"LogonTrigger", "BootTrigger"}) {
		t.Error("带开机触发器就该算过")
	}
	if hasBootTrigger(nil) {
		t.Error("没有触发器不许算过")
	}
}

func TestParseLinger(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"Linger=yes\n", true},
		{"Linger=no\n", false},
		{"", false},
		{"Linger=yes", true},
	} {
		if got := parseLinger(c.in); got != c.want {
			t.Errorf("parseLinger(%q) = %v，想要 %v", c.in, got, c.want)
		}
	}
}

// 没覆盖的平台返回 nil：doctor 不许因为「这个平台不知道查什么」而报出假红。
func TestStartupChecksOnThisPlatform(t *testing.T) {
	cs := startupChecks()
	switch runtime.GOOS {
	case "windows", "linux":
		if cs == nil {
			t.Fatalf("%s 上该有自检项", runtime.GOOS)
		}
		for _, c := range cs {
			if c.Name == "" {
				t.Errorf("自检项缺名字：%+v", c)
			}
		}
	default:
		if cs != nil {
			t.Errorf("%s 上该返回 nil，实际 %v", runtime.GOOS, cs)
		}
	}
}

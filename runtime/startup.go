package main

import (
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"unicode/utf16"
)

// ---------- 断电兜底：断电 / 重启之后，这东西会不会自己回来 ----------
//
// 与 anc probe 的分工：probe 看**现在**通不通话（socket + 会话事实）；
// 这一组看的是**下一次机器自己起来**时谁把它拉回来 —— 只读，不替客户改上游默认，
// 也不设门禁（doctor 的结论行本来就写着「⚠️ 不一定是阻断项」）。
//
// Linux 三条前提缺一条都不成立：单元存在 + 已 enable + Linger=yes；
// Windows 看触发器里有没有「开机就起」那一条，再看两条电源设置。
// 口径与三层实测见 runtime/DESIGN.md §7.1.11。

const startupTaskName = "cc-connect"

// taskFacts 是计划任务定义里与「起得来吗」有关的那几样。
type taskFacts struct {
	Triggers      []string
	NoStartOnBatt bool
	StopOnBatt    bool
}

// parseTaskXML 只取三样东西：触发器种类、两条电源设置。
func parseTaskXML(b []byte) (taskFacts, error) {
	var f taskFacts
	var doc struct {
		Settings struct {
			DisallowStartIfOnBatteries bool `xml:"DisallowStartIfOnBatteries"`
			StopIfGoingOnBatteries     bool `xml:"StopIfGoingOnBatteries"`
		} `xml:"Settings"`
		Triggers struct {
			Any []struct {
				XMLName xml.Name
			} `xml:",any"`
		} `xml:"Triggers"`
	}
	dec := xml.NewDecoder(strings.NewReader(toUTF8(b)))
	// 编码声明在 toUTF8 里已经改掉了；这个 CharsetReader 什么都不做，只是兜一层：
	// 哪天声明又变回别的名字，也不至于整条判据哑成「读不懂」。
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	if err := dec.Decode(&doc); err != nil {
		return f, err
	}
	for _, t := range doc.Triggers.Any {
		f.Triggers = append(f.Triggers, t.XMLName.Local)
	}
	f.NoStartOnBatt = doc.Settings.DisallowStartIfOnBatteries
	f.StopOnBatt = doc.Settings.StopIfGoingOnBatteries
	return f, nil
}

// toUTF8 把 schtasks 交出来的 UTF-16 转成 UTF-8。顺手把 XML 头里的编码声明改成
// UTF-8：声明留着不改，encoding/xml 会因为「不认这个编码又没有 CharsetReader」直接报错。
func toUTF8(b []byte) string {
	s := string(b)
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		s = string(utf16.Decode(u))
	}
	// 声明一定要改成 UTF-8 —— 留在那儿不改，encoding/xml 会因为「不认这个编码」直接报错。
	// 无 BOM 那条路（schtasks 真换成输出 UTF-8 时）同样要改：实测漏掉它就整条读不懂。
	return strings.Replace(s, `encoding="UTF-16"`, `encoding="UTF-8"`, 1)
}

// hasBootTrigger：触发器里有没有一条「机器起来就起、不用人登录」的。
// 只认 BootTrigger —— 定时 / 事件触发器也能不靠人，但那是另一回事，不许捎带算过。
func hasBootTrigger(ts []string) bool {
	for _, t := range ts {
		if t == "BootTrigger" {
			return true
		}
	}
	return false
}

// parseLinger 认 loginctl -p Linger 那一行（`Linger=yes`）。
func parseLinger(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && k == "Linger" {
			return strings.EqualFold(strings.TrimSpace(v), "yes")
		}
	}
	return false
}

// startupChecks 是「断电兜底」这一组；没覆盖的平台返回 nil（不许空手报错）。
func startupChecks() []check {
	switch runtime.GOOS {
	case "windows":
		return windowsStartupChecks()
	case "linux":
		return linuxStartupChecks()
	}
	return nil
}

// windowsStartupChecks：任务还没装就明确跳过 —— 不给一台刚 init 的机器平添红字。
func windowsStartupChecks() []check {
	out, err := exec.Command("schtasks", "/query", "/tn", startupTaskName, "/xml", "ONE").Output()
	if err != nil {
		return []check{{"自启兜底", true, "计划任务 " + startupTaskName + " 不在（还没 apply）—— 跳过"}}
	}
	f, err := parseTaskXML(out)
	if err != nil {
		return []check{{"自启兜底", false, "读不懂任务定义：" + err.Error()}}
	}
	triggers := strings.Join(f.Triggers, " + ")
	var cs []check
	if hasBootTrigger(f.Triggers) {
		cs = append(cs, check{"自启兜底", true, "触发器 " + triggers + " —— 断电重启后不等人登录"})
	} else {
		cs = append(cs, check{"自启兜底", false, "触发器只有「" + triggers + "」—— 断电重启后没人登录就不会起（议题 #42）" +
			"；补一条开机触发器 = 改上游默认（要么以 SYSTEM 跑、要么存下口令），要先拍，见 DESIGN §7.1.11"})
	}
	if f.NoStartOnBatt || f.StopOnBatt {
		cs = append(cs, check{"电源条件", false, "电池供电不启动 / 拔电就停 —— 笔记本上它一直起不来（议题 #42；服务器没有电池，这条不适用）"})
	} else {
		cs = append(cs, check{"电源条件", true, "没设「电池时不启动 / 拔电就停」"})
	}
	return cs
}

// linuxStartupChecks：装着哪份单元、它会不会自己起。
func linuxStartupChecks() []check {
	units, err := systemdUnits()
	if err != nil {
		return []check{{"自启兜底", false, err.Error()}}
	}
	var u systemdUnit
	found := false
	for _, c := range units {
		if _, err := os.Stat(c.Path); err == nil {
			u, found = c, true
			break
		}
	}
	if !found {
		return []check{{"自启兜底", true, "systemd 单元 " + startupTaskName + " 不在（还没 apply）—— 跳过"}}
	}
	cs := []check{{"单元", true, u.Path}}
	args := []string{"is-enabled", startupTaskName}
	if u.User {
		args = append([]string{"--user"}, args...)
	}
	// disabled 会带退出码 1，而且提示语在 stderr：看字，不看码。
	out, _ := exec.Command("systemctl", args...).CombinedOutput()
	st := strings.TrimSpace(string(out))
	if st == "" {
		st = "(空)"
	}
	if strings.HasPrefix(st, "enabled") {
		cs = append(cs, check{"开机自启", true, "is-enabled: " + st})
	} else {
		cs = append(cs, check{"开机自启", false, "is-enabled: " + st + " —— 开机不会自己起"})
	}
	if !u.User {
		return cs // 系统单元不归 linger 管
	}
	cu, err := user.Current()
	if err != nil {
		return cs
	}
	lout, err := exec.Command("loginctl", "show-user", cu.Username, "-p", "Linger").Output()
	if err != nil {
		cs = append(cs, check{"Linger", false, "查不到（" + err.Error() + "）—— 没人登录时用户级 systemd 不会起"})
		return cs
	}
	if parseLinger(string(lout)) {
		cs = append(cs, check{"Linger", true, cu.Username + " = yes（不等人登录）"})
	} else {
		cs = append(cs, check{"Linger", false, "Linger=no —— 没人登录时用户级 systemd 不会起。" +
			"补：sudo loginctl enable-linger " + cu.Username})
	}
	return cs
}

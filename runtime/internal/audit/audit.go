// Package audit —— 「行使」的流水（SPEC §6 不变量 4：行使必留痕）。
//
// 一句话：**谁、在什么时候、对谁、行使了什么类别、结果如何** —— 落成行文本，进 git。
//
// 四条款口径：
//
//  1. **真相源是 `audit/*.jsonl`，不是数据库**（与 timeline 同一条理由）：
//     审计最需要的恰恰是 diff / review / 回滚 —— 二进制库这三样全丢。
//
//  2. **离线**。ANC 不在行使路径上：SPEC §6 要求能放在 harness / 平台的权限规则里拦
//     就别放业务代码，D4 又拍板不自己写 serve。所以这份流水只能靠两条腿凑：
//     ① **事后归集** —— 从 harness 原生记录里把「已经发生的行使」抽出来（collect.go）；
//     ② **显式补记** —— 归集不到的那一类（本可行使但没行使、出站动作）由人 / agent 写一条。
//     由此推出一件必须让读的人知道的事：**「本可以行使但没行使」永远不可能自动归集出来**，
//     它只能被显式记下 —— 这份流水的边界，页面上要如实写出来。
//
//  3. **只记不拦**。审计是「先记下来」，不是「先拦住」；拦住是授权层（议题 #32–#35）的事。
//     两件事绑在一起，等于又要重写一遍。
//
//  4. **词表不锁死**。Action / Result 认不出的**原样保留**、落 unknown 档，不报错、不吞。
//
// 还有一条是给读的人的：**记不到的必须显式报**（Load 出 Bad、Collect 出 Problems），
// 不许把「读不到」静默当成「没发生」—— 那和假绿是同一类错误。
package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DirName 是 vault 下这一层的名字。与 timeline / charters 一样不带编号前缀 ——
// 它不是 agent 的「数据来源」目录（那些是带编号、进 persona 路由表的），
// 所以它**必须登记进 org 的 skipDirs**，否则会漏进 persona 段 3 的路由表。
const DirName = "audit"

// maxLine 是单行上限。原生记录里的一行可能很大（整段工具输出塞在行里），
// 默认 64KB 会直接报错 —— 那是**读不到**，不是「没有」（同 trail.maxLine）。
const maxLine = 16 << 20

// 结果：三档 + 认不出的。
//
// **denied 与 failed 必须分开**（同 trail.Failure 的理由）：把「权限没给」说成「故障」，
// 会把人引去查一个不存在的问题；反过来则会让人去加权限，而问题还在。
const (
	ResultOK      = "ok"
	ResultDenied  = "denied"
	ResultFailed  = "failed"
	ResultUnknown = "unknown"
)

// 类别：与 grant 的 action 词表同源（SPEC §6 的 read | write | invoke）。
const (
	ActionRead   = "read"
	ActionWrite  = "write"
	ActionInvoke = "invoke"
)

// knownResults 只用来决定「要不要落 unknown 档」，**不是白名单**：不在表里的原样保留。
var knownResults = map[string]bool{ResultOK: true, ResultDenied: true, ResultFailed: true}

// ResultBand 把结果归一成四档之一。认不出的落 unknown —— **刻意不报错**：
// 数据里出现新词时，看板照常显示原词 + 灰点，而不是整页崩掉或把词吞掉（同 timeline.Band）。
func ResultBand(r string) string {
	s := strings.ToLower(strings.TrimSpace(r))
	if knownResults[s] {
		return s
	}
	return ResultUnknown
}

// Bands 是四档计数（看板顶部那排卡片用）。
type Bands struct {
	OK      int `json:"ok"`
	Denied  int `json:"denied"`
	Failed  int `json:"failed"`
	Unknown int `json:"unknown"`
}

// Tally 数四档各几条。
func Tally(rs []Record) Bands {
	var n Bands
	for _, r := range rs {
		switch ResultBand(r.Result) {
		case ResultOK:
			n.OK++
		case ResultDenied:
			n.Denied++
		case ResultFailed:
			n.Failed++
		default:
			n.Unknown++
		}
	}
	return n
}

// Record 是审计流水里的一条 = **一次行使**。
//
// 字段全部是**数据**：加字段不用迁移，老文件里没这一行就是空值。
//
// 职责边界（这一条最容易写歪）：
//   - 这里存的是**当时的事实**：谁、何时、什么工具、目标客体（`Object` 是原样的路径 / 标识）。
//   - **不存派生**：「这个目标属于哪个域」「算不算跨域」是**视图层**按**当前**域表算出来的
//     （同 timeline 的 Band 不住在 Entry 里）。存死派生值，会在域表调整之后变成假证据。
//   - `Object` 允许为空 —— 它为空的意思是「这条行使的目标抽不出来」（例如 Bash 的自由文本命令），
//     **不是「没有目标」**。归集器必须把这种情况报出来（见 collect.go 的 Problems）。
type Record struct {
	ID         string `json:"id"`                     // 唯一 id（归集重跑靠它去重）
	At         string `json:"at"`                     // RFC3339 —— 何时
	Actor      string `json:"actor"`                  // 谁（bot 名 / 人）
	Action     string `json:"action"`                 // 类别：read | write | invoke
	Object     string `json:"object,omitempty"`       // 对谁：目标客体（路径 / 标识）；空 = 抽不出
	Result     string `json:"result"`                 // 结果：ok | denied | failed
	Why        string `json:"why,omitempty"`          // 结果的原话（截断，不转述）
	OnBehalfOf string `json:"on_behalf_of,omitempty"` // 署名：bot 行使的是它代理对象的投影（SPEC §6）
	Tool       string `json:"tool,omitempty"`         // harness 的工具名（原样，便于回溯）
	Source     string `json:"source,omitempty"`       // 这条从哪来：collect:<harness> | manual
	Ref        string `json:"ref,omitempty"`          // 指回原始记录（会话 id / 行号 / 工具调用 id）
	Detail     string `json:"detail,omitempty"`       // 展开
}

// Time 解析 At。解析不了返回零值 + false（调用方据此判「读不懂」）。
func (r Record) Time() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(r.At))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Doc 是一次读取的结果。
//
// Bad 是**读不懂的行**（逐条列出来，不是静默跳过）：审计少一行，
// 就可能让一次行使看起来从没发生过 —— 宁可报出来让人去看。
type Doc struct {
	Records []Record
	Bad     []string // "<文件名>:<行号>"
}

// Len 方便调用方判空。
func (d Doc) Len() int { return len(d.Records) }

// IDs 给出这批记录里已有的 id 集合（归集重跑去重用）。
func (d Doc) IDs() map[string]bool {
	m := make(map[string]bool, len(d.Records))
	for _, r := range d.Records {
		m[r.ID] = true
	}
	return m
}

// Load 读整个 audit 目录，按时间**升序**返回。
//
// 目录不存在 = 还没有任何行使记录 = 空流水，**不是错误**（刚 init 的机器不该红）。
func Load(vault string) (Doc, error) {
	dir := filepath.Join(vault, DirName)
	names, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Doc{}, nil
		}
		return Doc{}, err
	}
	var doc Doc
	for _, n := range names {
		if n.IsDir() || strings.HasPrefix(n.Name(), ".") || !strings.HasSuffix(n.Name(), ".jsonl") {
			continue
		}
		bad, err := readShard(filepath.Join(dir, n.Name()), n.Name(), &doc)
		doc.Bad = append(doc.Bad, bad...)
		if err != nil {
			return Doc{}, err
		}
	}
	sort.SliceStable(doc.Records, func(i, j int) bool {
		ti, _ := doc.Records[i].Time()
		tj, _ := doc.Records[j].Time()
		return ti.Before(tj)
	})
	return doc, nil
}

// readShard 逐行读一个分片。一行读不懂**不中断**（其余行照读），只记进 bad。
func readShard(path, name string, doc *Doc) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var bad []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			bad = append(bad, fmt.Sprintf("%s:%d", name, ln))
			continue
		}
		if strings.TrimSpace(r.ID) == "" {
			bad = append(bad, fmt.Sprintf("%s:%d（缺 id）", name, ln))
			continue
		}
		doc.Records = append(doc.Records, r)
	}
	return bad, sc.Err()
}

// Append 追加一条。文件 = `<YYYY-MM>.<actor>.jsonl` —— **按行使者分片**：
// 两个归集时段 / 两个人同时写也碰不到同一个文件（并发就地消掉，不靠锁），
// 而且「查某人做过什么」= 打开一个文件。
//
// 返回写到的文件路径，便于调用方回显。
func Append(vault string, r Record) (string, error) {
	if strings.TrimSpace(r.ID) == "" {
		return "", fmt.Errorf("id 不能为空 —— 归集重跑靠它去重，人补记也要能一眼指认")
	}
	if strings.TrimSpace(r.Actor) == "" {
		return "", fmt.Errorf("actor 不能为空 —— 行使的主体是谁，这是审计的第一性问题")
	}
	if strings.TrimSpace(r.Action) == "" {
		return "", fmt.Errorf("action 不能为空（read | write | invoke）")
	}
	if strings.TrimSpace(r.Result) == "" {
		return "", fmt.Errorf("result 不能为空（ok | denied | failed）")
	}
	t, ok := r.Time()
	if !ok {
		return "", fmt.Errorf("at 必须是 RFC3339（例如 2026-10-08T02:41:00+08:00）")
	}
	actor := ShardName(r.Actor)
	if actor == "" {
		return "", fmt.Errorf("actor %q 里没有一个能当文件名的字符 —— 换一个能落成路径的名字", r.Actor)
	}
	dir := filepath.Join(vault, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, t.Format("2006-01")+"."+actor+".jsonl")
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return "", err
	}
	return path, nil
}

// ShardName 把行使者名压成能当**分片文件名**的样子。
//
// 与「成员名」同一把尺子（见 org.MemberNameProblem）：中文 / 空格 / `-` / `.` 一律放行 ——
// 名字是不是 ASCII 不是判据。但这里**清洗**而不是拒绝：审计流水不能因为一个怪字符就记不下来
// （记不下来 = 审计失败），所以把真正会撕开路径的那几种字符换成 `_`。
// 原值不受影响 —— 它照旧留在 Record.Actor 里，文件名只是容器。
func ShardName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r < 0x20 || r == 0x7f: // 控制字符
			b.WriteRune('_')
		case strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), " .")
}

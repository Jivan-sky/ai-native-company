// Package bench —— 「不问人，能不能拿到」的测量器。
//
// 一句话：**把 ANC 的主判据变成一个别人能复跑的数**，而不是我们给自己打的分。
//
// 为什么需要它：SPEC §10 的五问、§9.3 的第三条，打分人一直是我们自己；`anc gate` 验的是
// 「有没有按自己说的留痕」，从不验「说的对不对」（judge/delivery.go 的注释写得很直白：
// 「一线那句话算不算数」要人判）。这个包补的是那半边里**能机械化**的一小块。
//
// 口径（三条，缺一条这个数就不成立）：
//
//  1. **判的是「送到手上没有」，不是「模型聪不聪明」。** 一个任务开工需要什么，写在任务数据里
//     （`needs`）；判据只看 ANC 的装配点（ctxpack.Assemble —— CLI / 看板 / MCP 读出口共用的那一处）
//     有没有把它摆到这个人眼前。模型能力是另一个变量，混进来就说不清是谁的功劳。
//  2. **没送到 = 要问一个人。** 每条没送达的 need 都要给出「该问谁」（拒答指的、跨域目录指的、
//     或者明说「没有指路」）。所以分数可以直接读成「这个任务有几处得靠人」。
//  3. **换个人还得成立。** 同一个任务在 k 个视角（新人 / 同事 / 业务 agent）下都要送到；
//     只有**全部视角全部 need 都送到**才算过（pass^k）。人事一动就断的装配，不算装配。
//
// 这个包**不判**：任务最后有没有做成（那要真跑 agent）、数据新不新鲜、答案对不对。
// 它只回答一个问题：**开工前那一关，人要不要开口。**
package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"anc/internal/ctxpack"
)

// Schema 是任务集与报告的版本号：字段就是契约，改字段就改版本。
const Schema = "anc.bench/v1"

// need 的种类。少一种就算少一条路：拿得到上下文却调不动工具，一样要开口问人。
const (
	KindDomain  = "domain"  // 知道这块业务是什么、数据在哪、找谁
	KindProject = "project" // 知道这件事挂在哪、谁在跟
	KindSkill   = "skill"   // 知道怎么做（且正文真在，不是只登记了名字）
	KindGrant   = "grant"   // 知道被允许做什么（没有 grant 就是不通）
	KindTool    = "tool"    // harness 里真被预授权、真能调
)

// KnownKinds 是认得的 need 种类。拼错要拦：拼错等于「你以为测了其实没测」。
var KnownKinds = []string{KindDomain, KindProject, KindSkill, KindGrant, KindTool}

// Need 是「开工前必须到手的一样东西」。
type Need struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`         // domain/project/skill 填 slug 或名字；grant 填 action[:object]；tool 填工具名
	Why   string `json:"why,omitempty"` // 人写的「为什么这件事离了它就开不了工」
}

// Task 是一个真实工作项在测量里的样子。**任务不由 ANC 定义** —— 它是一份数据，
// 客户 / 第三方可以把他们自己的活填进来（见 README 的 schema 一节）。
type Task struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Origin string   `json:"origin,omitempty"` // external:<出处> | synthetic —— 自造的任务不算外部证据，报告里分开计
	As     []string `json:"as"`               // 视角：成员名 / 业务 agent 的 slug
	Needs  []Need   `json:"needs"`
	// Note 是给读任务集的人看的一句口径（为什么不这样列视角、这条为什么算红线）。
	// 不参与判分 —— 但「为什么这么测」必须写在数据旁边，否则下一个读的人只能猜。
	Note string `json:"note,omitempty"`
}

// Suite 是一份任务集。
type Suite struct {
	Schema string `json:"schema"`
	Note   string `json:"note,omitempty"`
	Tasks  []Task `json:"tasks"`
}

// LoadSuite 读一份任务集：给目录就读目录下全部 *.json（按文件名排序，保证可复跑），
// 给文件就读那一个。两种文件形态都收：`{"tasks":[...]}` 或单个任务对象。
func LoadSuite(path string) (Suite, error) {
	st, err := os.Stat(path)
	if err != nil {
		return Suite{}, fmt.Errorf("任务集读不到：%v", err)
	}
	var files []string
	if st.IsDir() {
		ents, err := os.ReadDir(path)
		if err != nil {
			return Suite{}, err
		}
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				files = append(files, filepath.Join(path, e.Name()))
			}
		}
		sort.Strings(files)
	} else {
		files = []string{path}
	}
	if len(files) == 0 {
		return Suite{}, fmt.Errorf("任务集是空的：%s 下没有 *.json", path)
	}
	out := Suite{Schema: Schema}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return Suite{}, err
		}
		var probe struct {
			Tasks []Task `json:"tasks"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return Suite{}, fmt.Errorf("%s 不是合法 JSON：%v", filepath.Base(f), err)
		}
		if probe.Tasks != nil {
			var s Suite
			if err := json.Unmarshal(raw, &s); err != nil {
				return Suite{}, fmt.Errorf("%s 解析失败：%v", filepath.Base(f), err)
			}
			if s.Note != "" && out.Note == "" {
				out.Note = s.Note
			}
			out.Tasks = append(out.Tasks, s.Tasks...)
			continue
		}
		var t Task
		if err := json.Unmarshal(raw, &t); err != nil {
			return Suite{}, fmt.Errorf("%s 解析失败：%v", filepath.Base(f), err)
		}
		out.Tasks = append(out.Tasks, t)
	}
	if err := out.validate(); err != nil {
		return Suite{}, err
	}
	return out, nil
}

func (s *Suite) validate() error {
	if len(s.Tasks) == 0 {
		return fmt.Errorf("任务集里一个任务都没有")
	}
	seen := map[string]bool{}
	for i, t := range s.Tasks {
		where := fmt.Sprintf("第 %d 个任务", i+1)
		if t.ID == "" {
			return fmt.Errorf("%s 缺 id", where)
		}
		where = "任务 " + t.ID
		if seen[t.ID] {
			return fmt.Errorf("%s 的 id 重复 —— id 是复跑时对账用的，不能重", where)
		}
		seen[t.ID] = true
		if len(t.As) == 0 {
			return fmt.Errorf("%s 没写 as（至少要一个视角）—— 只测一个人，等于没测「换个人还成不成立」", where)
		}
		if len(t.Needs) == 0 {
			return fmt.Errorf("%s 没写 needs —— 没有需要测的东西", where)
		}
		for _, n := range t.Needs {
			if !knownKind(n.Kind) {
				return fmt.Errorf("%s 的 need 种类 %q 不认识（认得的：%s）—— 拼错等于没测",
					where, n.Kind, strings.Join(KnownKinds, " / "))
			}
			if strings.TrimSpace(n.Value) == "" {
				return fmt.Errorf("%s 有一条 %s 没写 value", where, n.Kind)
			}
		}
	}
	return nil
}

func knownKind(k string) bool {
	for _, v := range KnownKinds {
		if v == k {
			return true
		}
	}
	return false
}

// NeedResult 是「一样东西在一个人眼里到没到手」。没到手时 Ask 说清该问谁 —— 那是这个数最有用的一格。
type NeedResult struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	Why       string `json:"why,omitempty"`
	Delivered bool   `json:"delivered"`
	Where     string `json:"where,omitempty"` // 从装配结果的哪一格看出来的（可回去核对）
	Ask       string `json:"ask,omitempty"`   // 没到手：该问谁（空 = 没有指路，这本身是个发现）
}

// View 是一个视角（一个人 / 一个 agent）下的判分。
type View struct {
	Who       string       `json:"who"`
	Name      string       `json:"name,omitempty"`
	Role      string       `json:"role,omitempty"`
	Refused   bool         `json:"refused,omitempty"` // 装配点直接拒了：后面全是没到手
	Delivered int          `json:"delivered"`
	Total     int          `json:"total"`
	Asks      int          `json:"asks"`
	Needs     []NeedResult `json:"needs"`
}

// AllDelivered 是这一视角下的全部 need 都到手（pass^k 里那一个 k）。
func (v View) AllDelivered() bool { return v.Total > 0 && v.Delivered == v.Total }

// Grade 判一个任务在一个视角下「要不要开口」。纯函数：不加载、不落盘、不打印。
func Grade(p ctxpack.Out, tools []string, t Task) View {
	v := View{Who: p.Who, Total: len(t.Needs), Refused: p.Refused != nil}
	if p.Identity != nil {
		v.Name, v.Role = p.Identity.Name, p.Identity.Role
	}
	for _, n := range t.Needs {
		r := NeedResult{Kind: n.Kind, Value: n.Value, Why: n.Why}
		switch n.Kind {
		case KindDomain:
			r.Delivered, r.Where = hasDomain(p, n.Value)
		case KindProject:
			r.Delivered, r.Where = hasProject(p, n.Value)
		case KindSkill:
			r.Delivered, r.Where = hasSkill(p, n.Value)
		case KindGrant:
			r.Delivered, r.Where = hasGrant(p, n.Value)
		case KindTool:
			r.Delivered, r.Where = hasTool(tools, n.Value)
		}
		if r.Delivered {
			v.Delivered++
		} else {
			r.Ask = whoToAsk(p, n)
			v.Asks++
		}
		v.Needs = append(v.Needs, r)
	}
	return v
}

// hasDomain：自己的域整行在，且 data 那一格指得到地方。
// 只有名字没有 data = 知道这块业务存在，不知道资料在哪个目录 —— 还是要问人。
func hasDomain(p ctxpack.Out, slug string) (bool, string) {
	for i, d := range p.Domains {
		if d.Slug != slug {
			continue
		}
		if strings.TrimSpace(d.Data) == "" {
			return false, fmt.Sprintf("domains[%d] 有名字但没有 data（不知道资料在哪个目录）", i)
		}
		return true, fmt.Sprintf("domains[%d].data=%s", i, d.Data)
	}
	return false, ""
}

func hasProject(p ctxpack.Out, slug string) (bool, string) {
	for i, r := range p.Projects {
		if r.Slug == slug {
			return true, fmt.Sprintf("projects[%d]", i)
		}
	}
	return false, ""
}

// hasSkill：登记了**而且正文在**。missing=true 是「知道有这个技能，但拿不到它」—— 照样要问人。
func hasSkill(p ctxpack.Out, name string) (bool, string) {
	for i, s := range p.Skills {
		if s.Name != name {
			continue
		}
		if s.Missing {
			return false, fmt.Sprintf("skills[%d] 声明了但正文不在", i)
		}
		return true, fmt.Sprintf("skills[%d]", i)
	}
	return false, ""
}

// hasGrant：value 是 `action` 或 `action:object`。装配点给出的 grants 已经按主体滤过，
// 所以「在这几行里找得到」就是「覆盖到了」（同一支判据：org.GrantHitsSubject）。
func hasGrant(p ctxpack.Out, value string) (bool, string) {
	action, object, _ := strings.Cut(value, ":")
	if p.Grants == nil {
		return false, ""
	}
	for i, r := range p.Grants.Rows {
		if r.Action != action {
			continue
		}
		if object != "" && r.Object != object {
			continue
		}
		return true, fmt.Sprintf("grants.rows[%d] %s→%s", i, r.Action, r.Object)
	}
	return false, ""
}

// hasTool：角色被预授权了才叫「能调」。dontAsk 下没预授权的工具会被自动拒绝（实测）。
func hasTool(tools []string, want string) (bool, string) {
	for _, t := range tools {
		if strings.EqualFold(strings.TrimSpace(t), strings.TrimSpace(want)) {
			return true, "roles/<role>.allowed_tools"
		}
	}
	return false, ""
}

// whoToAsk：没到手时该找谁。宁可写「没有指路」，也不许编一个人出来。
func whoToAsk(p ctxpack.Out, n Need) string {
	if p.Refused != nil && strings.TrimSpace(p.Refused.Ask) != "" {
		return p.Refused.Ask
	}
	for _, d := range p.Directory {
		if n.Kind == KindDomain && d.Slug == n.Value {
			return d.Who
		}
	}
	return ""
}

// TaskResult 是一个任务在所有视角下的判分。PassK 只有「全部视角全部到手」才为真。
type TaskResult struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Origin string `json:"origin,omitempty"`
	Note   string `json:"note,omitempty"` // 原样带出来：口径要跟着结果走，不然读结果的人只能猜
	Views  []View `json:"views"`
	PassK  bool   `json:"pass_k"`
}

// Report 是一整轮测量。
type Report struct {
	Schema       string       `json:"schema"`
	Vault        string       `json:"vault"`
	Tasks        []TaskResult `json:"tasks"`
	Views        int          `json:"views"`
	Needs        int          `json:"needs"`
	Delivered    int          `json:"delivered"`
	Asks         int          `json:"asks"`
	DeliveryRate float64      `json:"delivery_rate"`
	PassKRate    float64      `json:"pass_k_rate"`
	External     int          `json:"external_tasks"`
	Synthetic    int          `json:"synthetic_tasks"`
}

// Add 把一份任务结果并进报告，顺手维护总数与两个率。
func (r *Report) Add(t TaskResult) {
	r.Tasks = append(r.Tasks, t)
	if strings.HasPrefix(t.Origin, "external:") {
		r.External++
	} else {
		r.Synthetic++
	}
	for _, v := range t.Views {
		r.Views++
		r.Needs += v.Total
		r.Delivered += v.Delivered
		r.Asks += v.Asks
	}
	r.DeliveryRate = ratio(r.Delivered, r.Needs)
	passed := 0
	for _, x := range r.Tasks {
		if x.PassK {
			passed++
		}
	}
	r.PassKRate = ratio(passed, len(r.Tasks))
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// FilterViews 按 --k 截断视角：k<=0 = 全跑。
func FilterViews(as []string, k int) []string {
	if k <= 0 || k >= len(as) {
		return as
	}
	return as[:k]
}

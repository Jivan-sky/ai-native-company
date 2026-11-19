package audit

// 哈希链：让"改历史"留下可验证的痕迹（治理判据 3）。
//
// 之前账本是普通 JSONL —— 谁有写权，谁就能悄悄改一行，而且看不出来。
// 那意味着所有"有痕"的承诺（判据 1/4/7/8）都建在沙子上。
//
// 现在每一行带两个字段：
//
//	prev_hash  上一行的 hash（本分片内）
//	hash       sha256(prev_hash + "\n" + 本条记录去掉这两个字段后的规范 JSON)
//
// 边界（必须写清，不含糊）：
//   - **按分片成链**（一个月 + 一个主体一个文件），不是全局单链 —— 全局单链要全局锁，
//     并发写会互相等；按分片的结果是"改某个人的某个中间行会被发现"，
//     但"整份文件被删或整份替换"这一版**检测不出来**（需要每日 head 签名 + 外部锚定，未做）。
//   - 老数据没有 hash：verify 把它们算作 legacy，**不算失败**（不许倒过来说老账是伪造的）。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// canonical 是参与哈希的字节：把两个链字段清空后序列化。
// 用结构体（不是 map）保证字段顺序稳定 —— 顺序一变，同一份内容会算出不同哈希。
func canonical(r Record) ([]byte, error) {
	c := r
	c.Hash, c.PrevHash = "", ""
	c.Src = ""
	return json.Marshal(c)
}

// ChainHash 算这一行的 hash。
func ChainHash(prev string, r Record) (string, error) {
	b, err := canonical(r)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write([]byte("\n"))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// lastHash 读分片最后一行的 hash。读不到（新文件 / 老数据）返回空串。
func lastHash(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var r Record
		if json.Unmarshal([]byte(line), &r) != nil {
			return ""
		}
		return r.Hash
	}
	return ""
}

// chainLock 是一个可移植的互斥（O_EXCL 重试）。跨平台，不引 syscall。
func chainLock(path string) (func(), error) {
	lock := path + ".lock"
	for i := 0; i < 200; i++ {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lock) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil, fmt.Errorf("链锁等了太久（%s）—— 有人在长时间写同一个分片", lock)
}

// VerifyIssue 是一处对不上的地方。
type VerifyIssue struct {
	File string `json:"file"`
	Line int    `json:"line"`
	ID   string `json:"id,omitempty"`
	Why  string `json:"why"`
}

// VerifyReport 是一次校验的结果。
type VerifyReport struct {
	Files   int           `json:"files"`
	Lines   int           `json:"lines"`
	Chained int           `json:"chained"`
	Legacy  int           `json:"legacy"` // 没有 hash 的老行
	Issues  []VerifyIssue `json:"issues"`
}

// Verify 校验整份账本的链。返回的第一批问题足够定位。
func Verify(vault string) (VerifyReport, error) {
	dir := filepath.Join(vault, DirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return VerifyReport{}, nil
		}
		return VerifyReport{}, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	rep := VerifyReport{}
	for _, name := range names {
		rep.Files++
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			rep.Issues = append(rep.Issues, VerifyIssue{File: name, Why: err.Error()})
			continue
		}
		prev := ""
		for i, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			rep.Lines++
			var r Record
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				rep.Issues = append(rep.Issues, VerifyIssue{File: name, Line: i + 1, Why: "不是合法 JSON：" + err.Error()})
				continue
			}
			if r.Hash == "" {
				rep.Legacy++ // 老账：没有链，不算失败
				prev = ""
				continue
			}
			if r.PrevHash != prev {
				rep.Issues = append(rep.Issues, VerifyIssue{File: name, Line: i + 1, ID: r.ID,
					Why: "prev_hash 与上一行对不上（这一行或它前面那行被改过/删过）"})
			}
			want, err := ChainHash(r.PrevHash, r)
			if err != nil {
				rep.Issues = append(rep.Issues, VerifyIssue{File: name, Line: i + 1, ID: r.ID, Why: err.Error()})
			} else if want != r.Hash {
				rep.Issues = append(rep.Issues, VerifyIssue{File: name, Line: i + 1, ID: r.ID,
					Why: "hash 与内容不符（这一行被改过）"})
			} else {
				rep.Chained++
			}
			prev = r.Hash
		}
	}
	return rep, nil
}

package main

// anc gateway —— 「第二条网关」的收口环。
//
// 它补的是哪一格（2026-10-11 对着 cc-connect origin/main 核过，见 internal/gateway 的包注释）：
// 上游**没有**「卡片事件进来」的公开口（`/send` 只出、`:9111/hook` 是反向下发、
// bridge 的入站 `card_action` 不带 operator）。所以这一条腿长在 ANC 这一侧：
//
//	事件流（一行一条） → 认事件 → 幂等 → 走同一个信号入口落痕 → 收口
//	   stdin                                                    stdout / 配置的命令
//
// 两端都是**可替代**的：事件从 stdin 进（`lark-cli event consume` 那条长连接，
// 或将来飞书官方 SDK 那条腿写的适配器），收口从 stdout 出去交给部署侧的适配器 ——
// 这一层**不碰平台 API**（SPEC §4.7 ① / §7.1.39 口径）。
//
// 一句话交代：Go，零新增依赖 —— 判断与幂等是仓库自己的事，长连接本体不在这条腿里。

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"anc/internal/gateway"
)

const gatewayUsage = `anc gateway —— 第二条网关（上游没有的那些口，长在 ANC 这一侧）

用法：
  anc gateway relay <vault> [--app-id <id>] [--patch print|off|cmd:<命令>]
                            [--done-frame <帧文件>] [--dedupe-capacity N] [--dedupe-ttl D] [--quiet]

relay 吃**一行一条**的事件流（飞书 card.action.trigger 的真事件，从 stdin 进）：
  1 认事件    宽进严出：真事件两种形状都收，但只认 anc_id / anc_signal 两个键
  2 幂等      同一件事递两次只算一次（键优先 event_id，没有就退到「哪条消息 + 谁点的 + 点的什么」）
  3 落痕      走与 CLI / 看板**同一个信号入口**（settleCardEvent）—— 判据只有那一份
  4 收口      拿终态卡按同一条 message_id 覆盖

--patch 说收口那一步怎么走（ANC 自己**不连**平台 API）：
  print        把「该盖什么」按 NDJSON 打到 stdout，交给部署侧的适配器（**默认**）
  off          只落痕不收口（调用方自己就是应答方时用得上，比如上游那个 hook）
  cmd:<命令>   跑一条配置里的命令去收；模板里可用 {message_id} {chat_id} {vault} {card_file}

日志走 stderr，一行一条。退出码：事件流读断、或中途出过 error，都返回 1 ——
让外面那个监管进程看得见（悄悄丢一次点击是最坏的结果）。

例：
  lark-cli event consume card.action.trigger --as bot | anc gateway relay ./vault --app-id cli_xxx
`

func cmdGateway(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, gatewayUsage)
		return 2
	}
	switch args[0] {
	case "relay":
		return cmdGatewayRelay(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "错误：不认识子命令 %q\n\n", args[0])
		fmt.Fprint(os.Stderr, gatewayUsage)
		return 2
	}
}

// relaySink 把 relay 那两步接到现实上：落痕走 settleCardEvent（与 CLI 同一份），
// 收口按 --patch 走（print / cmd:）。收口失败**不回退**已经落的痕 —— 那是 relay 的口径。
type relaySink struct {
	vault     string
	appID     string
	doneFrame string
	h         *hotOpts
	patch     string
	cardOut   io.Writer
}

func (s *relaySink) Settle(raw []byte) (*gateway.Settle, error) {
	done, err := settleCardEvent(s.vault, raw, s.appID, time.Now(), s.h, s.doneFrame)
	if err != nil {
		return nil, err
	}
	st := &gateway.Settle{
		Proposal:  done.Pending.ID,
		Signal:    string(done.Decision.Signal),
		By:        done.Decision.By,
		Via:       done.Decision.Via,
		At:        done.Receipt.At,
		ChatID:    done.Click.ChatID,
		MessageID: done.Click.MessageID,
		Timeline:  done.Receipt.Timeline,
		Audit:     done.Receipt.Audit,
	}
	if b, jerr := done.DoneCard.FeishuJSON(); jerr == nil {
		st.DoneCard = b
	}
	for _, n := range done.DoneNotes {
		st.Notes = append(st.Notes, n.Where+"："+n.What)
	}
	return st, nil
}

// patchLine 是交给部署侧适配器的**一份收口指令**。它是数据（NDJSON 一行），不是命令 ——
// 谁去盖、用什么盖（lark-cli / 官方 SDK / 上游那个 hook）不归这一层管。
type patchLine struct {
	Schema    string          `json:"schema"`
	MessageID string          `json:"message_id"`
	ChatID    string          `json:"chat_id"`
	Proposal  string          `json:"proposal"`
	By        string          `json:"by"`
	Card      json.RawMessage `json:"card"`
}

func (s *relaySink) Patch(st *gateway.Settle) error {
	switch {
	case s.patch == "print":
		b, err := json.Marshal(patchLine{
			Schema: "anc.gateway.patch/v1", MessageID: st.MessageID, ChatID: st.ChatID,
			Proposal: st.Proposal, By: st.By, Card: json.RawMessage(st.DoneCard),
		})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(s.cardOut, "%s\n", b)
		return err
	case strings.HasPrefix(s.patch, "cmd:"):
		return runPatchCmd(strings.TrimSpace(strings.TrimPrefix(s.patch, "cmd:")), st, s.vault)
	default:
		return fmt.Errorf("认不出的收口方式 %q —— 只认 print / cmd:<命令>（off 那档不会走到这儿）", s.patch)
	}
}

// runPatchCmd 跑配置里给的收口命令。
//
// 不经过 shell（按空白切成 argv 直接 exec）—— 配置里那一串是**部署侧写死的模板**，
// 不是从事件里来的字符串；事件里的值只出现在 {message_id} / {chat_id} 这两格，
// 单独成参数传过去，拼不进别的参数里去。
func runPatchCmd(tpl string, st *gateway.Settle, vault string) error {
	argv := strings.Fields(tpl)
	if len(argv) == 0 {
		return errors.New("cmd: 后面没跟命令")
	}
	vals := map[string]string{
		"{message_id}": st.MessageID,
		"{chat_id}":    st.ChatID,
		"{vault}":      vault,
	}
	if strings.Contains(tpl, "{card_file}") {
		f, err := os.CreateTemp("", "anc-done-card-*.json")
		if err != nil {
			return err
		}
		name := f.Name()
		defer os.Remove(name)
		if _, err := f.Write(st.DoneCard); err != nil {
			f.Close()
			return err
		}
		f.Close()
		vals["{card_file}"] = name
	}
	for i := range argv {
		for k, v := range vals {
			argv[i] = strings.ReplaceAll(argv[i], k, v)
		}
	}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s 没成：%v（%s）", argv[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

func cmdGatewayRelay(args []string) int {
	fs := flag.NewFlagSet("gateway relay", flag.ContinueOnError)
	h := addHotOpts(fs)
	appID := fs.String("app-id", "", "事件落在哪个 app（org 那一侧靠它定位是哪台 bot、属于谁）")
	patch := fs.String("patch", "print", "收口怎么走：print | off | cmd:<命令模板>")
	doneFrame := fs.String("done-frame", "", "收口那一帧：名字（approval-done）或文件；不给就用出厂那份")
	capacity := fs.Int("dedupe-capacity", 0, "幂等表最多记多少条（0 = 出厂 4096）")
	ttl := fs.Duration("dedupe-ttl", 0, "幂等表每条保鲜多久（0 = 出厂 30m）")
	quiet := fs.Bool("quiet", false, "不往 stderr 打那一行日志")
	flagArgs, pos := splitArgs(args, map[string]bool{"quiet": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(pos) < 1 {
		fmt.Fprintln(os.Stderr, "错误：用法 anc gateway relay <vault> [--app-id <id>] [--patch print|off|cmd:<命令>]")
		return 2
	}
	vault, err := filepath.Abs(pos[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}
	patchMode := strings.TrimSpace(*patch)
	if patchMode == "" {
		patchMode = "print"
	}
	switch {
	case patchMode == "print" || patchMode == "off" || strings.HasPrefix(patchMode, "cmd:"):
	default:
		fmt.Fprintf(os.Stderr, "错误：认不出的 --patch %q —— 只认 print / off / cmd:<命令>\n", patchMode)
		return 2
	}
	sink := &relaySink{
		vault: vault, appID: *appID, doneFrame: *doneFrame, h: h,
		patch: patchMode, cardOut: os.Stdout,
	}
	r, err := gateway.New(gateway.Config{
		NoPatch:  patchMode == "off",
		Capacity: *capacity,
		TTL:      *ttl,
	}, sink)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 1
	}

	sc := bufio.NewScanner(os.Stdin)
	// 一行可能挺长（真事件里带着整张卡的 card_content），把缓冲放宽。
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	n := map[string]int{}
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		o := r.Handle(line)
		n[o.Kind]++
		if !*quiet {
			fmt.Fprintln(os.Stderr, o.Line(time.Now()))
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "错误：事件流读断了：%v\n", err)
		return 1
	}
	if !*quiet {
		fmt.Fprintf(os.Stderr, "说到底：一共 %d 条 —— settled %d · duplicate %d · rejected %d · ignored %d · error %d\n",
			n[gateway.KindSettled]+n[gateway.KindDuplicate]+n[gateway.KindRejected]+n[gateway.KindIgnored]+n[gateway.KindError],
			n[gateway.KindSettled], n[gateway.KindDuplicate], n[gateway.KindRejected], n[gateway.KindIgnored], n[gateway.KindError])
	}
	if n[gateway.KindError] > 0 {
		return 1
	}
	return 0
}

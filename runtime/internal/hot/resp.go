package hot

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// 这是「够用就停」的 RESP2 客户端。
//
// 为什么手写：交付物是单 exe、CGO_ENABLED=0 交叉编译、go.mod 零第三方依赖；
// 引一个完整客户端（go-redis 之类）会带进一棵依赖树、二进制大好几兆，
// 而我们**只用得到十几条命令**。换回标准客户端只动这一个文件。
//
// 支持：状态码 +、错误 -、整数 :、批量串 $、数组 *（含 nil）。
// 不支持：内联命令、RESP3 的 map / set / push —— 用不到，不写。
type respConn struct {
	rw      io.ReadWriteCloser
	r       *bufio.Reader
	w       *bufio.Writer
	timeout time.Duration
}

// deadlineSetter 让测试里的假连接也能被复用：net.Conn 有 SetDeadline，别的没有就算了。
type deadlineSetter interface{ SetDeadline(time.Time) error }

func dialResp(addr string, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return net.DialTimeout("tcp", addr, timeout)
}

func newRespConn(rw io.ReadWriteCloser, timeout time.Duration) *respConn {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &respConn{rw: rw, r: bufio.NewReader(rw), w: bufio.NewWriter(rw), timeout: timeout}
}

func (rc *respConn) Close() error { return rc.rw.Close() }

// do 发一条命令、读一个回复。每次调用都重设超时：一条卡住的命令不该把进程挂死。
func (rc *respConn) do(args ...string) (any, error) {
	if d, ok := rc.rw.(deadlineSetter); ok {
		_ = d.SetDeadline(time.Now().Add(rc.timeout))
	}
	if err := rc.writeCmd(args); err != nil {
		return nil, err
	}
	return rc.readReply()
}

func (rc *respConn) writeCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("hot: 空命令")
	}
	if _, err := rc.w.WriteString("*" + strconv.Itoa(len(args)) + "\r\n"); err != nil {
		return err
	}
	for _, a := range args {
		if _, err := rc.w.WriteString("$" + strconv.Itoa(len(a)) + "\r\n" + a + "\r\n"); err != nil {
			return err
		}
	}
	return rc.w.Flush()
}

func (rc *respConn) readReply() (any, error) {
	line, err := rc.readLine()
	if err != nil {
		return nil, err
	}
	if line == "" {
		return nil, errors.New("hot: 读到一个空回复")
	}
	tag, rest := line[0], line[1:]
	switch tag {
	case '+':
		return rest, nil
	case '-':
		return nil, fmt.Errorf("hot: 热层回了一个错：%s", rest)
	case ':':
		n, perr := strconv.ParseInt(rest, 10, 64)
		if perr != nil {
			return nil, fmt.Errorf("hot: 整数回复读不懂（%q）", rest)
		}
		return n, nil
	case '$':
		return rc.readBulk(rest)
	case '*':
		return rc.readArray(rest)
	}
	return nil, fmt.Errorf("hot: 认不出的回复类型（%q）", line)
}

func (rc *respConn) readBulk(n string) (any, error) {
	size, err := strconv.Atoi(n)
	if err != nil {
		return nil, fmt.Errorf("hot: 批量串长度读不懂（%q）", n)
	}
	if size < 0 {
		return nil, nil // nil bulk = 没有这个值
	}
	buf := make([]byte, size+2) // 带上结尾的 \r\n
	if _, err := io.ReadFull(rc.r, buf); err != nil {
		return nil, err
	}
	return string(buf[:size]), nil
}

func (rc *respConn) readArray(n string) (any, error) {
	count, err := strconv.Atoi(n)
	if err != nil {
		return nil, fmt.Errorf("hot: 数组长度读不懂（%q）", n)
	}
	if count < 0 {
		return nil, nil
	}
	out := make([]any, 0, count)
	for i := 0; i < count; i++ {
		item, err := rc.readReply()
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (rc *respConn) readLine() (string, error) {
	line, err := rc.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

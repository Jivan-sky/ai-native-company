// Package mysqlproto —— 最小 MySQL 线协议客户端（零依赖）。
//
// 只做：握手 + 认证（mysql_native_password；caching_sha2 的 fast path）+ 简单查询。
// 不做：TLS、RSA 完整认证、预处理语句、多结果集。
//
// 三条口径：
//  1. **不支持就明说**：caching_sha2 需要完整认证时直接报错（需要 TLS/RSA），不假装成功。
//  2. **只读交给上层**：本包只负责「把一条 SQL 发出去、把结果读回来」，守卫在 ds 那层。
//  3. **字节级可测**：假服务端按包边界回放，错误路径（ERR 包）与结果集都确定性验。
package mysqlproto

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

// DSN 是最小连接信息。
type DSN struct{ Host, Port, User, Password, Database string }

func ParseDSN(raw string) (DSN, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return DSN{}, fmt.Errorf("连接串读不懂：%v", err)
	}
	if u.Scheme != "mysql" {
		return DSN{}, fmt.Errorf("只认 mysql:// 连接串")
	}
	d := DSN{Host: u.Hostname(), Port: u.Port(), Database: strings.TrimPrefix(u.Path, "/")}
	if d.Port == "" {
		d.Port = "3306"
	}
	if u.User != nil {
		d.User = u.User.Username()
		d.Password, _ = u.User.Password()
	}
	if d.Host == "" {
		return DSN{}, fmt.Errorf("连接串缺 host")
	}
	return d, nil
}

type Conn struct {
	nc     net.Conn
	br     *bufio.Reader
	seq    uint8
	server string
}

func (c *Conn) Close() error {
	if c.nc == nil {
		return nil
	}
	c.seq = 0
	_ = c.writePacket([]byte{0x01}) // COM_QUIT
	return c.nc.Close()
}

const (
	capProtocol41    = 0x00000200
	capSecureConn    = 0x00008000
	capPluginAuth    = 0x00080000
	capPluginAuthLen = 0x00200000
	capConnectWithDB = 0x00000008
	capDeprecateEOF  = 0x01000000
)

func Dial(ctx context.Context, d DSN, timeout time.Duration) (*Conn, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	nc, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(d.Host, d.Port))
	if err != nil {
		return nil, fmt.Errorf("连不上 %s:%s：%v", d.Host, d.Port, err)
	}
	c := &Conn{nc: nc, br: bufio.NewReader(nc), seq: 0}
	if err := c.handshake(d); err != nil {
		_ = nc.Close()
		return nil, err
	}
	return c, nil
}

// handshake 读服务端问候，回一个 native password 的握手响应。
func (c *Conn) handshake(d DSN) error {
	payload, _, err := c.readPacket()
	if err != nil {
		return err
	}
	if len(payload) < 1 {
		return fmt.Errorf("握手包为空")
	}
	if payload[0] == 0xff {
		return parseErr(payload)
	}
	if payload[0] != 10 {
		return fmt.Errorf("不认识的协议版本：%d", payload[0])
	}
	p := payload[1:]
	nul := strings.IndexByte(string(p), 0)
	if nul < 0 {
		return fmt.Errorf("握手包缺 server version")
	}
	c.server = string(p[:nul])
	p = p[nul+1:]
	if len(p) < 4+8+1+2 {
		return fmt.Errorf("握手包太短")
	}
	p = p[4:] // connection id
	scramble := make([]byte, 0, 20)
	scramble = append(scramble, p[:8]...)
	p = p[8:]
	p = p[1:] // filler
	caps := uint32(binary.LittleEndian.Uint16(p[:2]))
	p = p[2:]
	if len(p) < 1+2+2+1+10 {
		return fmt.Errorf("握手包缺能力位")
	}
	p = p[1:] // charset
	p = p[2:] // status
	caps |= uint32(binary.LittleEndian.Uint16(p[:2])) << 16
	p = p[2:]
	authLen := int(p[0])
	p = p[1:]
	p = p[10:] // reserved
	// auth-plugin-data-part-2
	want := 13
	if authLen > 8 && authLen-8 > 13 {
		want = authLen - 8
	}
	if len(p) >= want {
		scramble = append(scramble, p[:want-1]...) // 去掉结尾的 0
		p = p[want:]
	}
	plugin := "mysql_native_password"
	if i := strings.IndexByte(string(p), 0); i > 0 {
		plugin = string(p[:i])
	}
	if !strings.Contains(plugin, "native") {
		return fmt.Errorf("服务端要求认证插件 %q：本版本只支持 mysql_native_password"+
			"（caching_sha2 的完整认证要 TLS 或 RSA 交换）—— 把账号改成 mysql_native_password 再连", plugin)
	}

	// 握手响应
	flags := uint32(capProtocol41 | capSecureConn | capPluginAuth | capPluginAuthLen)
	if d.Database != "" {
		flags |= capConnectWithDB
	}
	authResp := nativeAuth(d.Password, scramble)

	var body []byte
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], flags)
	body = append(body, b[:]...)
	binary.LittleEndian.PutUint32(b[:], 16<<20)
	body = append(body, b[:]...)
	body = append(body, 45) // utf8mb4
	body = append(body, make([]byte, 23)...)
	body = append(body, []byte(d.User)...)
	body = append(body, 0)
	body = append(body, byte(len(authResp)))
	body = append(body, authResp...)
	if d.Database != "" {
		body = append(body, []byte(d.Database)...)
		body = append(body, 0)
	}
	body = append(body, []byte(plugin)...)
	body = append(body, 0)
	if err := c.writePacket(body); err != nil {
		return err
	}
	// 读认证结果（可能有 AuthSwitch / AuthMoreData）
	for i := 0; i < 4; i++ {
		resp, _, err := c.readPacket()
		if err != nil {
			return err
		}
		switch resp[0] {
		case 0x00:
			return nil
		case 0xff:
			return parseErr(resp)
		case 0xfe: // AuthSwitchRequest
			name, data := parseAuthSwitch(resp)
			if !strings.Contains(name, "native") {
				return fmt.Errorf("服务端要求切换认证方式 %q（本客户端只支持 mysql_native_password）", name)
			}
			if err := c.writePacket(nativeAuth(d.Password, data)); err != nil {
				return err
			}
		case 0x01: // AuthMoreData（caching_sha2）
			if len(resp) > 1 && resp[1] == 0x03 {
				continue // fast auth 成功，继续读 OK
			}
			if len(resp) > 1 && resp[1] == 0x04 {
				return fmt.Errorf("caching_sha2_password 需要完整认证（要 TLS 或 RSA 公钥交换）—— 本版本不支持；请把账号改成 mysql_native_password")
			}
			return fmt.Errorf("caching_sha2_password：未知的 AuthMoreData 0x%02x", resp[1])
		default:
			return fmt.Errorf("认证阶段收到未知包 0x%02x", resp[0])
		}
	}
	return fmt.Errorf("认证没有收敛（服务端一直要更多数据）")
}

// Query 发一条 COM_QUERY，读回列与行。
func (c *Conn) Query(ctx context.Context, sql string) ([]string, [][]*string, error) {
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	default:
	}
	c.seq = 0 // 每个命令的序号从 0 开始
	payload := append([]byte{0x03}, []byte(sql)...)
	if err := c.writePacket(payload); err != nil {
		return nil, nil, err
	}
	return c.readResult()
}

func (c *Conn) Exec(ctx context.Context, sql string) error {
	_, _, err := c.Query(ctx, sql)
	return err
}

func (c *Conn) readResult() ([]string, [][]*string, error) {
	first, _, err := c.readPacket()
	if err != nil {
		return nil, nil, err
	}
	switch first[0] {
	case 0xff:
		return nil, nil, parseErr(first)
	case 0x00: // OK（无结果集）
		return nil, nil, nil
	}
	// 列数（lenenc 整数）
	n, _, ok := readLenEncInt(first, 0)
	if !ok {
		return nil, nil, fmt.Errorf("结果集列数读不懂")
	}
	cols := make([]string, 0, n)
	for i := 0; i < int(n); i++ {
		pkt, _, err := c.readPacket()
		if err != nil {
			return nil, nil, err
		}
		name, err := parseColumnName(pkt)
		if err != nil {
			return nil, nil, err
		}
		cols = append(cols, name)
	}
	// 列定义之后的 EOF（0xfe）
	if pkt, _, err := c.readPacket(); err != nil {
		return nil, nil, err
	} else if len(pkt) > 0 && pkt[0] == 0xff {
		return nil, nil, parseErr(pkt)
	}
	var rows [][]*string
	for {
		pkt, _, err := c.readPacket()
		if err != nil {
			return nil, nil, err
		}
		if len(pkt) == 0 {
			continue
		}
		if pkt[0] == 0xff {
			return nil, nil, parseErr(pkt)
		}
		// EOF：0xfe 且长度 < 9
		if pkt[0] == 0xfe && len(pkt) < 9 {
			break
		}
		row, err := parseRow(pkt, int(n))
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, row)
	}
	return cols, rows, nil
}

func (c *Conn) readPacket() ([]byte, uint8, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
		return nil, 0, err
	}
	n := int(hdr[0]) | int(hdr[1])<<8 | int(hdr[2])<<16
	seq := hdr[3]
	if n < 0 || n > 64<<20 {
		return nil, seq, fmt.Errorf("包长度异常：%d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return nil, seq, err
	}
	c.seq = seq + 1 // 下一个包该用的序号
	return buf, seq, nil
}

func (c *Conn) writePacket(payload []byte) error {
	hdr := []byte{byte(len(payload)), byte(len(payload) >> 8), byte(len(payload) >> 16), c.seq}
	if _, err := c.nc.Write(hdr); err != nil {
		return err
	}
	c.seq++
	_, err := c.nc.Write(payload)
	return err
}

func nativeAuth(password string, scramble []byte) []byte {
	if password == "" {
		return nil
	}
	h1 := sha1.Sum([]byte(password))
	h2 := sha1.Sum(h1[:])
	h := sha1.New()
	h.Write(scramble)
	h.Write(h2[:])
	h3 := h.Sum(nil)
	out := make([]byte, len(h1))
	for i := range h1 {
		out[i] = h1[i] ^ h3[i]
	}
	return out
}

func parseAuthSwitch(pkt []byte) (string, []byte) {
	p := pkt[1:]
	i := strings.IndexByte(string(p), 0)
	if i < 0 {
		return "", nil
	}
	name := string(p[:i])
	data := append([]byte{}, p[i+1:]...)
	if len(data) > 0 && data[len(data)-1] == 0 {
		data = data[:len(data)-1]
	}
	return name, data
}

func parseColumnName(pkt []byte) (string, error) {
	// catalog, schema, table, org_table, name, ...
	_, off, ok := readLenEnc(pkt, 0)
	if !ok {
		return "", fmt.Errorf("列定义读不懂")
	}
	for i := 0; i < 3; i++ { // catalog / schema / table
		_, off, ok = readLenEnc(pkt, off)
		if !ok {
			return "", fmt.Errorf("列定义读不懂")
		}
	}
	_, off, ok = readLenEnc(pkt, off) // org_table
	if !ok {
		return "", fmt.Errorf("列定义读不懂")
	}
	name, _, ok := readLenEnc(pkt, off)
	if !ok {
		return "", fmt.Errorf("列名读不懂")
	}
	return name, nil
}

func parseRow(pkt []byte, want int) ([]*string, error) {
	row := make([]*string, 0, want)
	off := 0
	for i := 0; i < want; i++ {
		if off >= len(pkt) {
			return nil, fmt.Errorf("行比列少（%d/%d）", i, want)
		}
		if pkt[off] == 0xfb {
			row = append(row, nil)
			off++
			continue
		}
		s, next, ok := readLenEnc(pkt, off)
		if !ok {
			return nil, fmt.Errorf("第 %d 列读不懂", i+1)
		}
		v := s
		row = append(row, &v)
		off = next
	}
	return row, nil
}

func readLenEnc(b []byte, off int) (string, int, bool) {
	if off >= len(b) {
		return "", off, false
	}
	first := b[off]
	switch {
	case first < 0xfb:
		n := int(first)
		if off+1+n > len(b) {
			return "", off, false
		}
		return string(b[off+1 : off+1+n]), off + 1 + n, true
	case first == 0xfc:
		if off+3 > len(b) {
			return "", off, false
		}
		n := int(binary.LittleEndian.Uint16(b[off+1:]))
		if off+3+n > len(b) {
			return "", off, false
		}
		return string(b[off+3 : off+3+n]), off + 3 + n, true
	case first == 0xfd:
		if off+4 > len(b) {
			return "", off, false
		}
		n := int(b[off+1]) | int(b[off+2])<<8 | int(b[off+3])<<16
		if off+4+n > len(b) {
			return "", off, false
		}
		return string(b[off+4 : off+4+n]), off + 4 + n, true
	default:
		return "", off, false
	}
}

func parseErr(pkt []byte) error {
	if len(pkt) < 3 {
		return fmt.Errorf("MySQL 错误包太短")
	}
	code := binary.LittleEndian.Uint16(pkt[1:3])
	msg := pkt[3:]
	if len(msg) > 6 && msg[0] == '#' {
		msg = msg[6:]
	}
	return fmt.Errorf("MySQL 错误 %d：%s", code, string(msg))
}

// readLenEncInt 读长度编码的整数（列数这类）。
func readLenEncInt(b []byte, off int) (int, int, bool) {
	if off >= len(b) {
		return 0, off, false
	}
	first := b[off]
	switch {
	case first < 0xfb:
		return int(first), off + 1, true
	case first == 0xfc:
		if off+3 > len(b) {
			return 0, off, false
		}
		return int(binary.LittleEndian.Uint16(b[off+1:])), off + 3, true
	case first == 0xfd:
		if off+4 > len(b) {
			return 0, off, false
		}
		return int(b[off+1]) | int(b[off+2])<<8 | int(b[off+3])<<16, off + 4, true
	default:
		return 0, off, false
	}
}

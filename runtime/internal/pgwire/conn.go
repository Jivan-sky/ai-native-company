// Package pgwire —— 最小 PostgreSQL 线协议客户端（零依赖）。
//
// 只做我们要的那一件事：**跑一条只读查询，把列名与行取回来**。
// 不做连接池、不做 COPY、不做扩展协议（$1 参数）—— 那三样都要更多代码与更多坑，
// 而数据中台的第一根柱子只需要「连得上、查得动」。
//
// 支持认证：trust / cleartext / md5 / SCRAM-SHA-256（PG 14+ 默认）。
// 不支持 TLS：`sslmode=disable` 以外的连接串一律拒绝（不假装加密）。
package pgwire

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

// DSN 是最小的连接信息。
type DSN struct {
	Host, Port, User, Password, Database string
}

// ParseDSN 只接受 postgres:// 或 postgresql://，且 sslmode 必须是 disable。
func ParseDSN(raw string) (DSN, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return DSN{}, fmt.Errorf("连接串读不懂：%v", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return DSN{}, fmt.Errorf("只认 postgres:// 连接串")
	}
	if ssl := u.Query().Get("sslmode"); ssl != "" && ssl != "disable" {
		return DSN{}, fmt.Errorf("sslmode=%q 不支持：本版本不实现 TLS，别假装加密", ssl)
	}
	d := DSN{Host: u.Hostname(), Port: u.Port(), Database: strings.TrimPrefix(u.Path, "/")}
	if d.Port == "" {
		d.Port = "5432"
	}
	if u.User != nil {
		d.User = u.User.Username()
		d.Password, _ = u.User.Password()
	}
	if d.Host == "" || d.Database == "" {
		return DSN{}, fmt.Errorf("连接串缺 host 或 dbname")
	}
	return d, nil
}

// Conn 是一条已认证的连接。一次用完就关（不池化 — 第一版要的是可解释）。
type Conn struct {
	nc net.Conn
	br *bufio.Reader
}

func (c *Conn) Close() error {
	if c.nc == nil {
		return nil
	}
	_ = c.send('X', nil)
	return c.nc.Close()
}

// Dial 连接并完成认证。
func Dial(ctx context.Context, d DSN, timeout time.Duration) (*Conn, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	nc, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(d.Host, d.Port))
	if err != nil {
		return nil, fmt.Errorf("连不上 %s:%s：%v", d.Host, d.Port, err)
	}
	c := &Conn{nc: nc, br: bufio.NewReader(nc)}
	if err := c.startup(d); err != nil {
		_ = nc.Close()
		return nil, err
	}
	return c, nil
}

func (c *Conn) startup(d DSN) error {
	var b strings.Builder
	b.WriteString("user\x00" + d.User + "\x00")
	b.WriteString("database\x00" + d.Database + "\x00")
	b.WriteString("client_encoding\x00UTF8\x00")
	b.WriteString("\x00")
	payload := append([]byte{0, 3, 0, 0}, []byte(b.String())...)
	if err := binary.Write(c.nc, binary.BigEndian, int32(len(payload)+4)); err != nil {
		return err
	}
	if _, err := c.nc.Write(payload); err != nil {
		return err
	}
	return c.auth(d)
}

func (c *Conn) auth(d DSN) error {
	for {
		typ, body, err := c.readMsg()
		if err != nil {
			return err
		}
		switch typ {
		case 'R':
			code := binary.BigEndian.Uint32(body[:4])
			switch code {
			case 0:
				// AuthenticationOk
			case 3:
				if err := c.sendPassword(d.Password + "\x00"); err != nil {
					return err
				}
			case 5:
				salt := body[4:8]
				inner := md5.Sum([]byte(d.Password + d.User))
				outer := md5.Sum(append([]byte(hex.EncodeToString(inner[:])), salt...))
				if err := c.sendPassword("md5" + hex.EncodeToString(outer[:]) + "\x00"); err != nil {
					return err
				}
			case 10:
				if err := c.scram(d, body[4:]); err != nil {
					return err
				}
			default:
				return fmt.Errorf("不支持的认证方式：code=%d", code)
			}
		case 'E':
			return pgError(body)
		case 'Z':
			return nil
		case 'S', 'K', 'N':
			// ParameterStatus / BackendKeyData / NoticeResponse：忽略
		default:
			return fmt.Errorf("认证阶段收到未知消息 %q", string(typ))
		}
	}
}

func (c *Conn) sendPassword(s string) error { return c.send('p', []byte(s)) }

// scram 走 SCRAM-SHA-256（RFC 5802 的最小实现，无 channel binding）。
func (c *Conn) scram(d DSN, mechs []byte) error {
	if !strings.Contains(string(mechs), "SCRAM-SHA-256") {
		return fmt.Errorf("服务端只提供 %q 认证，本客户端只支持 SCRAM-SHA-256", strings.TrimSpace(string(mechs)))
	}
	var nonceRaw [18]byte
	if _, err := rand.Read(nonceRaw[:]); err != nil {
		return err
	}
	clientNonce := base64.StdEncoding.EncodeToString(nonceRaw[:])
	firstBare := "n=" + d.User + ",r=" + clientNonce
	first := "n,," + firstBare
	// SASLInitialResponse: mechanism\0 + int32 len + payload
	var msg []byte
	msg = append(msg, []byte("SCRAM-SHA-256\x00")...)
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(first)))
	msg = append(msg, l[:]...)
	msg = append(msg, []byte(first)...)
	if err := c.send('p', msg); err != nil {
		return err
	}

	typ, body, err := c.readMsg()
	if err != nil {
		return err
	}
	if typ == 'E' {
		return pgError(body)
	}
	if typ != 'R' || binary.BigEndian.Uint32(body[:4]) != 11 {
		return fmt.Errorf("SCRAM：期望 server-first，收到 %q", string(typ))
	}
	serverFirst := string(body[4:])
	fields := parseAttrs(serverFirst)
	salt, err := base64.StdEncoding.DecodeString(fields["s"])
	if err != nil {
		return fmt.Errorf("SCRAM：salt 读不懂")
	}
	iter := 0
	if _, err := fmt.Sscanf(fields["i"], "%d", &iter); err != nil || iter <= 0 {
		return fmt.Errorf("SCRAM：迭代次数读不懂")
	}
	salted := pbkdf2SHA256([]byte(d.Password), salt, iter, 32)
	clientKey := hmacSHA256(salted, []byte("Client Key"))
	storedKey := sha256.Sum256(clientKey)
	withoutProof := "c=biws,r=" + fields["r"]
	authMessage := firstBare + "," + serverFirst + "," + withoutProof
	clientSig := hmacSHA256(storedKey[:], []byte(authMessage))
	proof := make([]byte, len(clientKey))
	for i := range clientKey {
		proof[i] = clientKey[i] ^ clientSig[i]
	}
	final := withoutProof + ",p=" + base64.StdEncoding.EncodeToString(proof)
	if err := c.send('p', []byte(final)); err != nil {
		return err
	}
	typ, body, err = c.readMsg()
	if err != nil {
		return err
	}
	if typ == 'E' {
		return pgError(body)
	}
	if typ != 'R' || binary.BigEndian.Uint32(body[:4]) != 12 {
		return fmt.Errorf("SCRAM：期望 server-final，收到 %q", string(typ))
	}
	attrs := parseAttrs(string(body[4:]))
	if v, ok := attrs["v"]; ok {
		serverKey := hmacSHA256(salted, []byte("Server Key"))
		expect := base64.StdEncoding.EncodeToString(hmacSHA256(serverKey, []byte(authMessage)))
		if v != expect {
			return fmt.Errorf("SCRAM：服务端签名不对（可能中间人）")
		}
	}
	return nil
}

// Query 跑一条**简单查询**并读回结果。多语句会被服务端执行——所以调用方必须先过守卫。
func (c *Conn) Query(ctx context.Context, sql string) ([]string, [][]*string, error) {
	if err := c.send('Q', []byte(sql+"\x00")); err != nil {
		return nil, nil, err
	}
	var cols []string
	var rows [][]*string
	for {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}
		typ, body, err := c.readMsg()
		if err != nil {
			return nil, nil, err
		}
		switch typ {
		case 'T':
			cols = parseRowDescription(body)
		case 'D':
			rows = append(rows, parseDataRow(body))
		case 'C', 'I', 'S', 'N':
			// CommandComplete / EmptyQuery / ParameterStatus / Notice
		case 'E':
			return nil, nil, pgError(body)
		case 'Z':
			return cols, rows, nil
		default:
			return nil, nil, fmt.Errorf("查询阶段收到未知消息 %q", string(typ))
		}
	}
}

// Exec 跑一条不取结果的语句（用于 SET ...）。
func (c *Conn) Exec(ctx context.Context, sql string) error {
	_, _, err := c.Query(ctx, sql)
	return err
}

func (c *Conn) send(typ byte, payload []byte) error {
	hdr := make([]byte, 5)
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)+4))
	if _, err := c.nc.Write(hdr); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := c.nc.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func (c *Conn) readMsg() (byte, []byte, error) {
	typ, err := c.br.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	var l [4]byte
	if _, err := io.ReadFull(c.br, l[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(l[:])
	if n < 4 || n > 64<<20 {
		return 0, nil, fmt.Errorf("消息长度异常：%d", n)
	}
	body := make([]byte, n-4)
	if _, err := io.ReadFull(c.br, body); err != nil {
		return 0, nil, err
	}
	return typ, body, nil
}

func parseRowDescription(b []byte) []string {
	if len(b) < 2 {
		return nil
	}
	n := int(binary.BigEndian.Uint16(b[:2]))
	b = b[2:]
	cols := make([]string, 0, n)
	for i := 0; i < n && len(b) > 0; i++ {
		end := indexByte(b, 0)
		if end < 0 {
			break
		}
		cols = append(cols, string(b[:end]))
		// 跳过 name\0 + 18 字节固定字段
		b = b[end+1:]
		if len(b) < 18 {
			break
		}
		b = b[18:]
	}
	return cols
}

func parseDataRow(b []byte) []*string {
	if len(b) < 2 {
		return nil
	}
	n := int(binary.BigEndian.Uint16(b[:2]))
	b = b[2:]
	out := make([]*string, 0, n)
	for i := 0; i < n; i++ {
		if len(b) < 4 {
			return out
		}
		ln := int32(binary.BigEndian.Uint32(b[:4]))
		b = b[4:]
		if ln < 0 {
			out = append(out, nil) // NULL
			continue
		}
		if int(ln) > len(b) {
			return out
		}
		s := string(b[:ln])
		out = append(out, &s)
		b = b[ln:]
	}
	return out
}

func pgError(b []byte) error {
	fields := map[byte]string{}
	for i := 0; i < len(b) && b[i] != 0; {
		code := b[i]
		i++
		end := indexByte(b[i:], 0)
		if end < 0 {
			break
		}
		fields[code] = string(b[i : i+end])
		i += end + 1
	}
	return fmt.Errorf("PG 错误 %s：%s", fields['C'], fields['M'])
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func parseAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}

func hmacSHA256(key, msg []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(msg)
	return h.Sum(nil)
}

// pbkdf2SHA256 是最小 PBKDF2（RFC 2898），只支持我们需要的 c=iterations、dkLen=32。
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	var out []byte
	for block := 1; len(out) < keyLen; block++ {
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(block))
		u := hmacSHA256(password, append(append([]byte{}, salt...), hdr[:]...))
		t := append([]byte{}, u...)
		for i := 1; i < iter; i++ {
			u = hmacSHA256(password, u)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

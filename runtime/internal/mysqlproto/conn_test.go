package mysqlproto

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// fakeMySQL：握手 + native password + 简单结果集。用来确定性地验包边界与解析。
type fakeMySQL struct {
	ln     net.Listener
	user   string
	pass   string
	salt   []byte
	cols   []string
	rows   [][]*string
	failed bool
}

func newFake(t *testing.T) *fakeMySQL {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeMySQL{ln: ln, user: "reader", pass: "pw", salt: []byte("0123456789abcdefghij")}
	f.cols = []string{"id", "name"}
	f.rows = [][]*string{{sp("1"), sp("alice")}, {sp("2"), nil}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func sp(s string) *string { return &s }

func (f *fakeMySQL) handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)

	// 握手包
	var p []byte
	p = append(p, 10)
	p = append(p, []byte("8.0.0-fake")...)
	p = append(p, 0)
	var id [4]byte
	binary.LittleEndian.PutUint32(id[:], 42)
	p = append(p, id[:]...)
	p = append(p, f.salt[:8]...)
	p = append(p, 0)
	caps := uint32(capProtocol41 | capSecureConn | capPluginAuth)
	var lo [2]byte
	binary.LittleEndian.PutUint16(lo[:], uint16(caps))
	p = append(p, lo[:]...)
	p = append(p, 45)
	p = append(p, 2, 0) // status
	var hi [2]byte
	binary.LittleEndian.PutUint16(hi[:], uint16(caps>>16))
	p = append(p, hi[:]...)
	p = append(p, 21) // auth data len
	p = append(p, make([]byte, 10)...)
	p = append(p, f.salt[8:]...) // 13 字节（含结尾 0）
	p = append(p, 0)
	p = append(p, []byte("mysql_native_password")...)
	p = append(p, 0)
	writePkt(c, p)

	// 握手响应
	resp := readPkt(br)
	off := 4 + 4 + 1 + 23
	nul := indexByte(resp[off:], 0)
	if nul < 0 {
		return
	}
	user := string(resp[off : off+nul])
	off += nul + 1
	alen := int(resp[off])
	off++
	auth := resp[off : off+alen]
	want := nativeAuth(f.pass, f.salt)
	if user != f.user || string(auth) != string(want) {
		writeErr(c, 1045, "Access denied")
		return
	}
	writeOK(c)

	for {
		pkt := readPkt(br)
		if len(pkt) == 0 {
			return
		}
		switch pkt[0] {
		case 0x01: // QUIT
			return
		case 0x03: // QUERY
			sql := string(pkt[1:])
			if f.failed {
				writeErr(c, 1146, "Table 'x' doesn't exist")
				continue
			}
			if sql == "BAD" {
				writeErr(c, 1064, "syntax error")
				continue
			}
			writeCols(c, f.cols)
			for _, r := range f.rows {
				writeRow(c, r)
			}
			writeEOF(c)
		default:
			writeErr(c, 1047, "unknown command")
		}
	}
}

func writePkt(w io.Writer, p []byte) {
	hdr := []byte{byte(len(p)), byte(len(p) >> 8), byte(len(p) >> 16), 0}
	_, _ = w.Write(hdr)
	_, _ = w.Write(p)
}

func readPkt(br *bufio.Reader) []byte {
	var hdr [4]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return nil
	}
	n := int(hdr[0]) | int(hdr[1])<<8 | int(hdr[2])<<16
	b := make([]byte, n)
	if _, err := io.ReadFull(br, b); err != nil {
		return nil
	}
	return b
}

func writeOK(c net.Conn)  { writePkt(c, []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00}) }
func writeEOF(c net.Conn) { writePkt(c, []byte{0xfe, 0x00, 0x00, 0x02, 0x00}) }
func writeErr(c net.Conn, code uint16, msg string) {
	var b []byte
	b = append(b, 0xff)
	var cb [2]byte
	binary.LittleEndian.PutUint16(cb[:], code)
	b = append(b, cb[:]...)
	b = append(b, '#', 'H', 'Y', '0', '0', '0')
	b = append(b, []byte(msg)...)
	writePkt(c, b)
}

func lenEnc(s string) []byte {
	b := []byte{byte(len(s))}
	return append(b, []byte(s)...)
}

func writeCols(c net.Conn, cols []string) {
	writePkt(c, append([]byte{byte(len(cols))}, nil...))
	for _, name := range cols {
		var p []byte
		p = append(p, lenEnc("def")...)
		p = append(p, lenEnc("shop")...)
		p = append(p, lenEnc("orders")...)
		p = append(p, lenEnc("orders")...)
		p = append(p, lenEnc(name)...)
		p = append(p, lenEnc(name)...)
		p = append(p, 0x0c)
		p = append(p, 45, 0)            // charset
		p = append(p, 255, 0, 0, 0)     // length
		p = append(p, 0xfd, 0, 0, 0, 0) // type VAR_STRING + flags + decimals
		writePkt(c, p)
	}
	writeEOF(c)
}

func writeRow(c net.Conn, row []*string) {
	var p []byte
	for _, v := range row {
		if v == nil {
			p = append(p, 0xfb)
			continue
		}
		p = append(p, lenEnc(*v)...)
	}
	writePkt(c, p)
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func TestHandshakeAuthAndQuery(t *testing.T) {
	f := newFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, DSN{Host: "127.0.0.1", Port: portOf(f.ln), User: "reader", Password: "pw", Database: "shop"}, 3*time.Second)
	if err != nil {
		t.Fatalf("握手/认证失败：%v", err)
	}
	defer c.Close()
	cols, rows, err := c.Query(ctx, "SELECT id, name FROM orders")
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if len(cols) != 2 || cols[1] != "name" {
		t.Fatalf("列名不对：%v", cols)
	}
	if len(rows) != 2 || *rows[0][1] != "alice" || rows[1][1] != nil {
		t.Fatalf("行不对：%+v", rows)
	}
}

func TestServerErrorMapped(t *testing.T) {
	f := newFake(t)
	ctx := context.Background()
	c, err := Dial(ctx, DSN{Host: "127.0.0.1", Port: portOf(f.ln), User: "reader", Password: "pw", Database: "shop"}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, _, err := c.Query(ctx, "BAD"); err == nil || !contains(err.Error(), "1064") {
		t.Fatalf("错误码要带回来：%v", err)
	}
}

func TestWrongPasswordRejected(t *testing.T) {
	f := newFake(t)
	_, err := Dial(context.Background(), DSN{Host: "127.0.0.1", Port: portOf(f.ln), User: "reader", Password: "nope", Database: "shop"}, 3*time.Second)
	if err == nil || !contains(err.Error(), "1045") {
		t.Fatalf("错误密码应当被拒：%v", err)
	}
}

func portOf(ln net.Listener) string {
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	return p
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var _ = fmt.Sprint

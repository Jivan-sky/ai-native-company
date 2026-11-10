package ds

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePG 是一个只够用的 PG 服务端：trust 认证 + 简单查询。
// 有了它，线协议客户端与只读守卫都能确定性验，不依赖任何外部数据库。
type fakePG struct {
	ln       net.Listener
	mu       sync.Mutex
	queries  []string
	rows     [][]*string
	cols     []string
	failWord string
}

func newFakePG(t *testing.T) *fakePG {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakePG{ln: ln, cols: []string{"id", "name"}, rows: [][]*string{{strp("1"), strp("alice")}, {strp("2"), nil}}}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func strp(s string) *string { return &s }

func (f *fakePG) addr() string { return f.ln.Addr().String() }

func (f *fakePG) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *fakePG) handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	// startup：int32 长度 + int32 版本 + 参数
	var l [4]byte
	if _, err := io.ReadFull(br, l[:]); err != nil {
		return
	}
	n := binary.BigEndian.Uint32(l[:])
	body := make([]byte, n-4)
	if _, err := io.ReadFull(br, body); err != nil {
		return
	}
	writeMsg(c, 'R', []byte{0, 0, 0, 0}) // AuthenticationOk
	writeMsg(c, 'Z', []byte{'I'})

	for {
		typ, err := br.ReadByte()
		if err != nil {
			return
		}
		var bl [4]byte
		if _, err := io.ReadFull(br, bl[:]); err != nil {
			return
		}
		ln := binary.BigEndian.Uint32(bl[:])
		payload := make([]byte, ln-4)
		if _, err := io.ReadFull(br, payload); err != nil {
			return
		}
		switch typ {
		case 'X':
			return
		case 'Q':
			sql := strings.TrimRight(string(payload), "\x00")
			f.mu.Lock()
			f.queries = append(f.queries, sql)
			f.mu.Unlock()
			if f.failWord != "" && strings.Contains(sql, f.failWord) {
				writeMsg(c, 'E', errBody("42P01", "relation does not exist"))
				writeMsg(c, 'Z', []byte{'I'})
				continue
			}
			if strings.HasPrefix(strings.ToUpper(sql), "SET") {
				writeMsg(c, 'C', []byte("SET\x00"))
				writeMsg(c, 'Z', []byte{'I'})
				continue
			}
			if !strings.HasPrefix(strings.ToUpper(sql), "SELECT") {
				writeMsg(c, 'C', []byte("OK\x00"))
				writeMsg(c, 'Z', []byte{'I'})
				continue
			}
			writeMsg(c, 'T', rowDesc(f.cols))
			for _, r := range f.rows {
				writeMsg(c, 'D', dataRow(r))
			}
			writeMsg(c, 'C', []byte(fmt.Sprintf("SELECT %d\x00", len(f.rows))))
			writeMsg(c, 'Z', []byte{'I'})
		}
	}
}

func writeMsg(w io.Writer, typ byte, payload []byte) {
	hdr := make([]byte, 5)
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)+4))
	_, _ = w.Write(hdr)
	_, _ = w.Write(payload)
}

func rowDesc(cols []string) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(len(cols)))
	for _, c := range cols {
		b = append(b, []byte(c)...)
		b = append(b, 0)
		fixed := make([]byte, 18)
		binary.BigEndian.PutUint32(fixed[6:], 25) // text
		binary.BigEndian.PutUint16(fixed[10:], 0xffff)
		b = append(b, fixed...)
	}
	return b
}

func dataRow(vals []*string) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(len(vals)))
	for _, v := range vals {
		if v == nil {
			var n [4]byte
			binary.BigEndian.PutUint32(n[:], 0xffffffff)
			b = append(b, n[:]...)
			continue
		}
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(*v)))
		b = append(b, n[:]...)
		b = append(b, []byte(*v)...)
	}
	return b
}

func errBody(code, msg string) []byte {
	var b []byte
	b = append(b, 'C')
	b = append(b, []byte(code)...)
	b = append(b, 0)
	b = append(b, 'M')
	b = append(b, []byte(msg)...)
	b = append(b, 0, 0)
	return b
}

func srcFor(f *fakePG) Source {
	return Source{
		Schema: Schema, Name: "demo", Type: "postgres",
		DSN: "postgres://u:p@" + f.addr() + "/db?sslmode=disable", Secret: "PG_PASS",
		ReadOnly: true, MaxRows: 10, Timeout: "5s",
	}
}

func TestGuard(t *testing.T) {
	ok := []string{"SELECT 1", "  select * from t  ", "WITH x AS (SELECT 1) SELECT * FROM x"}
	for _, q := range ok {
		if err := Guard(q); err != nil {
			t.Errorf("应当放行 %q：%v", q, err)
		}
	}
	// 结尾一个分号是正常 SQL，允许；中间有分号（多语句）才拒。
	bad := []string{"", "UPDATE t SET a=1", "SELECT 1; DROP TABLE t", "DELETE FROM t",
		"SELECT 1;;", "insert into t values (1)"}
	for _, q := range bad {
		if err := Guard(q); err == nil {
			t.Errorf("应当拒绝 %q", q)
		}
	}
}

func TestQueryReadsRowsAndSetsReadOnly(t *testing.T) {
	f := newFakePG(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := Query(ctx, srcFor(f), "p", "SELECT id, name FROM users")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Columns) != 2 || res.Columns[1] != "name" {
		t.Fatalf("列名不对：%v", res.Columns)
	}
	if res.Returned != 2 || *res.Rows[0][1] != "alice" || res.Rows[1][1] != nil {
		t.Fatalf("行不对：%+v", res.Rows)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	joined := strings.Join(f.queries, " | ")
	if !strings.Contains(joined, "default_transaction_read_only = on") {
		t.Fatalf("必须先开只读会话：%s", joined)
	}
}

func TestQueryMapsServerError(t *testing.T) {
	f := newFakePG(t)
	f.failWord = "BOOM"
	_, err := Query(context.Background(), srcFor(f), "p", "SELECT BOOM FROM t")
	if err == nil || !strings.Contains(err.Error(), "42P01") {
		t.Fatalf("服务端错误要原样带回来：%v", err)
	}
}

func TestQueryTruncates(t *testing.T) {
	f := newFakePG(t)
	f.rows = make([][]*string, 30)
	for i := range f.rows {
		f.rows[i] = []*string{strp("x")}
	}
	s := srcFor(f)
	s.MaxRows = 5
	res, err := Query(context.Background(), s, "p", "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.Returned != 5 {
		t.Fatalf("应当截断到 5 行：%+v", res)
	}
}

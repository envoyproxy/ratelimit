package redis

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRedis is a minimal RESP server for exercising connection failure modes
// that miniredis cannot simulate: it answers PING with PONG and every other
// command with :1, answers CLUSTER SLOTS as a single-node cluster owning every
// slot, and can be switched to swallow requests without replying,
// like a server that vanished without resetting its connections. A connection
// that swallowed a request stays dead even after the blackhole is lifted, as
// TCP never loses a request on a live connection: only connections opened
// afterwards reach the "recovered" server.
type fakeRedis struct {
	ln        net.Listener
	blackhole atomic.Bool
	// delay, if set, is applied before every reply.
	delay atomic.Int64

	mu    sync.Mutex
	conns []net.Conn
}

func newFakeRedis(t *testing.T, tlsConfig *tls.Config) *fakeRedis {
	t.Helper()
	var ln net.Listener
	var err error
	if tlsConfig != nil {
		ln, err = tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	} else {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRedis{ln: ln}
	go f.accept()
	t.Cleanup(f.close)
	return f
}

func (f *fakeRedis) addr() string { return f.ln.Addr().String() }

func (f *fakeRedis) setBlackhole(on bool) { f.blackhole.Store(on) }

func (f *fakeRedis) setDelay(d time.Duration) { f.delay.Store(int64(d)) }

func (f *fakeRedis) close() {
	f.ln.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.Close()
	}
}

func (f *fakeRedis) accept() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns = append(f.conns, c)
		f.mu.Unlock()
		go f.serve(c)
	}
}

func (f *fakeRedis) serve(c net.Conn) {
	r := bufio.NewReader(c)
	dead := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if !strings.HasPrefix(line, "*") {
			continue
		}
		var n int
		fmt.Sscanf(line, "*%d", &n)
		var cmd string
		for i := 0; i < n; i++ {
			if _, err := r.ReadString('\n'); err != nil { // $len
				return
			}
			arg, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if i == 0 {
				cmd = strings.ToUpper(strings.TrimSpace(arg))
			}
		}
		if dead || f.blackhole.Load() {
			dead = true
			continue
		}
		if d := time.Duration(f.delay.Load()); d > 0 {
			time.Sleep(d)
		}
		switch cmd {
		case "PING":
			c.Write([]byte("+PONG\r\n"))
		case "AUTH", "SELECT", "SET":
			c.Write([]byte("+OK\r\n"))
		case "CLUSTER":
			c.Write([]byte(f.clusterSlots()))
		default:
			c.Write([]byte(":1\r\n"))
		}
	}
}

// clusterSlots is the CLUSTER SLOTS reply of a single-node cluster served by f.
func (f *fakeRedis) clusterSlots() string {
	host, port, _ := net.SplitHostPort(f.addr())
	return fmt.Sprintf("*1\r\n*3\r\n:0\r\n:16383\r\n*3\r\n$%d\r\n%s\r\n:%s\r\n$4\r\nfake\r\n", len(host), host, port)
}

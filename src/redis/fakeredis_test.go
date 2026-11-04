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
// that miniredis cannot simulate: it answers PING with PONG, LPUSH with a
// WRONGTYPE error and every other command with :1, answers CLUSTER SLOTS as a single-node cluster owning every
// slot, and can be switched to swallow requests without replying,
// like a server that vanished without resetting its connections. It can also
// demote every connection opened so far to a read-only replica, which answers
// writes with a READONLY error while connections opened afterwards reach a
// healthy master, as after a failover that keeps the demoted server running. A connection
// that swallowed a request stays dead even after the blackhole is lifted, as
// TCP never loses a request on a live connection: only connections opened
// afterwards reach the "recovered" server.
type fakeRedis struct {
	ln        net.Listener
	blackhole atomic.Bool
	// delay, if set, is applied before every reply.
	delay atomic.Int64
	// accepted counts the connections accepted so far; connections numbered up
	// to demoted answer every command but PING with a READONLY error.
	accepted atomic.Int64
	demoted  atomic.Int64

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

// demoteExistingConns makes every connection accepted so far a read-only
// replica. Connections accepted afterwards are healthy.
func (f *fakeRedis) demoteExistingConns() { f.demoted.Store(f.accepted.Load()) }

// connsAccepted returns the number of connections accepted so far.
func (f *fakeRedis) connsAccepted() int { return int(f.accepted.Load()) }

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
		go f.serve(c, f.accepted.Add(1))
	}
}

func (f *fakeRedis) serve(c net.Conn, seq int64) {
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
		case "LPUSH":
			c.Write([]byte("-WRONGTYPE Operation against a key holding the wrong kind of value\r\n"))
		default:
			if seq <= f.demoted.Load() {
				c.Write([]byte("-" + readOnlyErrMsg + "\r\n"))
				continue
			}
			c.Write([]byte(":1\r\n"))
		}
	}
}

// clusterSlots is the CLUSTER SLOTS reply of a single-node cluster served by f.
func (f *fakeRedis) clusterSlots() string {
	host, port, _ := net.SplitHostPort(f.addr())
	return fmt.Sprintf("*1\r\n*3\r\n:0\r\n:16383\r\n*3\r\n$%d\r\n%s\r\n:%s\r\n$4\r\nfake\r\n", len(host), host, port)
}

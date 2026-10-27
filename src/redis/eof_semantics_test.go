package redis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	"github.com/stretchr/testify/require"
)

// replyDropRedis records actual mutations, unlike a transport stub that can
// return EOF without establishing whether Redis received the commands.
type replyDropRedis struct {
	listener    net.Listener
	dropReply   bool
	acceptDone  chan struct{}
	workers     sync.WaitGroup
	mu          sync.Mutex
	closing     bool
	connections []net.Conn
	commands    map[string]int
	values      map[string]int
	ttls        map[string]int
	errors      []error
}

func newReplyDropRedis(t *testing.T, dropReply bool) *replyDropRedis {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &replyDropRedis{listener: listener, dropReply: dropReply, acceptDone: make(chan struct{}), commands: make(map[string]int), values: make(map[string]int), ttls: make(map[string]int)}
	go func() {
		defer close(s.acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			if s.closing {
				s.mu.Unlock()
				_ = conn.Close()
				return
			}
			s.connections = append(s.connections, conn)
			s.workers.Add(1)
			s.mu.Unlock()
			go func() { defer s.workers.Done(); defer conn.Close(); s.serve(conn) }()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		s.mu.Lock()
		s.closing = true
		for _, conn := range s.connections {
			_ = conn.Close()
		}
		s.mu.Unlock()
		select {
		case <-s.acceptDone:
		case <-time.After(time.Second):
			t.Fatal("fixture accept goroutine did not join")
		}
		joined := make(chan struct{})
		go func() { s.workers.Wait(); close(joined) }()
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Fatal("fixture socket goroutines did not join")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		require.Empty(t, s.errors)
	})
	return s
}

// The bounded RESP parser follows the existing exclusive-action socket fixture.
func readEOFCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 3 || line[0] != '*' {
		return nil, errors.New("expected command array")
	}
	count, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || count < 1 || count > 32 {
		return nil, errors.New("command argument bound")
	}
	args := make([]string, count)
	for i := range args {
		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if len(line) < 3 || line[0] != '$' {
			return nil, errors.New("expected bulk argument")
		}
		size, err := strconv.Atoi(strings.TrimSpace(line[1:]))
		if err != nil || size < 0 || size > 1024 {
			return nil, errors.New("command byte bound")
		}
		data := make([]byte, size+2)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		if string(data[size:]) != "\r\n" {
			return nil, errors.New("missing bulk terminator")
		}
		args[i] = string(data[:size])
	}
	return args, nil
}

func (s *replyDropRedis) serve(conn net.Conn) {
	reader := bufio.NewReader(conn)
	for {
		args, err := readEOFCommand(reader)
		if err != nil {
			return
		}
		command := strings.ToUpper(args[0])
		key := ""
		if len(args) > 1 {
			key = args[1]
		}
		s.mu.Lock()
		s.commands[command+":"+key]++
		reply := "+PONG\r\n"
		switch {
		case command == "PING":
		case command == "INCRBY" && len(args) == 3:
			n, err := strconv.Atoi(args[2])
			if err != nil {
				s.errors = append(s.errors, err)
			}
			s.values[key] += n
			reply = fmt.Sprintf(":%d\r\n", s.values[key])
		case command == "EXPIRE" && len(args) == 3:
			n, err := strconv.Atoi(args[2])
			if err != nil {
				s.errors = append(s.errors, err)
			}
			s.ttls[key] = n
			reply = ":1\r\n"
		default:
			s.errors = append(s.errors, fmt.Errorf("unexpected command %q", args))
		}
		drop := s.dropReply && key == "original"
		s.mu.Unlock()
		if drop {
			// Receive and apply both pipeline commands before dropping every
			// response. The client cannot infer that the increment committed.
			if command == "EXPIRE" {
				return
			}
			continue
		}
		if _, err := io.WriteString(conn, reply); err != nil {
			return
		}
	}
}

type eofNetDialer struct {
	base        *netDialer
	beforeWrite atomic.Bool
	active      atomic.Int32
	created     atomic.Int32
}

type eofNetConn struct {
	net.Conn
	dialer *eofNetDialer
	once   sync.Once
}

func (d *eofNetDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := d.base.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	d.active.Add(1)
	d.created.Add(1)
	return &eofNetConn{Conn: conn, dialer: d}, nil
}

func (c *eofNetConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.dialer.active.Add(-1) })
	return err
}

func (c *eofNetConn) Write(data []byte) (int, error) {
	if c.dialer.beforeWrite.CompareAndSwap(true, false) {
		// Deterministic control: disconnect before writing any mutation bytes.
		_ = c.Close()
		return 0, io.EOF
	}
	return c.Conn.Write(data)
}

func TestPipelineEOFDoesNotReplayMutationAndNextDistinctRequestRecovers(t *testing.T) {
	for _, tt := range []struct {
		name          string
		beforeWrite   bool
		originalCount int
	}{
		{name: "disconnect_before_dispatch", beforeWrite: true, originalCount: 0},
		{name: "mutation_committed_reply_lost", originalCount: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			server := newReplyDropRedis(t, !tt.beforeWrite)
			store := stats.NewStore(stats.NewNullSink(), false)
			poolStats := newPoolStats(store)
			dialer := &eofNetDialer{base: newNetDialer(time.Second, clientOptions{ioTimeout: time.Second}, false, nil)}
			config := radix.PoolConfig{Size: 1, Dialer: radix.Dialer{NetDialer: dialer, WriteFlushInterval: 150 * time.Microsecond}, Trace: poolTrace(&poolStats, false, nil)}
			config = exclusivePoolConfig(config, time.Second)
			pool, err := config.New(ctx, "tcp", server.listener.Addr().String())
			require.NoError(t, err)
			client := &clientImpl{client: pool, stats: poolStats, clusterPipelineParallelism: 1}
			t.Cleanup(func() {
				require.NoError(t, client.Close())
				require.Zero(t, dialer.active.Load(), "all client sockets must close before fixture joins")
			})

			operations, outstanding := 0, 0
			call := func(key string) (uint64, error) {
				operations++
				outstanding++
				defer func() { outstanding-- }()
				callCtx, stop := context.WithTimeout(ctx, time.Second)
				defer stop()
				var result uint64
				pipeline := client.PipeAppend(nil, &result, "INCRBY", key, 1)
				pipeline = client.PipeAppend(pipeline, nil, "EXPIRE", key, 60)
				err := client.PipeDo(callCtx, pipeline)
				return result, err
			}

			dialer.beforeWrite.Store(tt.beforeWrite)
			started := time.Now()
			_, err = call("original")
			if tt.beforeWrite {
				// Concurrent reader shutdown may report the closed socket before
				// the injected writer EOF; both preserve the transport failure.
				require.True(t, errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed), "unexpected transport error: %v", err)
				require.False(t, dialer.beforeWrite.Load(), "the no-write disconnect must have fired")
			} else {
				require.ErrorIs(t, err, io.EOF)
			}
			require.Less(t, time.Since(started), time.Second)
			require.Zero(t, outstanding, "original operation must be joined before the next request")
			started = time.Now()
			result, err := call("distinct")
			require.NoError(t, err)
			require.Equal(t, uint64(1), result)
			require.Less(t, time.Since(started), time.Second, "replacement and distinct operation are bounded")
			require.Equal(t, 2, operations)
			require.Zero(t, outstanding)
			require.Equal(t, int32(2), dialer.created.Load(), "one initial connection and one replacement")
			require.Equal(t, uint64(2), store.NewCounter("cx_total").Value())
			require.Equal(t, uint64(1), store.NewCounter("cx_local_close").Value())
			require.Equal(t, uint64(1), store.NewGauge("cx_active").Value())

			server.mu.Lock()
			defer server.mu.Unlock()
			for _, command := range []string{"INCRBY", "EXPIRE"} {
				require.Equal(t, tt.originalCount, server.commands[command+":original"], "original pipeline must never be replayed")
				require.Equal(t, 1, server.commands[command+":distinct"])
			}
			require.Equal(t, tt.originalCount, server.values["original"])
			require.Equal(t, tt.originalCount*60, server.ttls["original"])
			require.Equal(t, 1, server.values["distinct"])
			require.Equal(t, 60, server.ttls["distinct"])
			t.Logf("EOF retained; operations=%d outstanding=%d original_mutations=%d distinct_mutations=1 created_connections=%d", operations, outstanding, server.values["original"], dialer.created.Load())
		})
	}
}

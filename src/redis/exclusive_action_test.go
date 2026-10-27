package redis

import (
	"bufio"
	"context"
	"crypto/tls"
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

// gatedRedis holds command replies until release. Unlike a context-aware stub,
// the real socket reader cannot learn about a caller's cancellation from us.
type gatedRedis struct {
	listener    net.Listener
	ctx         context.Context
	cancel      context.CancelFunc
	released    chan struct{}
	releaseOnce sync.Once
	entered     chan string
	wg          sync.WaitGroup
	mu          sync.Mutex
	conns       []net.Conn
	commands    map[string]int
	gatePing    atomic.Bool
}

func newGatedRedis(t *testing.T, config *tls.Config) *gatedRedis {
	t.Helper()
	var listener net.Listener
	var err error
	if config == nil {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
	} else {
		listener, err = tls.Listen("tcp", "127.0.0.1:0", config)
	}
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	server := &gatedRedis{listener: listener, ctx: ctx, cancel: cancel, released: make(chan struct{}), entered: make(chan string, 16), commands: make(map[string]int)}
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			server.mu.Lock()
			server.conns = append(server.conns, conn)
			server.mu.Unlock()
			server.wg.Add(1)
			go func() { defer server.wg.Done(); defer conn.Close(); server.serve(conn) }()
		}
	}()
	t.Cleanup(func() {
		cancel()
		server.release()
		_ = listener.Close()
		server.mu.Lock()
		for _, conn := range server.conns {
			_ = conn.Close()
		}
		server.mu.Unlock()
		server.wg.Wait()
	})
	return server
}
func (s *gatedRedis) release() { s.releaseOnce.Do(func() { close(s.released) }) }
func (s *gatedRedis) count(command, key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commands[command+":"+key]
}

func readRESPCommand(reader *bufio.Reader) ([]string, error) {
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
		if _, err = io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		if string(data[size:]) != "\r\n" {
			return nil, errors.New("missing terminator")
		}
		args[i] = string(data[:size])
	}
	return args, nil
}

func (s *gatedRedis) serve(conn net.Conn) {
	reader := bufio.NewReader(conn)
	for {
		args, err := readRESPCommand(reader)
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
		s.mu.Unlock()
		if (command == "INCRBY" && (key == "long" || key == "short")) || (command == "PING" && s.gatePing.Load()) {
			if command == "PING" {
				key = "PING"
			}
			select {
			case s.entered <- key:
			case <-s.ctx.Done():
				return
			}
			select {
			case <-s.released:
			case <-s.ctx.Done():
				return
			}
		}
		reply := ":1\r\n"
		switch command {
		case "PING":
			reply = "+PONG\r\n"
		case "AUTH":
			reply = "+OK\r\n"
		case "CLUSTER":
			host, port, _ := net.SplitHostPort(s.listener.Addr().String())
			reply = fmt.Sprintf("*1\r\n*3\r\n:0\r\n:16383\r\n*3\r\n$%d\r\n%s\r\n:%s\r\n$4\r\ntest\r\n", len(host), host, port)
		}
		if _, err = io.WriteString(conn, reply); err != nil {
			return
		}
	}
}

type countingDialer struct {
	dialer       *netDialer
	active, peak atomic.Int32
}

type countedConn struct {
	net.Conn
	dialer *countingDialer
	once   sync.Once
}

func (c *countedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.dialer.active.Add(-1) })
	return err
}

func (d *countingDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := d.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	active := d.active.Add(1)
	for old := d.peak.Load(); active > old; old = d.peak.Load() {
		if d.peak.CompareAndSwap(old, active) {
			break
		}
	}
	return &countedConn{Conn: conn, dialer: d}, nil
}

func ownedTestClient(t *testing.T, server *gatedRedis, size int, cluster bool, tlsConfig *tls.Config) (*clientImpl, *countingDialer) {
	t.Helper()
	store := stats.NewStore(stats.NewNullSink(), false)
	poolStats := newPoolStats(store)
	dialer := &countingDialer{dialer: newNetDialer(time.Second, clientOptions{ioTimeout: time.Second}, tlsConfig != nil, tlsConfig)}
	config := radix.PoolConfig{Size: size, Dialer: radix.Dialer{NetDialer: dialer, WriteFlushInterval: 150 * time.Microsecond}, Trace: poolTrace(&poolStats, false, nil)}
	config = exclusivePoolConfig(config, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var client redisClient
	var err error
	if cluster {
		client, err = (radix.ClusterConfig{PoolConfig: config}).New(ctx, []string{server.listener.Addr().String()})
	} else {
		client, err = config.New(ctx, "tcp", server.listener.Addr().String())
	}
	require.NoError(t, err)
	result := &clientImpl{client: client, stats: poolStats, isCluster: cluster, clusterPipelineParallelism: 1}
	t.Cleanup(func() {
		_ = result.Close()
		require.Equal(t, int32(0), dialer.active.Load(), "client sockets must be closed and joined")
	})
	require.Eventually(t, func() bool { return dialer.active.Load() == int32(size) }, time.Second, time.Millisecond)
	return result, dialer
}

func ownedPipeline(c Client, key string) *Pipeline {
	p := c.PipeAppend(nil, new(uint64), "INCRBY", key, 1)
	p = c.PipeAppend(p, nil, "EXPIRE", key, 60)
	return &p
}

func waitEntered(t *testing.T, server *gatedRedis, want string) {
	t.Helper()
	select {
	case key := <-server.entered:
		require.Equal(t, want, key)
	case <-time.After(time.Second):
		t.Fatal("command never entered fake Redis")
	}
}

func receiveResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("unjoined Redis operation")
		return nil
	}
}

func TestRequestPipelineQueuedCancellationDoesNotWaitForLongPeer(t *testing.T) {
	for _, variant := range []struct {
		name         string
		cluster, tls bool
	}{{"single", false, false}, {"cluster", true, false}, {"clusterTLS", true, true}} {
		t.Run(variant.name, func(t *testing.T) {
			var serverTLS, clientTLS *tls.Config
			if variant.tls {
				serverTLS, clientTLS = selfSignedTLS(t)
			}
			server := newGatedRedis(t, serverTLS)
			client, dialer := ownedTestClient(t, server, 1, variant.cluster, clientTLS)
			longCtx, longCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer longCancel()
			longResult := make(chan error, 1)
			go func() { longResult <- client.PipeDo(longCtx, *ownedPipeline(client, "long")) }()
			waitEntered(t, server, "long")
			shortCtx, shortCancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer shortCancel()
			shortResult := make(chan error, 1)
			go func() { shortResult <- client.PipeDo(shortCtx, *ownedPipeline(client, "short")) }()
			prompt := false
			var shortErr error
			select {
			case shortErr = <-shortResult:
				prompt = true
			case <-time.After(150 * time.Millisecond):
			}
			select {
			case err := <-longResult:
				t.Fatalf("short cancellation terminated the long peer: %v", err)
			default:
			}
			require.Equal(t, int32(1), dialer.peak.Load(), "pool1 must not create overflow connections")
			// Always release and join both operations before reporting the regression.
			server.release()
			require.NoError(t, receiveResult(t, longResult))
			if !prompt {
				shortErr = receiveResult(t, shortResult)
			}
			require.True(t, prompt, "short canceled operation waited for unrelated long reply")
			require.ErrorIs(t, shortErr, context.DeadlineExceeded)
			require.Equal(t, 0, server.count("INCRBY", "short"), "canceled pool waiter must not write commands")
		})
	}
}

func TestRequestPipelineActiveCancellationDoesNotCloseLongPeer(t *testing.T) {
	server := newGatedRedis(t, nil)
	client, dialer := ownedTestClient(t, server, 2, false, nil)
	longCtx, longCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer longCancel()
	longResult := make(chan error, 1)
	go func() { longResult <- client.PipeDo(longCtx, *ownedPipeline(client, "long")) }()
	waitEntered(t, server, "long")
	shortCtx, shortCancel := context.WithCancel(context.Background())
	shortResult := make(chan error, 1)
	go func() { shortResult <- client.PipeDo(shortCtx, *ownedPipeline(client, "short")) }()
	waitEntered(t, server, "short")
	shortCancel()
	shortErr := receiveResult(t, shortResult)
	require.ErrorIs(t, shortErr, context.Canceled)
	select {
	case err := <-longResult:
		t.Fatalf("canceling owned connection affected long peer: %v", err)
	default:
	}
	require.LessOrEqual(t, dialer.peak.Load(), int32(2))
	server.release()
	require.NoError(t, receiveResult(t, longResult))
	// Recovery can use the surviving connection or a bounded replacement.
	recoveryCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, client.PipeDo(recoveryCtx, *ownedPipeline(client, "recovery")))
	require.LessOrEqual(t, dialer.peak.Load(), int32(2))
}

func TestRequestActionPreservesRoutingAndRetryProperties(t *testing.T) {
	action := radix.Cmd(new(uint64), "INCRBY", "key", "1")
	want := action.Properties()
	require.True(t, want.CanRetry)
	require.True(t, want.CanShareConn)
	want.CanShareConn = false
	require.Equal(t, want, (exclusiveRedisAction{action}).Properties())
	pipeline := radix.NewPipeline()
	pipeline.Append(action)
	pipeline.Append(radix.Cmd(nil, "EXPIRE", "key", "60"))
	want = pipeline.Properties()
	want.CanShareConn = false
	require.Equal(t, want, (exclusiveRedisAction{pipeline}).Properties())
}

func TestAllRequestPipelineDispatchesUseOwnedConnections(t *testing.T) {
	for _, cluster := range []bool{false, true} {
		for _, count := range []int{1, 2, 4} {
			fake := &recordingRedisClient{}
			client := &clientImpl{client: fake, isCluster: cluster, clusterPipelineParallelism: 1}
			var p Pipeline
			for i := 0; i < count; i++ {
				key := "a"
				if i > 1 {
					key = "b"
				}
				p = client.PipeAppend(p, new(uint64), "INCRBY", key, 1)
			}
			require.NoError(t, client.PipeDo(context.Background(), p))
			require.NotEmpty(t, fake.calls)
			for _, action := range fake.calls {
				require.False(t, action.Properties().CanShareConn)
				require.Equal(t, cluster && count == 1, action.Properties().CanRetry)
			}
		}
	}
}

func TestCanceledRequestDoesNotDispatchToRedis(t *testing.T) {
	fake := &recordingRedisClient{}
	client := &clientImpl{client: fake, isCluster: true, clusterPipelineParallelism: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := client.PipeDo(ctx, client.PipeAppend(nil, new(uint64), "INCRBY", "key", 1))
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, fake.calls)
}

func TestControlCommandDoesNotHoldPoolLockAcrossRequestDeadline(t *testing.T) {
	server := newGatedRedis(t, nil)
	client, dialer := ownedTestClient(t, server, 1, true, nil)
	server.gatePing.Store(true)
	controlCtx, controlCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer controlCancel()
	controlResult := make(chan error, 1)
	go func() { var pong string; controlResult <- client.client.Do(controlCtx, radix.Cmd(&pong, "PING")) }()
	waitEntered(t, server, "PING")
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer shortCancel()
	shortResult := make(chan error, 1)
	go func() { shortResult <- client.PipeDo(shortCtx, *ownedPipeline(client, "short")) }()
	prompt := false
	var shortErr error
	select {
	case shortErr = <-shortResult:
		prompt = true
	case <-time.After(150 * time.Millisecond):
	}
	select {
	case err := <-controlResult:
		t.Fatalf("short request canceled unrelated control action: %v", err)
	default:
	}
	server.release()
	require.NoError(t, receiveResult(t, controlResult))
	if !prompt {
		shortErr = receiveResult(t, shortResult)
	}
	require.True(t, prompt, "request cancellation waited for shared control I/O lock")
	require.ErrorIs(t, shortErr, context.DeadlineExceeded)
	require.Equal(t, 0, server.count("INCRBY", "short"))
	require.Equal(t, int32(1), dialer.peak.Load())
}

func TestExclusivePoolConfigPreservesDialerAndBounds(t *testing.T) {
	original := radix.PoolConfig{Size: 10, MinReconnectInterval: 50 * time.Millisecond, MaxReconnectInterval: time.Second, Dialer: radix.Dialer{WriteFlushInterval: 150 * time.Microsecond}}
	configured := exclusivePoolConfig(original, time.Second)
	require.Equal(t, original.Size, configured.Size)
	require.Equal(t, original.MinReconnectInterval, configured.MinReconnectInterval)
	require.Equal(t, original.MaxReconnectInterval, configured.MaxReconnectInterval)
	require.Equal(t, original.Dialer, configured.Dialer)
	require.Equal(t, time.Duration(-1), configured.PingInterval)
	require.NotNil(t, configured.CustomPool)
	require.Nil(t, original.CustomPool)
}

package redis

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"testing"
	"time"

	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	"github.com/stretchr/testify/require"
)

type writeObservedConn struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

type closeObservedConn struct {
	*writeObservedConn
	closeStarted chan struct{}
	releaseClose chan struct{}
	closeOnce    sync.Once
}

func (c *closeObservedConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.Conn.Close()
		close(c.closeStarted)
		<-c.releaseClose
	})
	return err
}

func TestOwnedActionJoinsCancellationBeforeReturning(t *testing.T) {
	local, peer := net.Pipe()
	defer peer.Close()
	observed := &closeObservedConn{
		writeObservedConn: &writeObservedConn{Conn: local, started: make(chan struct{})},
		closeStarted:      make(chan struct{}),
		releaseClose:      make(chan struct{}),
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(observed.releaseClose) }) }
	conn, err := (radix.Dialer{NetDialer: pipeNetDialer{observed}}).Dial(context.Background(), "tcp", "pipe")
	require.NoError(t, err)
	defer conn.Close()
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- (exclusiveRedisAction{radix.Cmd(nil, "SET", "blocked", "value")}).Perform(ctx, conn) }()
	select {
	case <-observed.started:
	case <-time.After(time.Second):
		_ = peer.Close()
		release()
		_ = receiveResult(t, result)
		t.Fatal("socket write never started")
	}
	cancel()
	closeStarted := false
	select {
	case <-observed.closeStarted:
		closeStarted = true
	case <-time.After(150 * time.Millisecond):
	}
	returnedEarly := false
	var got error
	select {
	case got = <-result:
		returnedEarly = true
	case <-time.After(30 * time.Millisecond):
	}
	release()
	_ = peer.Close()
	if !returnedEarly {
		got = receiveResult(t, result)
	}
	require.True(t, closeStarted, "cancellation never interrupted the owned connection")
	require.False(t, returnedEarly, "action returned while its cancellation callback still owned the connection")
	require.ErrorIs(t, got, context.Canceled)
}

func TestDisabledPoolWaitTimeoutPreservesWaiting(t *testing.T) {
	server := newGatedRedis(t, nil)
	config := exclusivePoolConfig(radix.PoolConfig{Size: 1, Dialer: radix.Dialer{NetDialer: newNetDialer(time.Second, clientOptions{ioTimeout: time.Second}, false, nil)}}, 0)
	client, err := config.New(context.Background(), "tcp", server.listener.Addr().String())
	require.NoError(t, err)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	longResult, shortResult := make(chan error, 1), make(chan error, 1)
	go func() { longResult <- client.Do(ctx, radix.Cmd(new(uint64), "INCRBY", "long", "1")) }()
	waitEntered(t, server, "long")
	go func() { shortResult <- client.Do(ctx, radix.Cmd(new(uint64), "INCRBY", "short", "1")) }()
	returnedEarly := false
	var got error
	select {
	case got = <-shortResult:
		returnedEarly = true
	case <-time.After(80 * time.Millisecond):
	}
	server.release()
	require.NoError(t, receiveResult(t, longResult))
	if !returnedEarly {
		got = receiveResult(t, shortResult)
	}
	require.False(t, returnedEarly, "disabled acquisition timeout rejected a valid waiter")
	require.NoError(t, got)
	require.Equal(t, 1, server.count("INCRBY", "short"))
}

func TestOwnedPoolCloseCancelsAndJoinsActiveIO(t *testing.T) {
	for _, waitTimeout := range []time.Duration{0, 40 * time.Millisecond} {
		t.Run(waitTimeout.String(), func(t *testing.T) {
			server := newGatedRedis(t, nil)
			raw, err := (radix.Dialer{NetDialer: newNetDialer(time.Second, clientOptions{}, false, nil)}).Dial(context.Background(), "tcp", server.listener.Addr().String())
			require.NoError(t, err)
			defer raw.Close()
			connectionStats := newPoolStats(stats.NewStore(stats.NewNullSink(), false))
			config := exclusivePoolConfig(radix.PoolConfig{Size: 1, Trace: poolTrace(&connectionStats, false, nil), Dialer: radix.Dialer{
				CustomConn: func(context.Context, string, string) (radix.Conn, error) { return raw, nil },
			}}, waitTimeout)
			client, err := config.New(context.Background(), "tcp", server.listener.Addr().String())
			require.NoError(t, err)
			defer client.Close()
			result, closeResult := make(chan error, 1), make(chan error, 1)
			go func() { result <- client.Do(context.Background(), radix.Cmd(new(uint64), "INCRBY", "long", "1")) }()
			waitEntered(t, server, "long")
			go func() { closeResult <- client.Close() }()
			prompt := false
			var got error
			select {
			case got = <-result:
				prompt = true
			case <-time.After(150 * time.Millisecond):
			}
			// Cleanup is explicit even when the pool forgets a checked-out connection.
			server.release()
			_ = raw.Close()
			if !prompt {
				got = receiveResult(t, result)
			}
			require.NoError(t, receiveResult(t, closeResult))
			require.True(t, prompt, "pool Close left checked-out I/O running")
			require.Error(t, got)
			require.Equal(t, uint64(0), connectionStats.connectionActive.Value(), "pool Close leaked its active connection")
			require.Error(t, client.Do(context.Background(), radix.Cmd(nil, "PING")))
		})
	}
}

// Use independent queue and socket limits here to prove that acquisition's
// timer stops at admission; it must not cancel a valid, slower peer operation.
func TestPoolWaitTimeoutDoesNotCancelActivePeer(t *testing.T) {
	for _, variant := range []struct {
		name         string
		cluster, tls bool
	}{{"single", false, false}, {"singleTLS", false, true}, {"cluster", true, false}, {"clusterTLS", true, true}} {
		t.Run(variant.name, func(t *testing.T) {
			var serverTLS, clientTLS *tls.Config
			if variant.tls {
				serverTLS, clientTLS = selfSignedTLS(t)
			}
			server := newGatedRedis(t, serverTLS)
			store := stats.NewStore(stats.NewNullSink(), false)
			connectionStats := newPoolStats(store)
			dialer := &countingDialer{dialer: newNetDialer(time.Second, clientOptions{ioTimeout: time.Second}, variant.tls, clientTLS)}
			config := exclusivePoolConfig(radix.PoolConfig{Size: 1, Dialer: radix.Dialer{NetDialer: dialer, WriteFlushInterval: 150 * time.Microsecond}, Trace: poolTrace(&connectionStats, false, nil)}, 40*time.Millisecond)
			var client redisClient
			var err error
			if variant.cluster {
				client, err = (radix.ClusterConfig{PoolConfig: config}).New(context.Background(), []string{server.listener.Addr().String()})
			} else {
				client, err = config.New(context.Background(), "tcp", server.listener.Addr().String())
			}
			require.NoError(t, err)
			defer client.Close()
			longContext, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			longResult := make(chan error, 1)
			go func() { longResult <- client.Do(longContext, radix.Cmd(new(uint64), "INCRBY", "long", "1")) }()
			waitEntered(t, server, "long")
			shortResult := make(chan error, 1)
			go func() { shortResult <- client.Do(context.Background(), radix.Cmd(new(uint64), "INCRBY", "short", "1")) }()
			var shortError error
			prompt := false
			select {
			case shortError = <-shortResult:
				prompt = true
			case <-time.After(200 * time.Millisecond):
			}
			time.Sleep(80 * time.Millisecond)
			var peerFinished bool
			select {
			case <-longResult:
				peerFinished = true
			default:
			}
			// Release and join before assertions so regressions cannot strand I/O.
			server.release()
			if !peerFinished {
				require.NoError(t, receiveResult(t, longResult))
			}
			if !prompt {
				shortError = receiveResult(t, shortResult)
			}
			require.True(t, prompt, "pool acquisition exceeded its independent timeout")
			require.ErrorIs(t, shortError, errPoolWaitTimeout)
			require.False(t, peerFinished, "queue timeout canceled an admitted peer")
			require.Equal(t, 0, server.count("INCRBY", "short"), "expired waiter wrote a Redis command")
			require.Equal(t, int32(1), dialer.peak.Load(), "pool1 created overflow connections")
		})
	}
}

func (c *writeObservedConn) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.started) })
	return c.Conn.Write(p)
}

type pipeNetDialer struct{ conn net.Conn }

func (d pipeNetDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return d.conn, nil
}

// A peer that never reads blocks the actual socket write. Radix cannot interrupt
// this by changing the read deadline: no reader has been assigned the command yet.
func TestOwnedActionCancellationInterruptsBlockedWrite(t *testing.T) {
	for _, flush := range []time.Duration{0, 150 * time.Microsecond} {
		t.Run(flush.String(), func(t *testing.T) {
			local, peer := net.Pipe()
			defer peer.Close()
			observed := &writeObservedConn{Conn: local, started: make(chan struct{})}
			conn, err := (radix.Dialer{NetDialer: pipeNetDialer{observed}, WriteFlushInterval: flush}).Dial(context.Background(), "tcp", "pipe")
			require.NoError(t, err)
			defer conn.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- (exclusiveRedisAction{radix.Cmd(nil, "SET", "blocked", "value")}).Perform(ctx, conn)
			}()
			select {
			case <-observed.started:
			case <-time.After(time.Second):
				_ = peer.Close()
				_ = receiveResult(t, result)
				t.Fatal("socket write never started")
			}
			cancel()
			prompt := false
			var got error
			select {
			case got = <-result:
				prompt = true
			case <-time.After(150 * time.Millisecond):
			}
			// Always unblock and join the operation before reporting a regression.
			_ = peer.Close()
			if !prompt {
				got = receiveResult(t, result)
			}
			require.True(t, prompt, "caller cancellation left a blocked socket writer running")
			require.ErrorIs(t, got, context.Canceled)
		})
	}
}

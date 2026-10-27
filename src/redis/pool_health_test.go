package redis

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mediocregopher/radix/v4"
	"github.com/stretchr/testify/require"
)

// Use the real pool and a socket-backed server: a context-aware fake would not
// expose a health probe that bypasses connection ownership or leaves I/O stuck.
func newHealthTestPool(t *testing.T, server *gatedRedis, size int, interval time.Duration) (radix.Client, *countingDialer) {
	t.Helper()
	dialer := &countingDialer{dialer: newNetDialer(time.Second, clientOptions{}, false, nil)}
	config := exclusivePoolConfig(radix.PoolConfig{
		Size:         size,
		PingInterval: interval,
		Dialer:       radix.Dialer{NetDialer: dialer, WriteFlushInterval: 150 * time.Microsecond},
	}, 0)
	client, err := config.New(context.Background(), "tcp", server.listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Close()
		require.Equal(t, int32(0), dialer.active.Load(), "pool cleanup must join all sockets")
	})
	expectedSize := size
	if expectedSize == 0 {
		expectedSize = 4 // The documented Radix default, independent of health.
	}
	require.Eventually(t, func() bool { return dialer.active.Load() == int32(expectedSize) }, time.Second, time.Millisecond)
	return client, dialer
}

func TestOwnedHealthConfiguredCadenceAndDisable(t *testing.T) {
	for _, interval := range []time.Duration{-1, -time.Second, 20 * time.Millisecond} {
		t.Run(interval.String(), func(t *testing.T) {
			server := newGatedRedis(t, nil)
			server.gatePing.Store(true)
			client, _ := newHealthTestPool(t, server, 1, interval)
			if interval > 0 {
				waitEntered(t, server, "PING")
			} else {
				select {
				case command := <-server.entered:
					t.Fatalf("disabled periodic health sent %s", command)
				case <-time.After(100 * time.Millisecond):
				}
				require.Zero(t, server.count("PING", ""))
			}
			require.NoError(t, client.Close())
		})
	}
}

func TestOwnedHealthDefaultCadenceRemainsEnabled(t *testing.T) {
	for _, tt := range []struct {
		name string
		size int
	}{
		{"ten_connections", 10},
		{"default_four_connections", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newGatedRedis(t, nil)
			server.gatePing.Store(true)
			client, _ := newHealthTestPool(t, server, tt.size, 0)
			// Default ten- and four-connection pools have 500ms and 1.25s
			// cadences. Permit scheduling delay, but catch a disabled loop.
			select {
			case command := <-server.entered:
				require.Equal(t, "PING", command)
			case <-time.After(2500 * time.Millisecond):
				t.Fatal("default health cadence never probed an idle connection")
			}
			require.NoError(t, client.Close())
		})
	}
}

func TestOwnedHealthDeadlineBoundsBlackholedProbeWithoutIOTimeout(t *testing.T) {
	server := newGatedRedis(t, nil)
	server.gatePing.Store(true)
	client, dialer := newHealthTestPool(t, server, 1, 20*time.Millisecond)
	waitEntered(t, server, "PING")
	// A second PING can arrive only after the first, unanswered probe has
	// expired, relinquished its socket and allowed the replacement to connect.
	select {
	case command := <-server.entered:
		require.Equal(t, "PING", command)
	case <-time.After(3 * time.Second):
		t.Fatal("health probe ignored its own deadline while Redis I/O timeout was disabled")
	}
	require.LessOrEqual(t, dialer.peak.Load(), int32(1), "health recovery must preserve the pool connection bound")
	require.NoError(t, client.Close())
}

func TestOwnedHealthPeerIsolationAndConcurrentClose(t *testing.T) {
	server := newGatedRedis(t, nil)
	server.gatePing.Store(true)
	client, dialer := newHealthTestPool(t, server, 2, 20*time.Millisecond)
	waitEntered(t, server, "PING")

	peerResult := make(chan error, 1)
	go func() {
		peerResult <- client.Do(context.Background(), radix.Cmd(new(uint64), "INCRBY", "long", "1"))
	}()
	waitEntered(t, server, "long")
	// Let the first health probe expire while the application owns the other
	// socket. A replacement probe proves the first cancellation has completed;
	// the peer must still own its original connection and unfinished operation.
	select {
	case command := <-server.entered:
		require.Equal(t, "PING", command)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out health probe did not release its connection")
	}
	select {
	case err := <-peerResult:
		t.Fatalf("health cancellation interrupted another connection's operation: %v", err)
	default:
	}

	shortContext, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	shortResult := make(chan error, 1)
	go func() {
		shortResult <- client.Do(shortContext, radix.Cmd(new(uint64), "INCRBY", "short", "1"))
	}()
	select {
	case err := <-shortResult:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(150 * time.Millisecond):
		t.Fatal("queued request cancellation blocked behind health probe I/O")
	}
	require.Zero(t, server.count("INCRBY", "short"), "canceled queue entry must never dispatch")
	select {
	case err := <-peerResult:
		t.Fatalf("another request's cancellation interrupted its active peer: %v", err)
	default:
	}

	queuedResult := make(chan error, 1)
	go func() {
		queuedResult <- client.Do(context.Background(), radix.Cmd(new(uint64), "INCRBY", "never", "1"))
	}()
	const closers = 8
	closeResults := make(chan error, closers)
	startClose := make(chan struct{})
	var closeWorkers sync.WaitGroup
	for i := 0; i < closers; i++ {
		closeWorkers.Add(1)
		go func() {
			defer closeWorkers.Done()
			<-startClose
			closeResults <- client.Close()
		}()
	}
	close(startClose)
	finished := make(chan struct{})
	go func() { closeWorkers.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("concurrent Close did not cancel and join the health probe and borrowers")
	}
	close(closeResults)
	successes := 0
	for err := range closeResults {
		if err == nil {
			successes++
		} else {
			require.True(t, errors.Is(err, errPoolClosed), "unexpected secondary Close error: %v", err)
		}
	}
	require.Equal(t, 1, successes, "exactly one caller owns pool shutdown")
	require.Error(t, receiveResult(t, peerResult))
	require.Error(t, receiveResult(t, queuedResult))
	require.Zero(t, server.count("INCRBY", "never"), "queued request dispatched during shutdown")
	require.Equal(t, int32(0), dialer.active.Load())
	require.LessOrEqual(t, dialer.peak.Load(), int32(2))
	require.ErrorIs(t, client.Do(context.Background(), radix.Cmd(nil, "PING")), errPoolClosed)
}

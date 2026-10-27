package redis

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mediocregopher/radix/v4"
	"github.com/stretchr/testify/require"
)

// Record the health loop's public client calls to check its finite budget and
// deadlines independently of socket scheduling. The real socket cancellation,
// isolation, and reconnect tests exercise the adapter beneath this boundary.
type healthSweepProbe struct {
	started, completed time.Time
	deadline           time.Time
	exclusive          bool
}

type healthSweepClient struct {
	mu                 sync.Mutex
	probes             []healthSweepProbe
	active, peak       int
	closeCalls         int
	started, completed chan int
	respond            func(context.Context, int) error
}

func newHealthSweepClient(respond func(context.Context, int) error) *healthSweepClient {
	return &healthSweepClient{started: make(chan int, 64), completed: make(chan int, 64), respond: respond}
}

func (c *healthSweepClient) Addr() net.Addr { return &net.TCPAddr{} }

func (c *healthSweepClient) Do(ctx context.Context, action radix.Action) error {
	deadline, _ := ctx.Deadline()
	c.mu.Lock()
	n := len(c.probes) + 1
	c.probes = append(c.probes, healthSweepProbe{
		started: time.Now(), deadline: deadline, exclusive: !action.Properties().CanShareConn,
	})
	c.active++
	if c.active > c.peak {
		c.peak = c.active
	}
	c.mu.Unlock()
	c.started <- n
	err := c.respond(ctx, n)
	c.mu.Lock()
	c.probes[n-1].completed = time.Now()
	c.active--
	c.mu.Unlock()
	c.completed <- n
	return err
}

func (c *healthSweepClient) Close() error {
	c.mu.Lock()
	c.closeCalls++
	c.mu.Unlock()
	return nil
}

func (c *healthSweepClient) snapshot() ([]healthSweepProbe, int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]healthSweepProbe(nil), c.probes...), c.peak, c.closeCalls
}

func startHealthSweep(t *testing.T, client *healthSweepClient, interval time.Duration, size int) *exclusivePool {
	t.Helper()
	lifetime, stop := context.WithCancel(context.Background())
	pool := &exclusivePool{
		Client: client, lifetime: lifetime, stop: stop,
		closeDone: make(chan struct{}), healthDone: make(chan struct{}),
	}
	go pool.checkHealth(interval, size)
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

func awaitHealthSweepProbe(t *testing.T, events <-chan int, want int) {
	t.Helper()
	select {
	case got := <-events:
		require.Equal(t, want, got)
	case <-time.After(3 * time.Second):
		t.Fatalf("health probe %d did not arrive", want)
	}
}

func requireNoHealthSweepProbe(t *testing.T, events <-chan int, duration time.Duration) {
	t.Helper()
	select {
	case got := <-events:
		t.Fatalf("unexpected extra health probe %d", got)
	case <-time.After(duration):
	}
}

func TestOwnedHealthSweepHasFiniteSequentialPoolBudget(t *testing.T) {
	for _, size := range []int{1, 4, 10} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			client := newHealthSweepClient(func(context.Context, int) error { return io.EOF })
			pool := startHealthSweep(t, client, 150*time.Millisecond, size)
			for n := 1; n <= size; n++ {
				awaitHealthSweepProbe(t, client.started, n)
				awaitHealthSweepProbe(t, client.completed, n)
			}
			// All failures may consume one probe per configured slot, never an
			// unbounded retry loop or a concurrent reconnect burst.
			requireNoHealthSweepProbe(t, client.started, 50*time.Millisecond)
			require.NoError(t, pool.Close())
			probes, peak, closes := client.snapshot()
			require.Len(t, probes, size)
			require.Equal(t, 1, peak)
			require.Equal(t, 1, closes)
			for _, probe := range probes {
				require.True(t, probe.exclusive, "health checks must use owned Do")
				require.False(t, probe.deadline.IsZero())
				require.Equal(t, probes[0].deadline, probe.deadline, "activation must share one deadline")
			}
		})
	}
}

func TestOwnedHealthSweepHealthyProbePreservesNormalCadence(t *testing.T) {
	client := newHealthSweepClient(func(context.Context, int) error { return nil })
	pool := startHealthSweep(t, client, 150*time.Millisecond, 10)
	awaitHealthSweepProbe(t, client.started, 1)
	awaitHealthSweepProbe(t, client.completed, 1)
	requireNoHealthSweepProbe(t, client.started, 50*time.Millisecond)
	awaitHealthSweepProbe(t, client.started, 2)
	awaitHealthSweepProbe(t, client.completed, 2)
	require.NoError(t, pool.Close())
	probes, peak, _ := client.snapshot()
	require.Len(t, probes, 2, "a healthy tick must issue one PING regardless of pool size")
	require.Equal(t, 1, peak)
}

func TestOwnedHealthSweepSharesWholeActivationDeadlineAndResetsTicker(t *testing.T) {
	client := newHealthSweepClient(func(ctx context.Context, n int) error {
		if n == 1 {
			// Consume a substantial part of the activation budget before the
			// failed first probe starts the remaining pool checks.
			timer := time.NewTimer(1100 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return io.EOF
			}
		}
		if n == 2 {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})
	pool := startHealthSweep(t, client, 150*time.Millisecond, 10)
	awaitHealthSweepProbe(t, client.started, 1)
	awaitHealthSweepProbe(t, client.completed, 1)
	awaitHealthSweepProbe(t, client.started, 2)
	awaitHealthSweepProbe(t, client.completed, 2)
	// Ticks buffered during the two-second activation must not immediately
	// start another activation after its deadline expires.
	requireNoHealthSweepProbe(t, client.started, 50*time.Millisecond)
	awaitHealthSweepProbe(t, client.started, 3)
	awaitHealthSweepProbe(t, client.completed, 3)
	require.NoError(t, pool.Close())
	probes, peak, _ := client.snapshot()
	require.Len(t, probes, 3)
	require.Equal(t, 1, peak)
	require.Equal(t, probes[0].deadline, probes[1].deadline)
	require.WithinDuration(t, probes[0].started.Add(2*time.Second), probes[1].completed, 500*time.Millisecond,
		"later probes must use the remaining activation budget, not a fresh two seconds each")
	require.True(t, probes[2].deadline.After(probes[1].deadline), "the next tick starts a new activation")
}

func TestOwnedHealthSweepCloseCancelsAndJoinsActiveProbe(t *testing.T) {
	canceled, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseProbe := func() { releaseOnce.Do(func() { close(release) }) }
	client := newHealthSweepClient(func(ctx context.Context, _ int) error {
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	})
	pool := startHealthSweep(t, client, 20*time.Millisecond, 10)
	// Run before the pool's cleanup if an assertion fails while its fake
	// transport is intentionally holding shutdown open.
	t.Cleanup(releaseProbe)
	awaitHealthSweepProbe(t, client.started, 1)
	closed := make(chan error, 1)
	go func() { closed <- pool.Close() }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the active health probe")
	}
	select {
	case err := <-closed:
		t.Fatalf("Close returned before its active health probe joined: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	releaseProbe()
	require.NoError(t, receiveResult(t, closed))
	probes, peak, closes := client.snapshot()
	require.Len(t, probes, 1, "shutdown must not start the rest of the sweep")
	require.Equal(t, 1, peak)
	require.Equal(t, 1, closes)
	select {
	case <-pool.healthDone:
	default:
		t.Fatal("Close returned before the health loop exited")
	}
}

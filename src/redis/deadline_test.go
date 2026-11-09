package redis

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipeDoReturnsWhenRequestDeadlineExpires(t *testing.T) {
	srv := newFakeRedis(t, nil)
	// A long I/O timeout: the request deadline must win.
	c, _ := newFakeRedisClient(t, srv.addr(), 2, false, nil, clientOptions{ioTimeout: 5 * time.Second})

	srv.setBlackhole(true)
	const calls = 50
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()

			rcv := uint64(42)
			start := time.Now()
			err := c.PipeDo(ctx, incrPipeline(c, &rcv))
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Less(t, time.Since(start), 500*time.Millisecond)
			assert.Equal(t, uint64(42), rcv, "receivers must not be written on error")
		}()
	}
	wg.Wait()
}

func TestPipeDoAbandonedCallNeverWritesReceivers(t *testing.T) {
	srv := newFakeRedis(t, nil)
	c, _ := newFakeRedisClient(t, srv.addr(), 1, false, nil, clientOptions{ioTimeout: 5 * time.Second})

	// The reply arrives after the caller gave up: the background call must
	// decode it into its private receiver, not the caller's (go test -race
	// flags it otherwise).
	srv.setDelay(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	rcv := uint64(42)
	require.Error(t, c.PipeDo(ctx, incrPipeline(c, &rcv)))
	for i := 0; i < 20; i++ {
		assert.Equal(t, uint64(42), rcv)
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPipeDoCommitsRepliesOnSuccess(t *testing.T) {
	srv := newFakeRedis(t, nil)
	c, _ := newFakeRedisClient(t, srv.addr(), 1, false, nil, clientOptions{ioTimeout: time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var rcv uint64
	require.NoError(t, c.PipeDo(ctx, incrPipeline(c, &rcv)))
	assert.Equal(t, uint64(1), rcv)

	var s string
	require.NoError(t, c.DoCmd(&s, "SET", "key", "value"))
	assert.Equal(t, "OK", s)
}

func TestPipeDoWithExpiredContextDoesNotTouchRedis(t *testing.T) {
	fakeClient := &recordingRedisClient{}
	c := &clientImpl{client: fakeClient}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.PipeDo(ctx, Pipeline{{Key: "a", Action: &testAction{key: "a"}}})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, fakeClient.callCount())
}

func TestExpiringContextsDoNotCloseHealthyConnections(t *testing.T) {
	srv := newFakeRedis(t, nil)
	c, store := newFakeRedisClient(t, srv.addr(), 4, false, nil, clientOptions{ioTimeout: time.Second})

	var wg sync.WaitGroup
	for w := 0; w < 20; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				if (w+i)%10 == 0 {
					// Arrives already expired.
					cancel()
				}
				var rcv uint64
				_ = c.PipeDo(ctx, incrPipeline(c, &rcv))
				cancel()
			}
		}(w)
	}
	wg.Wait()
	assert.Equal(t, uint64(0), store.NewCounter("cx_local_close").Value())
}

func TestMaxInflightFailsFast(t *testing.T) {
	srv := newFakeRedis(t, nil)
	c, _ := newFakeRedisClient(t, srv.addr(), 2, false, nil, clientOptions{ioTimeout: 300 * time.Millisecond, maxInflight: 2})

	srv.setBlackhole(true)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var rcv uint64
			_ = c.PipeDo(context.Background(), incrPipeline(c, &rcv))
		}()
	}
	require.Eventually(t, func() bool { return len(c.inflight) == 2 }, time.Second, 5*time.Millisecond)

	var rcv uint64
	start := time.Now()
	err := c.PipeDo(context.Background(), incrPipeline(c, &rcv))
	assert.True(t, errors.Is(err, ErrTooManyInflight))
	assert.Less(t, time.Since(start), 50*time.Millisecond)

	// Slots are released once the I/O timeout ends the blocked calls.
	wg.Wait()
	assert.Equal(t, 0, len(c.inflight))
}

func TestCopyReply(t *testing.T) {
	u := uint64(1)
	copyReply(&u, newUint64(7))
	assert.Equal(t, uint64(7), u)

	s := "old"
	reply := newReply(&s).(*string)
	*reply = "new"
	copyReply(&s, reply)
	assert.Equal(t, "new", s)

	assert.Nil(t, newReply(nil))
	copyReply(nil, nil)
}

func newUint64(v uint64) *uint64 { return &v }

func TestPoolOnEmptyErrorFailsFastWithoutConnections(t *testing.T) {
	srv := newFakeRedis(t, nil)
	store := stats.NewStore(stats.NewNullSink(), false)
	c := newClientImpl(context.Background(), store, false, "", "tcp", "single", srv.addr(), 2,
		0, 0, nil, false, nil, time.Second, "ERROR", "", 10*time.Millisecond, 100*time.Millisecond,
		time.Second, 1, false, nil, clientOptions{ioTimeout: 100 * time.Millisecond}).(*clientImpl)
	defer c.Close()

	var rcv uint64
	require.NoError(t, c.PipeDo(context.Background(), incrPipeline(c, &rcv)))

	// Redis goes away: once the pool has dropped its connections, commands
	// fail right away instead of waiting for a connection.
	srv.close()
	require.Eventually(t, func() bool {
		var rcv uint64
		_ = c.PipeDo(context.Background(), incrPipeline(c, &rcv))
		return c.NumActiveConns() == 0
	}, 5*time.Second, 10*time.Millisecond)

	start := time.Now()
	err := c.PipeDo(context.Background(), incrPipeline(c, &rcv))
	assert.ErrorIs(t, err, ErrNoConnection)
	assert.Less(t, time.Since(start), 10*time.Millisecond)
}

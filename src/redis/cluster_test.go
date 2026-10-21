package redis

import (
	"context"
	"testing"
	"time"

	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFakeRedisClusterClient(t *testing.T, addr string, parallelism int, opts clientOptions) *clientImpl {
	t.Helper()
	store := stats.NewStore(stats.NewNullSink(), false)
	c := newClientImpl(context.Background(), store, false, "", "tcp", "cluster", addr, 4,
		0, 0, nil, false, nil, time.Second, "", "", 10*time.Millisecond, 100*time.Millisecond,
		time.Second, parallelism, false, nil, opts).(*clientImpl)
	t.Cleanup(func() { c.Close() })
	return c
}

// multiKeyPipeline spans several keys, so cluster mode runs it as several
// groups, concurrently when parallelism allows.
func multiKeyPipeline(c Client, rcvs []uint64) Pipeline {
	var p Pipeline
	for i := range rcvs {
		key := string(rune('a' + i))
		p = c.PipeAppend(p, &rcvs[i], "INCRBY", key, 1)
		p = c.PipeAppend(p, nil, "EXPIRE", key, 60)
	}
	return p
}

func TestClusterClientFailsFastWhenServerStopsAnswering(t *testing.T) {
	for _, parallelism := range []int{1, 0} {
		srv := newFakeRedis(t, nil)
		ioTimeout := 200 * time.Millisecond
		c := newFakeRedisClusterClient(t, srv.addr(), parallelism, clientOptions{ioTimeout: ioTimeout})

		rcvs := make([]uint64, 3)
		require.NoError(t, c.PipeDo(context.Background(), multiKeyPipeline(c, rcvs)))
		assert.Equal(t, []uint64{1, 1, 1}, rcvs)

		srv.setBlackhole(true)
		start := time.Now()
		assert.Error(t, c.PipeDo(context.Background(), multiKeyPipeline(c, rcvs)), "parallelism %d", parallelism)
		// Serial groups may each wait for one I/O timeout.
		assert.Less(t, time.Since(start), time.Duration(len(rcvs)+2)*ioTimeout, "parallelism %d", parallelism)

		srv.setBlackhole(false)
		require.Eventually(t, func() bool {
			return c.PipeDo(context.Background(), multiKeyPipeline(c, rcvs)) == nil
		}, 10*time.Second, 50*time.Millisecond, "parallelism %d", parallelism)
	}
}

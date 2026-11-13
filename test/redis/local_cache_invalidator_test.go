package redis_test

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coocood/freecache"
	gostats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/settings"
)

func makeInvalidatorSettings(addr string) settings.Settings {
	var s settings.Settings
	s.RedisType = "SINGLE"
	s.RedisSocketType = "tcp"
	s.RedisUrl = addr
	s.RedisTimeout = 1 * time.Second
	return s
}

type invalidatorFixture struct {
	cache      *freecache.Cache
	guard      *limiter.LocalCacheGuard
	subscribed gostats.Gauge
	received   gostats.Counter
	deleted    gostats.Counter
}

func startInvalidator(t *testing.T, s settings.Settings) *invalidatorFixture {
	return startInvalidatorWithDial(t, s, func(ctx context.Context) (radix.Conn, error) {
		return radix.Dial(ctx, s.RedisSocketType, s.RedisUrl)
	})
}

func startInvalidatorWithDial(t *testing.T, s settings.Settings, dial func(context.Context) (radix.Conn, error)) *invalidatorFixture {
	cache := freecache.NewCache(1024)
	store := gostats.NewStore(gostats.NewNullSink(), false)
	f := &invalidatorFixture{
		cache:      cache,
		guard:      limiter.NewLocalCacheGuard(cache),
		subscribed: store.NewGauge("localcache.invalidation.subscribed"),
		received:   store.NewCounter("localcache.invalidation.received"),
		deleted:    store.NewCounter("localcache.invalidation.deleted"),
	}
	invalidator := redis.StartLocalCacheInvalidator(context.Background(), s, f.guard, store, dial)
	t.Cleanup(func() { invalidator.Close() })
	return f
}

func (f *invalidatorFixture) awaitSubscribed(t *testing.T, timeout time.Duration) {
	t.Helper()
	assert.Eventually(t, func() bool { return f.subscribed.Value() == 1 }, timeout, 10*time.Millisecond)
}

func (f *invalidatorFixture) cached(key string) bool {
	_, err := f.cache.Get([]byte(key))
	return err == nil
}

func (f *invalidatorFixture) awaitInvalidated(t *testing.T, key string) {
	t.Helper()
	assert.Eventually(t, func() bool { return !f.cached(key) }, 5*time.Second, 10*time.Millisecond)
}

func publish(t *testing.T, client redis.Client, key string) {
	t.Helper()
	assert.NoError(t, client.DoCmd(nil, "PUBLISH", redis.LocalCacheInvalidationChannel, key))
}

func TestLocalCacheInvalidatorConfigValidation(t *testing.T) {
	assert := assert.New(t)
	localCache := limiter.NewLocalCacheGuard(freecache.NewCache(1024))
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)

	assert.Panics(func() {
		redis.StartLocalCacheInvalidator(context.Background(), makeInvalidatorSettings("localhost:6379"), nil, statsStore, nil)
	}, "nil local cache")

	assert.Panics(func() {
		redis.StartLocalCacheInvalidator(context.Background(), makeInvalidatorSettings("localhost:6379"), localCache, statsStore, nil)
	}, "nil dial callback")
}

func TestLocalCacheInvalidator(t *testing.T) {
	assert := assert.New(t)
	redisSrv := mustNewRedisServer()
	defer redisSrv.Close()
	client := mkSingleRedisClient(redisSrv.Addr())
	defer client.Close()

	f := startInvalidator(t, makeInvalidatorSettings(redisSrv.Addr()))
	f.awaitSubscribed(t, 5*time.Second)

	assert.NoError(f.cache.Set([]byte("poisoned_key"), []byte{}, 60))
	publish(t, client, "poisoned_key")
	f.awaitInvalidated(t, "poisoned_key")
	assert.EqualValues(1, f.deleted.Value())

	publish(t, client, "unknown_key")
	assert.Eventually(func() bool { return f.received.Value() == 2 }, 5*time.Second, 10*time.Millisecond)
	assert.EqualValues(1, f.deleted.Value())
}

func TestLocalCacheInvalidatorClearsOnFirstSubscription(t *testing.T) {
	redisSrv := miniredis.RunT(t)
	allowConnect := make(chan struct{})
	f := startInvalidatorWithDial(t, makeInvalidatorSettings(redisSrv.Addr()), func(ctx context.Context) (radix.Conn, error) {
		select {
		case <-allowConnect:
			return radix.Dial(ctx, "tcp", redisSrv.Addr())
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	key := []byte("startup_key")
	require.NoError(t, f.cache.Set(key, nil, 60))
	snap := f.guard.GenSnapshot(key)
	close(allowConnect)
	f.awaitSubscribed(t, 5*time.Second)
	assert.False(t, f.cached(string(key)))
	inserted, err := f.guard.SetIfUnchanged(key, nil, 60, snap)
	require.NoError(t, err)
	assert.False(t, inserted)
}

func TestLocalCacheInvalidatorIsolatesPrefixes(t *testing.T) {
	redisSrv := miniredis.RunT(t)
	prefixes := []string{"", "prod:", "staging:"}
	fixtures := make([]*invalidatorFixture, len(prefixes))
	generations := make([]uint64, len(prefixes))
	for i, prefix := range prefixes {
		s := makeInvalidatorSettings(redisSrv.Addr())
		s.CacheKeyPrefix = prefix
		fixtures[i] = startInvalidator(t, s)
		fixtures[i].awaitSubscribed(t, 5*time.Second)
		generations[i] = fixtures[i].guard.GenSnapshot([]byte("poisoned_key"))
		// Use the same key to make an unintended cross-deployment eviction visible.
		require.NoError(t, fixtures[i].cache.Set([]byte("poisoned_key"), []byte{}, 60))
	}

	for i, prefix := range prefixes {
		require.Equal(t, 1, redisSrv.Publish(prefix+redis.LocalCacheInvalidationChannel, "poisoned_key"))
		fixtures[i].awaitInvalidated(t, "poisoned_key")
		for j := i + 1; j < len(fixtures); j++ {
			assert.True(t, fixtures[j].cached("poisoned_key"))
			assert.Equal(t, generations[j], fixtures[j].guard.GenSnapshot([]byte("poisoned_key")))
		}
	}
	for i, f := range fixtures {
		assert.EqualValues(t, 1, f.received.Value())
		assert.Equal(t, generations[i]+1, f.guard.GenSnapshot([]byte("poisoned_key")))
	}
}

func TestLocalCacheInvalidatorReconnects(t *testing.T) {
	assert := assert.New(t)
	redisSrv := mustNewRedisServer()
	addr := redisSrv.Addr()

	allowReconnect := make(chan struct{})
	first := true
	f := startInvalidatorWithDial(t, makeInvalidatorSettings(addr), func(ctx context.Context) (radix.Conn, error) {
		if !first {
			select {
			case <-allowReconnect:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		first = false
		return radix.Dial(ctx, "tcp", addr)
	})
	f.awaitSubscribed(t, 5*time.Second)
	assert.NoError(f.cache.Set([]byte("before_disconnect"), []byte{}, 60))

	redisSrv.Close()
	assert.Eventually(func() bool { return f.subscribed.Value() == 0 }, 10*time.Second, 10*time.Millisecond)
	assert.NoError(f.cache.Set([]byte("during_disconnect"), []byte{}, 60))
	snap := f.guard.GenSnapshot([]byte("during_disconnect"))

	restartedSrv := miniredis.NewMiniRedis()
	assert.NoError(restartedSrv.StartAddr(addr))
	defer restartedSrv.Close()
	assert.Equal(0, restartedSrv.Publish(redis.LocalCacheInvalidationChannel, "before_disconnect"))
	assert.True(f.cached("before_disconnect"))
	assert.True(f.cached("during_disconnect"))
	close(allowReconnect)
	f.awaitSubscribed(t, 30*time.Second)
	assert.False(f.cached("before_disconnect"))
	assert.False(f.cached("during_disconnect"))
	inserted, err := f.guard.SetIfUnchanged([]byte("during_disconnect"), nil, 60, snap)
	assert.NoError(err)
	assert.False(inserted)

	assert.NoError(f.cache.Set([]byte("poisoned_key"), []byte{}, 60))
	client := mkSingleRedisClient(addr)
	defer client.Close()
	publish(t, client, "poisoned_key")
	f.awaitInvalidated(t, "poisoned_key")
}

func TestLocalCacheInvalidatorRotatesSubscription(t *testing.T) {
	redisSrv := mustNewRedisServer()
	defer redisSrv.Close()
	client := mkSingleRedisClient(redisSrv.Addr())
	defer client.Close()

	s := makeInvalidatorSettings(redisSrv.Addr())
	s.LocalCacheInvalidationResubscribeInterval = 200 * time.Millisecond

	var dials atomic.Uint64
	f := startInvalidatorWithDial(t, s, func(ctx context.Context) (radix.Conn, error) {
		conn, err := radix.Dial(ctx, "tcp", s.RedisUrl)
		if err == nil {
			dials.Add(1)
		}
		return conn, err
	})
	f.awaitSubscribed(t, 5*time.Second)

	require.NoError(t, f.cache.Set([]byte("retained_key"), nil, 60))
	snap := f.guard.GenSnapshot([]byte("retained_key"))
	require.Eventually(t, func() bool {
		return dials.Load() >= 3 && f.subscribed.Value() == 1
	}, 5*time.Second, 10*time.Millisecond)
	assert.True(t, f.cached("retained_key"))
	assert.Equal(t, snap, f.guard.GenSnapshot([]byte("retained_key")))

	// The publish is retried: a message landing in the short unsubscribed gap
	// between sessions is legitimately dropped (at-most-once delivery).
	assert.NoError(t, f.cache.Set([]byte("poisoned_key"), []byte{}, 60))
	assert.Eventually(t, func() bool {
		publish(t, client, "poisoned_key")
		return f.received.Value() > 0 && !f.cached("poisoned_key")
	}, 5*time.Second, 50*time.Millisecond)
	f.awaitSubscribed(t, 5*time.Second)
}

func TestLocalCacheInvalidatorSuppressesRacedInsert(t *testing.T) {
	assert := assert.New(t)
	redisSrv := mustNewRedisServer()
	defer redisSrv.Close()
	client := mkSingleRedisClient(redisSrv.Addr())
	defer client.Close()

	f := startInvalidator(t, makeInvalidatorSettings(redisSrv.Addr()))
	f.awaitSubscribed(t, 5*time.Second)

	// A snapshot taken before the invalidation must not insert afterwards; a
	// fresh one must.
	snap := f.guard.GenSnapshot([]byte("poisoned_key"))
	publish(t, client, "poisoned_key")
	assert.Eventually(func() bool { return f.received.Value() == 1 }, 5*time.Second, 10*time.Millisecond)

	inserted, err := f.guard.SetIfUnchanged([]byte("poisoned_key"), []byte{}, 60, snap)
	assert.NoError(err)
	assert.False(inserted)
	assert.False(f.cached("poisoned_key"))

	inserted, err = f.guard.SetIfUnchanged([]byte("poisoned_key"), []byte{}, 60, f.guard.GenSnapshot([]byte("poisoned_key")))
	assert.NoError(err)
	assert.True(inserted)
	assert.True(f.cached("poisoned_key"))
}

func TestLocalCacheInvalidatorSkipsStalledEndpoint(t *testing.T) {
	stalled, conns := newStalledListener(t)
	defer stalled.Close()
	defer conns.closeAll()

	redisSrv := mustNewRedisServer()
	defer redisSrv.Close()

	s := makeInvalidatorSettings(redisSrv.Addr())
	s.RedisTimeout = 500 * time.Millisecond

	// Discovery is supplied by the driver; the invalidator must bound a stalled
	// PING and call the dialer again rather than staying on the first endpoint.
	first := true
	f := startInvalidatorWithDial(t, s, func(ctx context.Context) (radix.Conn, error) {
		addr := redisSrv.Addr()
		if first {
			first = false
			addr = stalled.Addr().String()
		}
		return radix.Dial(ctx, "tcp", addr)
	})
	f.awaitSubscribed(t, 10*time.Second)
}

// stalledConns retains accepted connections: an unreferenced net.Conn can be
// closed by its runtime finalizer, faking a timely failure.
type stalledConns struct {
	mu    sync.Mutex
	conns []net.Conn
}

func (s *stalledConns) add(c net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns = append(s.conns, c)
}

func (s *stalledConns) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		c.Close()
	}
}

// newStalledListener accepts TCP connections and never responds on them.
func newStalledListener(t *testing.T) (net.Listener, *stalledConns) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conns := &stalledConns{}
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			conns.add(c)
		}
	}()
	return listener, conns
}

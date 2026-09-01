package redis_test

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coocood/freecache"
	gostats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"

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
	cache := freecache.NewCache(1024)
	store := gostats.NewStore(gostats.NewNullSink(), false)
	f := &invalidatorFixture{
		cache:      cache,
		guard:      limiter.NewLocalCacheGuard(cache),
		subscribed: store.NewGauge("localcache.invalidation.subscribed"),
		received:   store.NewCounter("localcache.invalidation.received"),
		deleted:    store.NewCounter("localcache.invalidation.deleted"),
	}
	invalidator := redis.StartLocalCacheInvalidator(context.Background(), s, f.guard, store)
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
		redis.StartLocalCacheInvalidator(context.Background(), makeInvalidatorSettings("localhost:6379"), nil, statsStore)
	}, "nil local cache")

	badType := makeInvalidatorSettings("localhost:6379")
	badType.RedisType = "clustered"
	assert.Panics(func() {
		redis.StartLocalCacheInvalidator(context.Background(), badType, localCache, statsStore)
	}, "unrecognized redis type")

	badSentinel := makeInvalidatorSettings("mymaster")
	badSentinel.RedisType = "sentinel"
	assert.Panics(func() {
		redis.StartLocalCacheInvalidator(context.Background(), badSentinel, localCache, statsStore)
	}, "sentinel url without sentinel addresses")
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

func TestLocalCacheInvalidatorReconnects(t *testing.T) {
	assert := assert.New(t)
	redisSrv := mustNewRedisServer()
	addr := redisSrv.Addr()

	f := startInvalidator(t, makeInvalidatorSettings(addr))
	f.awaitSubscribed(t, 5*time.Second)

	redisSrv.Close()
	assert.Eventually(func() bool { return f.subscribed.Value() == 0 }, 10*time.Second, 10*time.Millisecond)

	restartedSrv := miniredis.NewMiniRedis()
	assert.NoError(restartedSrv.StartAddr(addr))
	defer restartedSrv.Close()
	f.awaitSubscribed(t, 30*time.Second)

	assert.NoError(f.cache.Set([]byte("poisoned_key"), []byte{}, 60))
	client := mkSingleRedisClient(addr)
	defer client.Close()
	publish(t, client, "poisoned_key")
	f.awaitInvalidated(t, "poisoned_key")
}

func TestLocalCacheInvalidatorClusterDialsTcp(t *testing.T) {
	redisSrv := mustNewRedisServer()
	defer redisSrv.Close()
	client := mkSingleRedisClient(redisSrv.Addr())
	defer client.Close()

	// REDIS_SOCKET_TYPE defaults to unix; cluster subscriptions must dial TCP
	// like radix's cluster client does.
	s := makeInvalidatorSettings(redisSrv.Addr())
	s.RedisType = "cluster"
	s.RedisSocketType = "unix"

	f := startInvalidator(t, s)
	f.awaitSubscribed(t, 5*time.Second)

	assert.NoError(t, f.cache.Set([]byte("poisoned_key"), []byte{}, 60))
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

	f := startInvalidator(t, s)
	f.awaitSubscribed(t, 5*time.Second)

	time.Sleep(600 * time.Millisecond)

	// The publish is retried: a message landing in the short unsubscribed gap
	// between sessions is legitimately dropped (at-most-once delivery).
	assert.NoError(t, f.cache.Set([]byte("poisoned_key"), []byte{}, 60))
	assert.Eventually(t, func() bool {
		publish(t, client, "poisoned_key")
		return !f.cached("poisoned_key")
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
	snap := f.guard.GenSnapshot()
	publish(t, client, "poisoned_key")
	assert.Eventually(func() bool { return f.received.Value() == 1 }, 5*time.Second, 10*time.Millisecond)

	inserted, err := f.guard.SetIfUnchanged([]byte("poisoned_key"), []byte{}, 60, snap)
	assert.NoError(err)
	assert.False(inserted)
	assert.False(f.cached("poisoned_key"))

	inserted, err = f.guard.SetIfUnchanged([]byte("poisoned_key"), []byte{}, 60, f.guard.GenSnapshot())
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

	s := makeInvalidatorSettings(stalled.Addr().String() + "," + redisSrv.Addr())
	s.RedisType = "cluster"
	s.RedisTimeout = 500 * time.Millisecond

	f := startInvalidator(t, s)
	f.awaitSubscribed(t, 10*time.Second)
}

func TestLocalCacheInvalidatorRotatesPastStalledSentinel(t *testing.T) {
	stalled, conns := newStalledListener(t)
	defer stalled.Close()
	defer conns.closeAll()

	redisSrv := mustNewRedisServer()
	defer redisSrv.Close()
	masterHost, masterPort, err := net.SplitHostPort(redisSrv.Addr())
	assert.NoError(t, err)

	sentinel := newFakeSentinel(t, masterHost, masterPort)
	defer sentinel.Close()

	s := makeInvalidatorSettings("mymaster," + stalled.Addr().String() + "," + sentinel.Addr().String())
	s.RedisType = "sentinel"
	s.RedisTimeout = 500 * time.Millisecond

	f := startInvalidator(t, s)
	f.awaitSubscribed(t, 15*time.Second)
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

// newFakeSentinel answers every connection with the given master address; the
// reply is written without reading the request and waits in the buffer.
func newFakeSentinel(t *testing.T, masterHost, masterPort string) net.Listener {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	reply := fmt.Sprintf("*2\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n",
		len(masterHost), masterHost, len(masterPort), masterPort)
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				_, _ = c.Write([]byte(reply))
			}(c)
		}
	}()
	return listener
}

package redis

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jpillora/backoff"
	gostats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	logger "github.com/sirupsen/logrus"

	"github.com/envoyproxy/ratelimit/src/assert"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/utils"
)

// Cache keys published here are evicted from every replica's local cache.
const LocalCacheInvalidationChannel = "ratelimit:local_cache_invalidation"

type localCacheInvalidationStats struct {
	subscribed gostats.Gauge
	received   gostats.Counter
	deleted    gostats.Counter
}

func newLocalCacheInvalidationStats(scope gostats.Scope) localCacheInvalidationStats {
	invalidationScope := scope.Scope("localcache").Scope("invalidation")
	return localCacheInvalidationStats{
		subscribed: invalidationScope.NewGauge("subscribed"),
		received:   invalidationScope.NewCounter("received"),
		deleted:    invalidationScope.NewCounter("deleted"),
	}
}

// LocalCacheInvalidator deletes cache keys published on the invalidation
// channel from this replica's local cache. Best-effort, at-most-once: a
// missed message leaves the stale entry until the window ends.
//
// It owns one raw connection instead of reusing the driver's clients: radix
// pub/sub wraps a single Conn (pooled clients cannot subscribe), and losing
// it must never take the process down — the loop reconnects forever.
type LocalCacheInvalidator struct {
	localCache *limiter.LocalCacheGuard
	stats      localCacheInvalidationStats

	redisType      string
	socketType     string
	url            string
	dialer         radix.Dialer
	sentinelDialer radix.Dialer

	// clusterAddrs is refreshed from the live topology on every (re)connect.
	clusterAddrs        []string
	sentinelMasterName  string
	sentinelAddrs       []string
	addrIndex           int
	resubscribeInterval time.Duration
	// An endpoint that accepts and then stalls must not wedge the reconnect
	// loop away from the remaining candidates.
	connectTimeout time.Duration

	backoff *backoff.Backoff
	cancel  context.CancelFunc
	done    chan struct{}
}

// StartLocalCacheInvalidator starts the eviction loop against the main Redis.
func StartLocalCacheInvalidator(ctx context.Context, s settings.Settings, localCache *limiter.LocalCacheGuard, scope gostats.Scope) *LocalCacheInvalidator {
	assert.Assert(localCache != nil)
	maskedUrl := utils.MaskCredentialsInUrl(s.RedisUrl)
	this := &LocalCacheInvalidator{
		localCache:          localCache,
		stats:               newLocalCacheInvalidationStats(scope),
		redisType:           strings.ToLower(s.RedisType),
		socketType:          s.RedisSocketType,
		url:                 s.RedisUrl,
		dialer:              createDialer(s.RedisTimeout, s.RedisTls, s.RedisTlsConfig, s.RedisAuth, maskedUrl),
		sentinelDialer:      createDialer(s.RedisTimeout, s.RedisTls, s.RedisTlsConfig, s.RedisSentinelAuth, fmt.Sprintf("sentinel(%s)", maskedUrl)),
		resubscribeInterval: s.LocalCacheInvalidationResubscribeInterval,
		backoff: &backoff.Backoff{
			Min:    time.Second,
			Max:    30 * time.Second,
			Factor: 2,
			Jitter: true,
		},
		done: make(chan struct{}),
	}
	if this.resubscribeInterval <= 0 {
		this.resubscribeInterval = 5 * time.Minute
	}
	this.connectTimeout = s.RedisTimeout
	if this.connectTimeout <= 0 {
		this.connectTimeout = 10 * time.Second
	}

	switch this.redisType {
	case "single":
	case "cluster":
		this.clusterAddrs = strings.Split(this.url, ",")
	case "sentinel":
		var err error
		this.sentinelMasterName, this.sentinelAddrs, err = parseSentinelUrl(this.url)
		if err != nil {
			panic(RedisError(err.Error()))
		}
	default:
		panic(RedisError("Unrecognized redis type " + this.redisType))
	}

	ctx, this.cancel = context.WithCancel(ctx)
	logger.Warnf("starting local cache invalidation subscriber on redis %s", maskedUrl)
	go this.run(ctx)
	return this
}

func (this *LocalCacheInvalidator) Close() error {
	this.cancel()
	<-this.done
	return nil
}

func (this *LocalCacheInvalidator) run(ctx context.Context) {
	defer close(this.done)
	for {
		err := this.subscribeAndConsume(ctx)
		if err != nil && ctx.Err() == nil {
			logger.Warnf("local cache invalidation subscription lost: %v", err)
		}
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			// Routine session rotation: reconnect immediately.
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(this.backoff.Duration()):
		}
	}
}

func (this *LocalCacheInvalidator) subscribeAndConsume(ctx context.Context) error {
	conn, err := this.connect(ctx)
	if err != nil {
		return err
	}

	sessionCtx, cancel := context.WithTimeout(ctx, this.resubscribeInterval)
	defer cancel()

	pubSubConn := radix.PubSubConfig{}.New(conn)
	defer pubSubConn.Close()

	if err := pubSubConn.Subscribe(sessionCtx, LocalCacheInvalidationChannel); err != nil {
		return err
	}
	this.backoff.Reset()
	this.stats.subscribed.Set(1)
	defer this.stats.subscribed.Set(0)
	logger.Debugf("subscribed to %s for local cache invalidation", LocalCacheInvalidationChannel)

	for {
		message, err := pubSubConn.Next(sessionCtx)
		if err != nil {
			if sessionCtx.Err() != nil && ctx.Err() == nil {
				logger.Debugf("rotating local cache invalidation subscription")
				return nil
			}
			return err
		}
		this.stats.received.Inc()
		if this.localCache.Invalidate(message.Message) {
			this.stats.deleted.Inc()
		}
	}
}

// connect establishes a confirmed connection within connectTimeout: radix's
// pub/sub Subscribe and Ping are flush-only, so the PING round trip is the
// only bounded proof that the endpoint responds.
func (this *LocalCacheInvalidator) connect(ctx context.Context) (radix.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, this.connectTimeout)
	defer cancel()
	conn, err := this.dial(ctx)
	if err != nil {
		return nil, err
	}
	if err := conn.Do(ctx, radix.Cmd(nil, "PING")); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (this *LocalCacheInvalidator) dial(ctx context.Context) (radix.Conn, error) {
	switch this.redisType {
	case "single":
		return this.dialer.Dial(ctx, this.socketType, this.url)
	case "cluster":
		// Any node works (cluster pub/sub is broadcast), always over TCP:
		// radix's cluster client ignores REDIS_SOCKET_TYPE (default "unix")
		// for its host:port seeds, and the subscriber must match.
		addr := this.clusterAddrs[this.addrIndex%len(this.clusterAddrs)]
		this.addrIndex++
		conn, err := this.dialer.Dial(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		this.refreshClusterAddrs(ctx, conn)
		return conn, nil
	case "sentinel":
		return this.dialSentinelMaster(ctx)
	default:
		return nil, fmt.Errorf("unrecognized redis type %s", this.redisType)
	}
}

// refreshClusterAddrs re-reads the topology so reconnects survive replacement
// of every bootstrap node; on failure the current list is kept.
func (this *LocalCacheInvalidator) refreshClusterAddrs(ctx context.Context, conn radix.Conn) {
	var topo radix.ClusterTopo
	if err := conn.Do(ctx, radix.Cmd(&topo, "CLUSTER", "SLOTS")); err != nil {
		logger.Warnf("could not refresh cluster topology for the invalidation subscriber: %v", err)
		return
	}
	if len(topo) == 0 {
		return
	}
	addrs := make([]string, 0, len(topo))
	for addr := range topo.Map() {
		addrs = append(addrs, addr)
	}
	this.clusterAddrs = addrs
}

func (this *LocalCacheInvalidator) dialSentinelMaster(ctx context.Context) (radix.Conn, error) {
	masterName := this.sentinelMasterName

	// A stalled sentinel consumes the whole connect budget; rotating the
	// start keeps it from starving the healthy ones behind it.
	start := this.addrIndex
	this.addrIndex++

	var lastErr error
	for i := range this.sentinelAddrs {
		sentinelUrl := this.sentinelAddrs[(start+i)%len(this.sentinelAddrs)]
		sentinelConn, err := this.sentinelDialer.Dial(ctx, "tcp", sentinelUrl)
		if err != nil {
			lastErr = err
			continue
		}

		var masterAddr []string
		err = sentinelConn.Do(ctx, radix.Cmd(&masterAddr, "SENTINEL", "GET-MASTER-ADDR-BY-NAME", masterName))
		sentinelConn.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if len(masterAddr) != 2 {
			lastErr = fmt.Errorf("sentinel did not resolve a master address for %s", masterName)
			continue
		}

		return this.dialer.Dial(ctx, "tcp", net.JoinHostPort(masterAddr[0], masterAddr[1]))
	}
	return nil, fmt.Errorf("could not resolve master %s through any sentinel: %w", masterName, lastErr)
}

package redis

import (
	"context"
	"fmt"
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

// LocalCacheInvalidationChannel is the channel suffix, prepended with
// CACHE_KEY_PREFIX to isolate deployments sharing Redis.
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
// channel from this replica's local cache. Delivery is best-effort, at-most-once;
// every confirmed subscription clears the cache to cover missed publications.
//
// It uses the driver's dialer and current topology for a dedicated raw Conn:
// pooled clients cannot subscribe. The loop provides nonfatal retries with
// backoff and rotation of healthy-looking but obsolete subscriptions.
type LocalCacheInvalidator struct {
	localCache *limiter.LocalCacheGuard
	stats      localCacheInvalidationStats
	channel    string

	dial                func(context.Context) (radix.Conn, error)
	resubscribeInterval time.Duration
	// An endpoint that accepts and then stalls must not wedge the reconnect
	// loop away from the remaining candidates.
	connectTimeout time.Duration

	backoff *backoff.Backoff
	cancel  context.CancelFunc
	done    chan struct{}
}

// StartLocalCacheInvalidator starts the eviction loop against the main Redis.
func StartLocalCacheInvalidator(ctx context.Context, s settings.Settings, localCache *limiter.LocalCacheGuard, scope gostats.Scope,
	dial func(context.Context) (radix.Conn, error),
) *LocalCacheInvalidator {
	assert.Assert(localCache != nil)
	assert.Assert(dial != nil)
	maskedUrl := utils.MaskCredentialsInUrl(s.RedisUrl)
	this := &LocalCacheInvalidator{
		localCache:          localCache,
		stats:               newLocalCacheInvalidationStats(scope),
		channel:             s.CacheKeyPrefix + LocalCacheInvalidationChannel,
		dial:                dial,
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

	pubSubConn := radix.PubSubConfig{}.New(conn)
	defer pubSubConn.Close()

	if err := this.subscribe(ctx, conn, pubSubConn); err != nil {
		return err
	}
	this.localCache.Clear()
	sessionCtx, cancel := context.WithTimeout(ctx, this.resubscribeInterval)
	defer cancel()

	this.backoff.Reset()
	this.stats.subscribed.Set(1)
	defer this.stats.subscribed.Set(0)
	logger.Debugf("subscribed to %s for local cache invalidation", this.channel)

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

// subscribe waits for Redis to acknowledge the channel before the session is
// considered live. Radix's Subscribe only flushes the command, and Next discards
// subscription acknowledgements, so read the first response directly from Conn.
func (this *LocalCacheInvalidator) subscribe(ctx context.Context, conn radix.Conn, pubSubConn radix.PubSubConn) error {
	ctx, cancel := context.WithTimeout(ctx, this.connectTimeout)
	defer cancel()
	if err := pubSubConn.Subscribe(ctx, this.channel); err != nil {
		return err
	}
	// Both RESP2 arrays and RESP3 push frames decode into a slice. This is a
	// fresh connection subscribing to exactly one channel, so the count is 1.
	var ack []string
	if err := conn.EncodeDecode(ctx, nil, &ack); err != nil {
		return fmt.Errorf("confirming local cache invalidation subscription: %w", err)
	}
	if len(ack) != 3 || ack[0] != "subscribe" || ack[1] != this.channel || ack[2] != "1" {
		return fmt.Errorf("unexpected local cache invalidation subscription acknowledgement: %q", ack)
	}
	return nil
}

// connect bounds dialing and verifies the connection with a normal PING round
// trip. The subscription acknowledgement is checked separately.
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

package redis

import (
	"context"
	"io"
	"math/rand"
	"time"

	"github.com/coocood/freecache"

	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
	"github.com/envoyproxy/ratelimit/src/utils"
)

func NewRateLimiterCacheImplFromSettings(ctx context.Context, s settings.Settings, localCache *freecache.Cache, srv server.Server, timeSource utils.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64, statsManager stats.Manager) (limiter.RateLimitCache, io.Closer) {
	closer := &utils.MultiCloser{}
	var perSecondPool Client
	if s.RedisPerSecond {
		perSecondPool = newClientImpl(ctx, srv.Scope().Scope("redis_per_second_pool"), s.RedisPerSecondTls, s.RedisPerSecondAuth, s.RedisPerSecondSocketType,
			s.RedisPerSecondType, s.RedisPerSecondUrl, s.RedisPerSecondPoolSize, s.RedisPerSecondPipelineWindow, s.RedisPerSecondPipelineLimit, s.RedisTlsConfig, s.RedisHealthCheckActiveConnection, srv, s.RedisPerSecondTimeout,
			s.RedisPerSecondPoolOnEmptyBehavior, s.RedisPerSecondSentinelAuth,
			s.RedisStartupInitialInterval, s.RedisStartupMaxInterval, s.RedisStartupMaxElapsedTime,
			s.RedisPerSecondClusterPipelineParallelism,
			s.RedisCloseConnectionOnReadOnlyError,
			newCredentialProviderFromSettings(ctx, s, true),
			clientOptions{ioTimeout: ioTimeout(s.RedisPerSecondIoTimeout, s.RedisPerSecondTimeout)})
		closer.Closers = append(closer.Closers, perSecondPool)
	}

	otherPool := newClientImpl(ctx, srv.Scope().Scope("redis_pool"), s.RedisTls, s.RedisAuth, s.RedisSocketType, s.RedisType, s.RedisUrl, s.RedisPoolSize,
		s.RedisPipelineWindow, s.RedisPipelineLimit, s.RedisTlsConfig, s.RedisHealthCheckActiveConnection, srv, s.RedisTimeout,
		s.RedisPoolOnEmptyBehavior, s.RedisSentinelAuth,
		s.RedisStartupInitialInterval, s.RedisStartupMaxInterval, s.RedisStartupMaxElapsedTime,
		s.RedisClusterPipelineParallelism,
		s.RedisCloseConnectionOnReadOnlyError,
		newCredentialProviderFromSettings(ctx, s, false),
		clientOptions{ioTimeout: ioTimeout(s.RedisIoTimeout, s.RedisTimeout)})
	closer.Closers = append(closer.Closers, otherPool)

	return NewFixedRateLimitCacheImpl(
		otherPool,
		perSecondPool,
		timeSource,
		jitterRand,
		expirationJitterMaxSeconds,
		localCache,
		s.NearLimitRatio,
		s.CacheKeyPrefix,
		statsManager,
		s.StopCacheKeyIncrementWhenOverlimit,
		s.UseCalendarMonthRateLimit,
	), closer
}

// ioTimeout returns the I/O timeout for a Redis client: the explicit
// REDIS_IO_TIMEOUT when set, otherwise REDIS_TIMEOUT.
func ioTimeout(explicit *time.Duration, timeout time.Duration) time.Duration {
	if explicit != nil {
		return *explicit
	}
	return timeout
}

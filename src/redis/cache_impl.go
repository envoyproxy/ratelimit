package redis

import (
	"context"
	"math/rand"

	"github.com/coocood/freecache"

	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
	"github.com/envoyproxy/ratelimit/src/utils"
)

func NewRateLimiterCacheImplFromSettings(ctx context.Context, s settings.Settings, localCache *freecache.Cache, srv server.Server,
	timeSource utils.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64, statsManager stats.Manager) limiter.RateLimitCache {
	var perSecondPool Client
	if s.RedisPerSecond {
		perSecondPool = NewClientImpl(ctx, srv.Scope().Scope("redis_per_second_pool"), s.RedisPerSecondTls, s.RedisPerSecondAuth, s.RedisPerSecondSocketType,
			s.RedisPerSecondType, s.RedisPerSecondUrl, s.RedisPerSecondPoolSize, s.RedisImplicitPipeline, s.RedisTlsConfig, s.RedisHealthCheckActiveConnection, srv)
	}

	otherPool := NewClientImpl(ctx, srv.Scope().Scope("redis_pool"), s.RedisTls, s.RedisAuth, s.RedisSocketType, s.RedisType, s.RedisUrl, s.RedisPoolSize,
		s.RedisImplicitPipeline, s.RedisTlsConfig, s.RedisHealthCheckActiveConnection, srv)

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
	)
}

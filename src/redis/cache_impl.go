package redis

import (
	"math/rand"

	"github.com/coocood/freecache"

	"github.com/zackzhangverkada/ratelimit/src/limiter"
	"github.com/zackzhangverkada/ratelimit/src/server"
	"github.com/zackzhangverkada/ratelimit/src/settings"
	"github.com/zackzhangverkada/ratelimit/src/stats"
	"github.com/zackzhangverkada/ratelimit/src/utils"
)

func NewRateLimiterCacheImplFromSettings(s settings.Settings, localCache *freecache.Cache, srv server.Server, timeSource utils.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64, statsManager stats.Manager) limiter.RateLimitCache {
	var perSecondPool Client
	if s.RedisPerSecond {
		perSecondPool = NewClientImpl(srv.Scope().Scope("redis_per_second_pool"), s.RedisPerSecondTls, s.RedisPerSecondAuth, s.RedisPerSecondSocketType,
			s.RedisPerSecondType, s.RedisPerSecondUrl, s.RedisPerSecondPoolSize, s.RedisPerSecondPipelineWindow, s.RedisPerSecondPipelineLimit, s.RedisTlsConfig, s.RedisHealthCheckActiveConnection, srv)
	}
	var otherPool Client
	otherPool = NewClientImpl(srv.Scope().Scope("redis_pool"), s.RedisTls, s.RedisAuth, s.RedisSocketType, s.RedisType, s.RedisUrl, s.RedisPoolSize,
		s.RedisPipelineWindow, s.RedisPipelineLimit, s.RedisTlsConfig, s.RedisHealthCheckActiveConnection, srv)

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
	)
}

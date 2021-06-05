package redis

import (
	"math/rand"

	"github.com/coocood/freecache"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
	"github.com/envoyproxy/ratelimit/src/utils"

	storage_factory "github.com/envoyproxy/ratelimit/src/storage/factory"
	storage_strategy "github.com/envoyproxy/ratelimit/src/storage/strategy"
)

func NewRateLimiterCacheImplFromSettings(s settings.Settings, localCache *freecache.Cache, srv server.Server, timeSource utils.TimeSource, jitterRand *rand.Rand, statsManager stats.Manager) limiter.RateLimitCache {
	var perSecondPool storage_strategy.StorageStrategy
	if s.RedisPerSecond {
		perSecondPool = storage_factory.NewRedis(srv.Scope().Scope("redis_per_second_pool"), s.RedisPerSecondTls, s.RedisPerSecondAuth,
			s.RedisPerSecondType, s.RedisPerSecondUrl, s.RedisPerSecondPoolSize, s.RedisPerSecondPipelineWindow, s.RedisPerSecondPipelineLimit)
	}
	otherPool := storage_factory.NewRedis(srv.Scope().Scope("redis_pool"), s.RedisTls, s.RedisAuth, s.RedisType, s.RedisUrl, s.RedisPoolSize,
		s.RedisPipelineWindow, s.RedisPipelineLimit)

	return NewFixedRateLimitCacheImpl(
		otherPool,
		perSecondPool,
		timeSource,
		jitterRand,
		localCache,
		s.ExpirationJitterMaxSeconds,
		s.NearLimitRatio,
		s.CacheKeyPrefix,
		statsManager,
	)
}

package redis

import (
	"fmt"
	"math/rand"

	"github.com/coocood/freecache"

	"github.com/envoyproxy/ratelimit/src/algorithm"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/redis/driver"
	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/utils"
)

func NewRateLimiterCacheImplFromSettings(s settings.Settings, localCache *freecache.Cache, srv server.Server, timeSource utils.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64) (limiter.RateLimitCache, error) {
	var perSecondPool driver.Client
	if s.RedisPerSecond {
		perSecondPool = driver.NewClientImpl(srv.Scope().Scope("redis_per_second_pool"), s.RedisPerSecondTls, s.RedisPerSecondAuth,
			s.RedisPerSecondType, s.RedisPerSecondUrl, s.RedisPerSecondPoolSize, s.RedisPipelineWindow, s.RedisPipelineLimit)
	}
	var otherPool driver.Client
	otherPool = driver.NewClientImpl(srv.Scope().Scope("redis_pool"), s.RedisTls, s.RedisAuth, s.RedisType, s.RedisUrl, s.RedisPoolSize,
		s.RedisPipelineWindow, s.RedisPipelineLimit)

	if s.RateLimitAlgorithm == settings.FixedRateLimit {
		ratelimitAlgorithm := algorithm.NewFixedWindowAlgorithm(
			timeSource,
			localCache,
			s.NearLimitRatio,
		)
		return NewFixedRateLimitCacheImpl(
			otherPool,
			perSecondPool,
			timeSource,
			jitterRand,
			expirationJitterMaxSeconds,
			localCache,
			s.NearLimitRatio,
			ratelimitAlgorithm), nil
	}
	if s.RateLimitAlgorithm == settings.WindowedRateLimit {
		ratelimitAlgorithm := algorithm.NewRollingWindowAlgorithm(
			timeSource,
			localCache,
			s.NearLimitRatio,
		)
		return NewWindowedRateLimitCacheImpl(
			otherPool,
			perSecondPool,
			timeSource,
			jitterRand,
			expirationJitterMaxSeconds,
			localCache,
			s.NearLimitRatio,
			ratelimitAlgorithm), nil
	}
	return nil, fmt.Errorf("Unknown rate limit algorithm. %s\n", s.RateLimitAlgorithm)
}

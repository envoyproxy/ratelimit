package redis

import (
	"math/rand"

	"github.com/coocood/freecache"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/settings"
	logger "github.com/sirupsen/logrus"
)

func NewRateLimiterCacheImplFromSettings(s settings.Settings, localCache *freecache.Cache, srv server.Server, timeSource utils.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64) limiter.RateLimitCache {
	var perSecondPool Client
	if s.RedisPerSecond {
		perSecondPool = NewClientImpl(srv.Scope().Scope("redis_per_second_pool"), s.RedisPerSecondTls, s.RedisPerSecondAuth,
			s.RedisPerSecondType, s.RedisPerSecondUrl, s.RedisPerSecondPoolSize, s.RedisPipelineWindow, s.RedisPipelineLimit)
	}
	var otherPool Client
	otherPool = NewClientImpl(srv.Scope().Scope("redis_pool"), s.RedisTls, s.RedisAuth, s.RedisType, s.RedisUrl, s.RedisPoolSize,
		s.RedisPipelineWindow, s.RedisPipelineLimit)

	if s.RateLimitAlgorithm == settings.FixedRateLimit {
		return NewFixedRateLimitCacheImpl(
			otherPool,
			perSecondPool,
			timeSource,
			jitterRand,
			expirationJitterMaxSeconds,
			localCache,
			s.NearLimitRatio)
	} else if s.RateLimitAlgorithm == settings.WindowedRateLimit {
		return NewWindowedRateLimitCacheImpl(
			otherPool,
			perSecondPool,
			timeSource,
			jitterRand,
			expirationJitterMaxSeconds,
			localCache,
			s.NearLimitRatio)
	} else {
		logger.Fatalf("Unknown rate limit algorithm. %s\n", s.RateLimitAlgorithm)
	}
	return nil
}

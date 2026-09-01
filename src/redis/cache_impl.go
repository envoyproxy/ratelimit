package redis

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"

	"github.com/coocood/freecache"

	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
	"github.com/envoyproxy/ratelimit/src/utils"
)

func NewRateLimiterCacheImplFromSettings(ctx context.Context, s settings.Settings, localCache *freecache.Cache, srv server.Server, timeSource utils.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64, statsManager stats.Manager) (limiter.RateLimitCache, io.Closer) {
	redisAuth, err := redisAuthFromFile(s.RedisAuth, s.RedisAuthFile, "REDIS_AUTH", "REDIS_AUTH_FILE")
	if err != nil {
		panic(err)
	}
	var redisPerSecondAuth string
	if s.RedisPerSecond {
		redisPerSecondAuth, err = redisAuthFromFile(s.RedisPerSecondAuth, s.RedisPerSecondAuthFile, "REDIS_PERSECOND_AUTH", "REDIS_PERSECOND_AUTH_FILE")
		if err != nil {
			panic(err)
		}
	}

	closer := &utils.MultiCloser{}
	var perSecondPool Client
	if s.RedisPerSecond {
		perSecondPool = newClientImpl(ctx, srv.Scope().Scope("redis_per_second_pool"), s.RedisPerSecondTls, redisPerSecondAuth, s.RedisPerSecondSocketType,
			s.RedisPerSecondType, s.RedisPerSecondUrl, s.RedisPerSecondPoolSize, s.RedisPerSecondPipelineWindow, s.RedisPerSecondPipelineLimit, s.RedisTlsConfig, s.RedisHealthCheckActiveConnection, srv, s.RedisPerSecondTimeout,
			s.RedisPerSecondPoolOnEmptyBehavior, s.RedisPerSecondSentinelAuth,
			s.RedisStartupInitialInterval, s.RedisStartupMaxInterval, s.RedisStartupMaxElapsedTime,
			s.RedisPerSecondClusterPipelineParallelism,
			s.RedisCloseConnectionOnReadOnlyError)
		closer.Closers = append(closer.Closers, perSecondPool)
	}

	otherPool := newClientImpl(ctx, srv.Scope().Scope("redis_pool"), s.RedisTls, redisAuth, s.RedisSocketType, s.RedisType, s.RedisUrl, s.RedisPoolSize,
		s.RedisPipelineWindow, s.RedisPipelineLimit, s.RedisTlsConfig, s.RedisHealthCheckActiveConnection, srv, s.RedisTimeout,
		s.RedisPoolOnEmptyBehavior, s.RedisSentinelAuth,
		s.RedisStartupInitialInterval, s.RedisStartupMaxInterval, s.RedisStartupMaxElapsedTime,
		s.RedisClusterPipelineParallelism,
		s.RedisCloseConnectionOnReadOnlyError)
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

func redisAuthFromFile(inlineAuth, authFile, inlineEnv, fileEnv string) (string, error) {
	if inlineAuth != "" && authFile != "" {
		return "", fmt.Errorf("%s and %s cannot both be set", inlineEnv, fileEnv)
	}
	if authFile == "" {
		return inlineAuth, nil
	}

	contents, err := os.ReadFile(authFile)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileEnv, err)
	}
	auth := strings.TrimRight(string(contents), "\r\n")
	if auth == "" {
		return "", fmt.Errorf("%s must not reference an empty file", fileEnv)
	}
	return auth, nil
}

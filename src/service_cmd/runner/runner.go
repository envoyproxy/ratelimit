package runner

import (
	"io"
	"math/rand"
	"net/http"
	"time"

	stats "github.com/lyft/gostats"

	"github.com/coocood/freecache"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v2"
	pb_legacy "github.com/envoyproxy/ratelimit/proto/ratelimit"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/server"
	ratelimit "github.com/envoyproxy/ratelimit/src/service"
	"github.com/envoyproxy/ratelimit/src/settings"
	logger "github.com/sirupsen/logrus"
)

type Runner struct {
	statsStore stats.Store
}

func NewRunner() Runner {
	return Runner{stats.NewDefaultStore()}
}

func (runner *Runner) GetStatsStore() stats.Store {
	return runner.statsStore
}

func (runner *Runner) Run() {
	s := settings.NewSettings()

	logLevel, err := logger.ParseLevel(s.LogLevel)
	if err != nil {
		logger.Fatalf("Could not parse log level. %v\n", err)
	} else {
		logger.SetLevel(logLevel)
	}
	var localCache *freecache.Cache
	if s.LocalCacheSizeInBytes != 0 {
		localCache = freecache.NewCache(s.LocalCacheSizeInBytes)
	}

	srv := server.NewServer("ratelimit", runner.statsStore, localCache, settings.GrpcUnaryInterceptor(nil))

	var perSecondPool redis.Pool
	if s.RedisPerSecond {
		perSecondPool = redis.NewPoolImpl(srv.Scope().Scope("redis_per_second_pool"), s.RedisPerSecondTls, s.RedisPerSecondAuth, s.RedisPerSecondUrl, s.RedisPerSecondPoolSize)
	}
	var otherPool redis.Pool
	otherPool = redis.NewPoolImpl(srv.Scope().Scope("redis_pool"), s.RedisTls, s.RedisAuth, s.RedisUrl, s.RedisPoolSize)

	service := ratelimit.NewService(
		srv.Runtime(),
		redis.NewRateLimitCacheImpl(
			otherPool,
			perSecondPool,
			redis.NewTimeSourceImpl(),
			rand.New(redis.NewLockedSource(time.Now().Unix())),
			s.ExpirationJitterMaxSeconds,
			localCache),
		config.NewRateLimitConfigLoaderImpl(),
		srv.Scope().Scope("service"))

	srv.AddDebugHttpEndpoint(
		"/rlconfig",
		"print out the currently loaded configuration for debugging",
		func(writer http.ResponseWriter, request *http.Request) {
			io.WriteString(writer, service.GetCurrentConfig().Dump())
		})

	// Ratelimit is compatible with two proto definitions
	// 1. data-plane-api rls.proto: https://github.com/envoyproxy/data-plane-api/blob/master/envoy/service/ratelimit/v2/rls.proto
	pb.RegisterRateLimitServiceServer(srv.GrpcServer(), service)
	// 2. ratelimit.proto defined in this repository: https://github.com/envoyproxy/ratelimit/blob/0ded92a2af8261d43096eba4132e45b99a3b8b14/proto/ratelimit/ratelimit.proto
	pb_legacy.RegisterRateLimitServiceServer(srv.GrpcServer(), service.GetLegacyService())
	// (1) is the current definition, and (2) is the legacy definition.

	srv.Start()
}

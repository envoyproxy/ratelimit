package runner

import (
	"io"
	"math/rand"
	"net/http"
	"time"

	stats "github.com/lyft/gostats"

	"github.com/coocood/freecache"

	pb_legacy "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v2"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
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

	service := ratelimit.NewService(
		srv.Runtime(),
		redis.NewRateLimiterCacheImplFromSettings(
			s,
			localCache,
			srv,
			limiter.NewTimeSourceImpl(),
			rand.New(limiter.NewLockedSource(time.Now().Unix())),
			s.ExpirationJitterMaxSeconds),
		config.NewRateLimitConfigLoaderImpl(),
		srv.Scope().Scope("service"),
		s.RuntimeWatchRoot,
	)

	srv.AddDebugHttpEndpoint(
		"/rlconfig",
		"print out the currently loaded configuration for debugging",
		func(writer http.ResponseWriter, request *http.Request) {
			io.WriteString(writer, service.GetCurrentConfig().Dump())
		})

	srv.AddJsonHandler(service)

	// Ratelimit is compatible with two proto definitions
	// 1. data-plane-api v3 rls.proto: https://github.com/envoyproxy/data-plane-api/blob/master/envoy/service/ratelimit/v3/rls.proto
	pb.RegisterRateLimitServiceServer(srv.GrpcServer(), service)
	// 1. data-plane-api v2 rls.proto: https://github.com/envoyproxy/data-plane-api/blob/master/envoy/service/ratelimit/v2/rls.proto
	pb_legacy.RegisterRateLimitServiceServer(srv.GrpcServer(), service.GetLegacyService())
	// (1) is the current definition, and (2) is the legacy definition.

	srv.Start()
}

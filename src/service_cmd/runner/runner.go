package runner

import (
	"context"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	stats "github.com/lyft/gostats"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"

	"github.com/coocood/freecache"

	pb_legacy "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v2"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"

	"github.com/replicon/ratelimit/src/config"
	"github.com/replicon/ratelimit/src/limiter"
	"github.com/replicon/ratelimit/src/redis"
	"github.com/replicon/ratelimit/src/server"
	ratelimit "github.com/replicon/ratelimit/src/service"
	"github.com/replicon/ratelimit/src/settings"
	logger "github.com/sirupsen/logrus"
)

type Runner struct {
	statsStore stats.Store
}

var (
	shadowModeEnabled = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "rate_limiting_shadow_mode_enabled",
		Help: "Indicates whether shadow mode is enabled",
	})
)

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
	if strings.ToLower(s.LogFormat) == "json" {
		logger.SetFormatter(&logger.JSONFormatter{
			TimestampFormat: time.RFC3339Nano,
			FieldMap: logger.FieldMap{
				logger.FieldKeyTime: "@timestamp",
				logger.FieldKeyMsg:  "@message",
			},
		})
	}

	var localCache *freecache.Cache
	if s.LocalCacheSizeInBytes != 0 {
		localCache = freecache.NewCache(s.LocalCacheSizeInBytes)
	}
	var loggingHandler grpc.UnaryServerInterceptor
	loggingHandler = func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		req_id := uuid.New()
		logger.WithFields(logger.Fields{"req_id": req_id, "method": info.FullMethod, "req": req}).Trace("incoming gRPC request")

		resp, err := handler(ctx, req)

		if err != nil {
			logger.WithFields(logger.Fields{"req_id": req_id, "method": info.FullMethod, "req": req, "err": err}).Error("incoming gRPC request error")
		} else {
			logger.WithFields(logger.Fields{"req_id": req_id, "method": info.FullMethod, "req": req, "resp": resp}).Trace("incoming gRPC request success")
		}

		return resp, err
	}
	srv := server.NewServer("ratelimit", runner.statsStore, localCache, settings.GrpcUnaryInterceptor(loggingHandler))
	if s.ShadowMode {
		logger.Info("Shadow Mode Enabled")
		shadowModeEnabled.Set(1)
	}
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
		s.ShadowMode,
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

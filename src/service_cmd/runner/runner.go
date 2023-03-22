package runner

import (
	"context"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/envoyproxy/ratelimit/src/metrics"
	"github.com/envoyproxy/ratelimit/src/stats"
	"github.com/envoyproxy/ratelimit/src/trace"

	gostats "github.com/lyft/gostats"

	"github.com/coocood/freecache"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"

	logger "github.com/sirupsen/logrus"

	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/memcached"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/server"
	ratelimit "github.com/envoyproxy/ratelimit/src/service"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/utils"
)

type Runner struct {
	statsManager stats.Manager
	settings     settings.Settings
	srv          server.Server
	mu           sync.Mutex
}

func NewRunner(s settings.Settings) Runner {
	return Runner{
		statsManager: stats.NewStatManager(gostats.NewDefaultStore(), s),
		settings:     s,
	}
}

func (runner *Runner) GetStatsStore() gostats.Store {
	return runner.statsManager.GetStatsStore()
}

func createLimiter(srv server.Server, s settings.Settings, localCache *freecache.Cache, statsManager stats.Manager) limiter.RateLimitCache {
	switch s.BackendType {
	case "redis", "":
		return redis.NewRateLimiterCacheImplFromSettings(
			s,
			localCache,
			srv,
			utils.NewTimeSourceImpl(),
			rand.New(utils.NewLockedSource(time.Now().Unix())),
			s.ExpirationJitterMaxSeconds,
			statsManager,
		)
	case "memcache":
		return memcached.NewRateLimitCacheImplFromSettings(
			s,
			utils.NewTimeSourceImpl(),
			rand.New(utils.NewLockedSource(time.Now().Unix())),
			localCache,
			srv.Scope(),
			statsManager)
	default:
		logger.Fatalf("Invalid setting for BackendType: %s", s.BackendType)
		panic("This line should not be reachable")
	}
}

func (runner *Runner) Run() {
	s := runner.settings
	if s.TracingEnabled {
		tp := trace.InitProductionTraceProvider(s.TracingExporterProtocol, s.TracingServiceName, s.TracingServiceNamespace, s.TracingServiceInstanceId, s.TracingSamplingRate)
		defer func() {
			if err := tp.Shutdown(context.Background()); err != nil {
				logger.Printf("Error shutting down tracer provider: %v", err)
			}
		}()
	} else {
		logger.Infof("Tracing disabled")
	}

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

	serverReporter := metrics.NewServerReporter(runner.statsManager.GetStatsStore().ScopeWithTags("ratelimit_server", s.ExtraTags))

	srv := server.NewServer(s, "ratelimit", runner.statsManager, localCache, settings.GrpcUnaryInterceptor(serverReporter.UnaryServerInterceptor()))
	runner.mu.Lock()
	runner.srv = srv
	runner.mu.Unlock()

	service := ratelimit.NewService(
		createLimiter(srv, s, localCache, runner.statsManager),
		srv.Provider(),
		runner.statsManager,
		srv.HealthChecker(),
		utils.NewTimeSourceImpl(),
		s.GlobalShadowMode,
		s.ForceStartWithoutInitialConfig,
		s.HealthyWithAtLeastOneConfigLoaded,
	)

	srv.AddDebugHttpEndpoint(
		"/rlconfig",
		"print out the currently loaded configuration for debugging",
		func(writer http.ResponseWriter, request *http.Request) {
			if current, _ := service.GetCurrentConfig(); current != nil {
				io.WriteString(writer, current.Dump())
			}
		})

	srv.AddJsonHandler(service)

	// Ratelimit is compatible with the below proto definition
	// data-plane-api v3 rls.proto: https://github.com/envoyproxy/data-plane-api/blob/master/envoy/service/ratelimit/v3/rls.proto
	// v2 proto is no longer supported
	pb.RegisterRateLimitServiceServer(srv.GrpcServer(), service)

	srv.Start()
}

func (runner *Runner) Stop() {
	runner.mu.Lock()
	srv := runner.srv
	runner.mu.Unlock()
	if srv != nil {
		srv.Stop()
	}
}

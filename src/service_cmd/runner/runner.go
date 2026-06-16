package runner

import (
	"context"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/envoyproxy/ratelimit/src/filter"
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
	statsManager   stats.Manager
	settings       settings.Settings
	srv            server.Server
	filterProvider filter.Provider
	mu             sync.Mutex
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

	filterProvider := buildFilterProvider(s, runner.statsManager.GetStatsStore())
	runner.mu.Lock()
	runner.filterProvider = filterProvider
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
		filterProvider,
		s.ForceFlag,
		s.OnlyLogOnLimit,
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
	fp := runner.filterProvider
	runner.mu.Unlock()
	if srv != nil {
		srv.Stop()
	}
	if fp != nil {
		// Releases the fsnotify watcher fd + watch goroutine for the file
		// provider; no-op for the static (env-var) fallback. Without this,
		// in-process restart patterns or test harnesses leak a watcher per
		// Run/Stop cycle.
		fp.Stop()
	}
}

// buildFilterProvider returns the filter.Provider used by the rate-limit
// service. When FILTER_CONFIG_PATH is set, filters hot-reload from the file
// at that path (typically a Kubernetes ConfigMap mount); otherwise filters
// come from the BLACKLIST_*/WHITELIST_* env vars at startup and stay static
// — fully backward compatible with deployments that haven't adopted the
// file-based config.
//
// Initial load failure with FILTER_CONFIG_PATH set is treated as a hard
// startup error (panic) — the same contract used for malformed CIDR env
// vars in settings.NewSettings().
func buildFilterProvider(s settings.Settings, store gostats.Store) filter.Provider {
	if s.FilterConfigPath == "" {
		return filter.NewStaticProvider(s.IPFilter, s.UIDFilter)
	}
	p, err := filter.NewFileProvider(s.FilterConfigPath, store.Scope("filter"), logger.StandardLogger())
	if err != nil {
		panic(err)
	}
	return p
}

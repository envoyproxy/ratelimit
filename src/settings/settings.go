package settings

import (
	"time"

	"github.com/kelseyhightower/envconfig"
	"google.golang.org/grpc"
)

type Settings struct {
	// runtime options
	GrpcUnaryInterceptor grpc.ServerOption
	// env config
	Port                         int           `envconfig:"PORT" default:"8080"`
	GrpcPort                     int           `envconfig:"GRPC_PORT" default:"8081"`
	DebugPort                    int           `envconfig:"DEBUG_PORT" default:"6070"`
	UseStatsd                    bool          `envconfig:"USE_STATSD" default:"true"`
	StatsdHost                   string        `envconfig:"STATSD_HOST" default:"localhost"`
	StatsdPort                   int           `envconfig:"STATSD_PORT" default:"8125"`
	RuntimePath                  string        `envconfig:"RUNTIME_ROOT" default:"/srv/runtime_data/current"`
	RuntimeSubdirectory          string        `envconfig:"RUNTIME_SUBDIRECTORY"`
	RuntimeIgnoreDotFiles        bool          `envconfig:"RUNTIME_IGNOREDOTFILES" default:"false"`
	RuntimeWatchRoot             bool          `envconfig:"RUNTIME_WATCH_ROOT" default:"true"`
	LogLevel                     string        `envconfig:"LOG_LEVEL" default:"WARN"`
	LogFormat                    string        `envconfig:"LOG_FORMAT" default:"text"`
	RedisSocketType              string        `envconfig:"REDIS_SOCKET_TYPE" default:"unix"`
	RedisType                    string        `envconfig:"REDIS_TYPE" default:"SINGLE"`
	RedisUrl                     string        `envconfig:"REDIS_URL" default:"/var/run/nutcracker/ratelimit.sock"`
	RedisPoolSize                int           `envconfig:"REDIS_POOL_SIZE" default:"10"`
	RedisAuth                    string        `envconfig:"REDIS_AUTH" default:""`
	RedisTls                     bool          `envconfig:"REDIS_TLS" default:"false"`
	RedisPipelineWindow          time.Duration `envconfig:"REDIS_PIPELINE_WINDOW" default:"0"`
	RedisPipelineLimit           int           `envconfig:"REDIS_PIPELINE_LIMIT" default:"0"`
	RedisPerSecond               bool          `envconfig:"REDIS_PERSECOND" default:"false"`
	RedisPerSecondSocketType     string        `envconfig:"REDIS_PERSECOND_SOCKET_TYPE" default:"unix"`
	RedisPerSecondType           string        `envconfig:"REDIS_PERSECOND_TYPE" default:"SINGLE"`
	RedisPerSecondUrl            string        `envconfig:"REDIS_PERSECOND_URL" default:"/var/run/nutcracker/ratelimitpersecond.sock"`
	RedisPerSecondPoolSize       int           `envconfig:"REDIS_PERSECOND_POOL_SIZE" default:"10"`
	RedisPerSecondAuth           string        `envconfig:"REDIS_PERSECOND_AUTH" default:""`
	RedisPerSecondTls            bool          `envconfig:"REDIS_PERSECOND_TLS" default:"false"`
	RedisPerSecondPipelineWindow time.Duration `envconfig:"REDIS_PERSECOND_PIPELINE_WINDOW" default:"0"`
	RedisPerSecondPipelineLimit  int           `envconfig:"REDIS_PERSECOND_PIPELINE_LIMIT" default:"0"`
	ExpirationJitterMaxSeconds   int64         `envconfig:"EXPIRATION_JITTER_MAX_SECONDS" default:"300"`
	LocalCacheSizeInBytes        int           `envconfig:"LOCAL_CACHE_SIZE_IN_BYTES" default:"0"`
	NearLimitRatio               float32       `envconfig:"NEAR_LIMIT_RATIO" default:"0.8"`
	MemcacheHostPort             string        `envconfig:"MEMCACHE_HOST_PORT" default:""`
	BackendType                  string        `envconfig:"BACKEND_TYPE" default:"redis"`
}

type Option func(*Settings)

func NewSettings() Settings {
	var s Settings

	err := envconfig.Process("", &s)
	if err != nil {
		panic(err)
	}

	return s
}

func GrpcUnaryInterceptor(i grpc.UnaryServerInterceptor) Option {
	return func(s *Settings) {
		s.GrpcUnaryInterceptor = grpc.UnaryInterceptor(i)
	}
}

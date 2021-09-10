package settings

import (
	"crypto/tls"
	"time"

	"github.com/kelseyhightower/envconfig"
	"google.golang.org/grpc"
)

type Settings struct {
	// runtime options
	GrpcUnaryInterceptor grpc.ServerOption
	// Server listen address config
	Host      string `envconfig:"HOST" default:"0.0.0.0"`
	Port      int    `envconfig:"PORT" default:"8080"`
	GrpcHost  string `envconfig:"GRPC_HOST" default:"0.0.0.0"`
	GrpcPort  int    `envconfig:"GRPC_PORT" default:"8081"`
	DebugHost string `envconfig:"DEBUG_HOST" default:"0.0.0.0"`
	DebugPort int    `envconfig:"DEBUG_PORT" default:"6070"`

	// GRPC server settings
	// GrpcMaxConnectionAge is a duration for the maximum amount of time a connection may exist before it will be closed by sending a GoAway.
	// A random jitter of +/-10% will be added to MaxConnectionAge to spread out connection storms.
	GrpcMaxConnectionAge time.Duration `envconfig:"GRPC_MAX_CONNECTION_AGE" default:"24h" description:"Duration a connection may exist before it will be closed by sending a GoAway."`
	// GrpcMaxConnectionAgeGrace is an additive period after MaxConnectionAge after which the connection will be forcibly closed.
	GrpcMaxConnectionAgeGrace time.Duration `envconfig:"GRPC_MAX_CONNECTION_AGE_GRACE" default:"1h" description:"Period after MaxConnectionAge after which the connection will be forcibly closed."`

	// Logging settings
	LogLevel  string `envconfig:"LOG_LEVEL" default:"WARN"`
	LogFormat string `envconfig:"LOG_FORMAT" default:"text"`

	// Stats-related settings
	UseStatsd  bool              `envconfig:"USE_STATSD" default:"true"`
	StatsdHost string            `envconfig:"STATSD_HOST" default:"localhost"`
	StatsdPort int               `envconfig:"STATSD_PORT" default:"8125"`
	ExtraTags  map[string]string `envconfig:"EXTRA_TAGS" default:""`

	// Settings for rate limit configuration
	RuntimePath           string `envconfig:"RUNTIME_ROOT" default:"/srv/runtime_data/current"`
	RuntimeSubdirectory   string `envconfig:"RUNTIME_SUBDIRECTORY"`
	RuntimeIgnoreDotFiles bool   `envconfig:"RUNTIME_IGNOREDOTFILES" default:"false"`
	RuntimeWatchRoot      bool   `envconfig:"RUNTIME_WATCH_ROOT" default:"true"`

	// Settings for all cache types
	ExpirationJitterMaxSeconds int64   `envconfig:"EXPIRATION_JITTER_MAX_SECONDS" default:"300"`
	LocalCacheSizeInBytes      int     `envconfig:"LOCAL_CACHE_SIZE_IN_BYTES" default:"0"`
	NearLimitRatio             float32 `envconfig:"NEAR_LIMIT_RATIO" default:"0.8"`
	CacheKeyPrefix             string  `envconfig:"CACHE_KEY_PREFIX" default:""`
	BackendType                string  `envconfig:"BACKEND_TYPE" default:"redis"`

	// Redis settings
	RedisSocketType string `envconfig:"REDIS_SOCKET_TYPE" default:"unix"`
	RedisType       string `envconfig:"REDIS_TYPE" default:"SINGLE"`
	RedisUrl        string `envconfig:"REDIS_URL" default:"/var/run/nutcracker/ratelimit.sock"`
	RedisPoolSize   int    `envconfig:"REDIS_POOL_SIZE" default:"10"`
	RedisAuth       string `envconfig:"REDIS_AUTH" default:""`
	RedisTls        bool   `envconfig:"REDIS_TLS" default:"false"`
	// TODO: Make this setting configurable out of the box instead of having to provide it through code.
	RedisTlsConfig *tls.Config

	// RedisPipelineWindow sets the duration after which internal pipelines will be flushed.
	// If window is zero then implicit pipelining will be disabled. Radix use 150us for the
	// default value, see https://github.com/mediocregopher/radix/blob/v3.5.1/pool.go#L278.
	RedisPipelineWindow time.Duration `envconfig:"REDIS_PIPELINE_WINDOW" default:"0"`
	// RedisPipelineLimit sets maximum number of commands that can be pipelined before flushing.
	// If limit is zero then no limit will be used and pipelines will only be limited by the specified time window.
	RedisPipelineLimit       int    `envconfig:"REDIS_PIPELINE_LIMIT" default:"0"`
	RedisPerSecond           bool   `envconfig:"REDIS_PERSECOND" default:"false"`
	RedisPerSecondSocketType string `envconfig:"REDIS_PERSECOND_SOCKET_TYPE" default:"unix"`
	RedisPerSecondType       string `envconfig:"REDIS_PERSECOND_TYPE" default:"SINGLE"`
	RedisPerSecondUrl        string `envconfig:"REDIS_PERSECOND_URL" default:"/var/run/nutcracker/ratelimitpersecond.sock"`
	RedisPerSecondPoolSize   int    `envconfig:"REDIS_PERSECOND_POOL_SIZE" default:"10"`
	RedisPerSecondAuth       string `envconfig:"REDIS_PERSECOND_AUTH" default:""`
	RedisPerSecondTls        bool   `envconfig:"REDIS_PERSECOND_TLS" default:"false"`
	// RedisPerSecondPipelineWindow sets the duration after which internal pipelines will be flushed for per second redis.
	// See comments of RedisPipelineWindow for details.
	RedisPerSecondPipelineWindow time.Duration `envconfig:"REDIS_PERSECOND_PIPELINE_WINDOW" default:"0"`
	// RedisPerSecondPipelineLimit sets maximum number of commands that can be pipelined before flushing for per second redis.
	// See comments of RedisPipelineLimit for details.
	RedisPerSecondPipelineLimit int `envconfig:"REDIS_PERSECOND_PIPELINE_LIMIT" default:"0"`

	// Memcache settings
	MemcacheHostPort []string `envconfig:"MEMCACHE_HOST_PORT" default:""`
	// MemcacheMaxIdleConns sets the maximum number of idle TCP connections per memcached node.
	// The default is 2 as that is the default of the underlying library. This is the maximum
	// number of connections to memcache kept idle in pool, if a connection is needed but none
	// are idle a new connection is opened, used and closed and can be left in a time-wait state
	// which can result in high CPU usage.
	MemcacheMaxIdleConns int           `envconfig:"MEMCACHE_MAX_IDLE_CONNS" default:"2"`
	MemcacheSrv          string        `envconfig:"MEMCACHE_SRV" default:""`
	MemcacheSrvRefresh   time.Duration `envconfig:"MEMCACHE_SRV_REFRESH" default:"0"`
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

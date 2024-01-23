package settings

import (
	"crypto/tls"
	"time"

	"github.com/kelseyhightower/envconfig"
	"google.golang.org/grpc"

	"github.com/envoyproxy/ratelimit/src/utils"
)

type Settings struct {
	// runtime options
	// This value shall be imported into unary server interceptor in order to enable chaining
	GrpcUnaryInterceptor grpc.UnaryServerInterceptor
	// Server listen address config
	Host      string `envconfig:"HOST" default:"0.0.0.0"`
	Port      int    `envconfig:"PORT" default:"8080"`
	DebugHost string `envconfig:"DEBUG_HOST" default:"0.0.0.0"`
	DebugPort int    `envconfig:"DEBUG_PORT" default:"6070"`

	// GRPC server settings
	GrpcHost string `envconfig:"GRPC_HOST" default:"0.0.0.0"`
	GrpcPort int    `envconfig:"GRPC_PORT" default:"8081"`
	// GrpcServerTlsConfig configures grpc for the server
	GrpcServerTlsConfig *tls.Config
	// GrpcMaxConnectionAge is a duration for the maximum amount of time a connection may exist before it will be closed by sending a GoAway.
	// A random jitter of +/-10% will be added to MaxConnectionAge to spread out connection storms.
	GrpcMaxConnectionAge time.Duration `envconfig:"GRPC_MAX_CONNECTION_AGE" default:"24h" description:"Duration a connection may exist before it will be closed by sending a GoAway."`
	// GrpcMaxConnectionAgeGrace is an additive period after MaxConnectionAge after which the connection will be forcibly closed.
	GrpcMaxConnectionAgeGrace time.Duration `envconfig:"GRPC_MAX_CONNECTION_AGE_GRACE" default:"1h" description:"Period after MaxConnectionAge after which the connection will be forcibly closed."`
	// GrpcServerUseTLS enables gprc connections to server over TLS
	GrpcServerUseTLS bool `envconfig:"GRPC_SERVER_USE_TLS" default:"false"`
	// Allow to set the server certificate and key for TLS connections.
	// 	GrpcServerTlsCert is the path to the file containing the server cert chain
	GrpcServerTlsCert string `envconfig:"GRPC_SERVER_TLS_CERT" default:""`
	// 	GrpcServerTlsKey is the path to the file containing the server private key
	GrpcServerTlsKey string `envconfig:"GRPC_SERVER_TLS_KEY" default:""`
	// GrpcClientTlsCACert is the path to the file containing the client CA certificate.
	// Use for validating client certificate
	GrpcClientTlsCACert string `envconfig:"GRPC_CLIENT_TLS_CACERT" default:""`
	// GrpcClientTlsSAN is the SAN to validate from the client cert during mTLS auth
	GrpcClientTlsSAN string `envconfig:"GRPC_CLIENT_TLS_SAN" default:""`
	// Logging settings
	LogLevel  string `envconfig:"LOG_LEVEL" default:"WARN"`
	LogFormat string `envconfig:"LOG_FORMAT" default:"text"`

	// Rate limit configuration
	// ConfigType is the method of configuring rate limits. Possible values "FILE", "GRPC_XDS_SOTW".
	ConfigType string `envconfig:"CONFIG_TYPE" default:"FILE"`
	// ForceStartWithoutInitialConfig enables start the server without initial rate limit config event
	ForceStartWithoutInitialConfig bool `envconfig:"FORCE_START_WITHOUT_INITIAL_CONFIG" default:"false"`

	// xDS rate limit configuration
	// ConfigGrpcXdsNodeId is the Node ID. xDS server should set snapshots to this Node ID
	ConfigGrpcXdsNodeId                     string            `envconfig:"CONFIG_GRPC_XDS_NODE_ID" default:"default"`
	ConfigGrpcXdsNodeMetadata               string            `envconfig:"CONFIG_GRPC_XDS_NODE_METADATA" default:""` // eg: "key1:val1,key2=val2"
	ConfigGrpcXdsServerUrl                  string            `envconfig:"CONFIG_GRPC_XDS_SERVER_URL" default:"localhost:18000"`
	ConfigGrpcXdsServerConnectRetryInterval time.Duration     `envconfig:"CONFIG_GRPC_XDS_SERVER_CONNECT_RETRY_INTERVAL" default:"3s"`
	ConfigGrpcXdsClientAdditionalHeaders    map[string]string `envconfig:"CONFIG_GRPC_XDS_CLIENT_ADDITIONAL_HEADERS" default:""`

	// xDS config server TLS configurations
	ConfigGrpcXdsTlsConfig       *tls.Config
	ConfigGrpcXdsServerUseTls    bool   `envconfig:"CONFIG_GRPC_XDS_SERVER_USE_TLS" default:"false"`
	ConfigGrpcXdsClientTlsCert   string `envconfig:"CONFIG_GRPC_XDS_CLIENT_TLS_CERT" default:""`
	ConfigGrpcXdsClientTlsKey    string `envconfig:"CONFIG_GRPC_XDS_CLIENT_TLS_KEY" default:""`
	ConfigGrpcXdsServerTlsCACert string `envconfig:"CONFIG_GRPC_XDS_SERVER_TLS_CACERT" default:""`
	// GrpcClientTlsSAN is the SAN to validate from the client cert during mTLS auth
	ConfigGrpcXdsServerTlsSAN string `envconfig:"CONFIG_GRPC_XDS_SERVER_TLS_SAN" default:""`

	// Stats-related settings
	UseStatsd  bool              `envconfig:"USE_STATSD" default:"true"`
	StatsdHost string            `envconfig:"STATSD_HOST" default:"localhost"`
	StatsdPort int               `envconfig:"STATSD_PORT" default:"8125"`
	ExtraTags  map[string]string `envconfig:"EXTRA_TAGS" default:""`

	// Settings for rate limit configuration
	RuntimePath           string `envconfig:"RUNTIME_ROOT" default:"/srv/runtime_data/current"`
	RuntimeSubdirectory   string `envconfig:"RUNTIME_SUBDIRECTORY"`
	RuntimeAppDirectory   string `envconfig:"RUNTIME_APPDIRECTORY" default:"config"`
	RuntimeIgnoreDotFiles bool   `envconfig:"RUNTIME_IGNOREDOTFILES" default:"false"`
	RuntimeWatchRoot      bool   `envconfig:"RUNTIME_WATCH_ROOT" default:"true"`

	// Settings for all cache types
	ExpirationJitterMaxSeconds         int64   `envconfig:"EXPIRATION_JITTER_MAX_SECONDS" default:"300"`
	LocalCacheSizeInBytes              int     `envconfig:"LOCAL_CACHE_SIZE_IN_BYTES" default:"0"`
	NearLimitRatio                     float32 `envconfig:"NEAR_LIMIT_RATIO" default:"0.8"`
	CacheKeyPrefix                     string  `envconfig:"CACHE_KEY_PREFIX" default:""`
	BackendType                        string  `envconfig:"BACKEND_TYPE" default:"redis"`
	StopCacheKeyIncrementWhenOverlimit bool    `envconfig:"STOP_CACHE_KEY_INCREMENT_WHEN_OVERLIMIT" default:"false"`

	// Settings for optional returning of custom headers
	RateLimitResponseHeadersEnabled bool `envconfig:"LIMIT_RESPONSE_HEADERS_ENABLED" default:"false"`
	// value: the current limit
	HeaderRatelimitLimit string `envconfig:"LIMIT_LIMIT_HEADER" default:"RateLimit-Limit"`
	// value: remaining count
	HeaderRatelimitRemaining string `envconfig:"LIMIT_REMAINING_HEADER" default:"RateLimit-Remaining"`
	// value: remaining seconds
	HeaderRatelimitReset string `envconfig:"LIMIT_RESET_HEADER" default:"RateLimit-Reset"`

	// Health-check settings
	HealthyWithAtLeastOneConfigLoaded bool `envconfig:"HEALTHY_WITH_AT_LEAST_ONE_CONFIG_LOADED" default:"false"`

	// Redis settings
	RedisSocketType string `envconfig:"REDIS_SOCKET_TYPE" default:"unix"`
	RedisType       string `envconfig:"REDIS_TYPE" default:"SINGLE"`
	RedisUrl        string `envconfig:"REDIS_URL" default:"/var/run/nutcracker/ratelimit.sock"`
	RedisPoolSize   int    `envconfig:"REDIS_POOL_SIZE" default:"10"`
	RedisAuth       string `envconfig:"REDIS_AUTH" default:""`
	RedisTls        bool   `envconfig:"REDIS_TLS" default:"false"`
	// TODO: Make this setting configurable out of the box instead of having to provide it through code.
	RedisTlsConfig *tls.Config
	// Allow to set the client certificate and key for TLS connections.
	RedisTlsClientCert               string `envconfig:"REDIS_TLS_CLIENT_CERT" default:""`
	RedisTlsClientKey                string `envconfig:"REDIS_TLS_CLIENT_KEY" default:""`
	RedisTlsCACert                   string `envconfig:"REDIS_TLS_CACERT" default:""`
	RedisTlsSkipHostnameVerification bool   `envconfig:"REDIS_TLS_SKIP_HOSTNAME_VERIFICATION" default:"false"`

	// RedisImplicitPipeline if implicit pipelining for redis should be enabled
	RedisImplicitPipeline    bool   `envconfig:"REDIS_IMPLICIT_PIPELINE" default:"true"`
	RedisPerSecond           bool   `envconfig:"REDIS_PERSECOND" default:"false"`
	RedisPerSecondSocketType string `envconfig:"REDIS_PERSECOND_SOCKET_TYPE" default:"unix"`
	RedisPerSecondType       string `envconfig:"REDIS_PERSECOND_TYPE" default:"SINGLE"`
	RedisPerSecondUrl        string `envconfig:"REDIS_PERSECOND_URL" default:"/var/run/nutcracker/ratelimitpersecond.sock"`
	RedisPerSecondPoolSize   int    `envconfig:"REDIS_PERSECOND_POOL_SIZE" default:"10"`
	RedisPerSecondAuth       string `envconfig:"REDIS_PERSECOND_AUTH" default:""`
	RedisPerSecondTls        bool   `envconfig:"REDIS_PERSECOND_TLS" default:"false"`
	// Enable healthcheck to check Redis Connection. If there is no active connection, healthcheck failed.
	RedisHealthCheckActiveConnection bool `envconfig:"REDIS_HEALTH_CHECK_ACTIVE_CONNECTION" default:"false"`
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

	// Should the ratelimiting be running in Global shadow-mode, ie. never report a ratelimit status, unless a rate was provided from envoy as an override
	GlobalShadowMode bool `envconfig:"SHADOW_MODE" default:"false"`

	// Allow merging of multiple yaml files referencing the same domain
	MergeDomainConfigurations bool `envconfig:"MERGE_DOMAIN_CONFIG" default:"false"`

	// OTLP trace settings
	TracingEnabled           bool   `envconfig:"TRACING_ENABLED" default:"false"`
	TracingServiceName       string `envconfig:"TRACING_SERVICE_NAME" default:"RateLimit"`
	TracingServiceNamespace  string `envconfig:"TRACING_SERVICE_NAMESPACE" default:""`
	TracingServiceInstanceId string `envconfig:"TRACING_SERVICE_INSTANCE_ID" default:""`
	// can only be http or gRPC
	TracingExporterProtocol string `envconfig:"TRACING_EXPORTER_PROTOCOL" default:"http"`
	// detailed setting of exporter should refer to https://opentelemetry.io/docs/reference/specification/protocol/exporter/, e.g. OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_CERTIFICATE, OTEL_EXPORTER_OTLP_TIMEOUT
	// TracingSamplingRate defaults to 1 which amounts to using the `AlwaysSample` sampler
	TracingSamplingRate float64 `envconfig:"TRACING_SAMPLING_RATE" default:"1"`
}

type Option func(*Settings)

func NewSettings() Settings {
	var s Settings
	if err := envconfig.Process("", &s); err != nil {
		panic(err)
	}
	// When we require TLS to connect to Redis, we check if we need to connect using the provided key-pair.
	RedisTlsConfig(s.RedisTls || s.RedisPerSecondTls)(&s)
	GrpcServerTlsConfig()(&s)
	ConfigGrpcXdsServerTlsConfig()(&s)
	return s
}

func RedisTlsConfig(redisTls bool) Option {
	return func(s *Settings) {
		// Golang copy-by-value causes the RootCAs to no longer be nil
		// which isn't the expected default behavior of continuing to use system roots
		// so let's just initialize to what we want the correct value to be.
		s.RedisTlsConfig = &tls.Config{}
		if redisTls {
			s.RedisTlsConfig = utils.TlsConfigFromFiles(s.RedisTlsClientCert, s.RedisTlsClientKey, s.RedisTlsCACert, utils.ServerCA, s.RedisTlsSkipHostnameVerification)
		}
	}
}

func GrpcServerTlsConfig() Option {
	return func(s *Settings) {
		if s.GrpcServerUseTLS {
			grpcServerTlsConfig := utils.TlsConfigFromFiles(s.GrpcServerTlsCert, s.GrpcServerTlsKey, s.GrpcClientTlsCACert, utils.ClientCA, false)
			if s.GrpcClientTlsCACert != "" {
				grpcServerTlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
			} else {
				grpcServerTlsConfig.ClientAuth = tls.NoClientCert
			}
			s.GrpcServerTlsConfig = grpcServerTlsConfig
		}
	}
}

func ConfigGrpcXdsServerTlsConfig() Option {
	return func(s *Settings) {
		if s.ConfigGrpcXdsServerUseTls {
			configGrpcXdsServerTlsConfig := utils.TlsConfigFromFiles(s.ConfigGrpcXdsClientTlsCert, s.ConfigGrpcXdsClientTlsKey, s.ConfigGrpcXdsServerTlsCACert, utils.ServerCA, false)
			if s.ConfigGrpcXdsServerTlsCACert != "" {
				configGrpcXdsServerTlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
			} else {
				configGrpcXdsServerTlsConfig.ClientAuth = tls.NoClientCert
			}
			s.ConfigGrpcXdsTlsConfig = configGrpcXdsServerTlsConfig
		}
	}
}

func GrpcUnaryInterceptor(i grpc.UnaryServerInterceptor) Option {
	return func(s *Settings) {
		s.GrpcUnaryInterceptor = i
	}
}

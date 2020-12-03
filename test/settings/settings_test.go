package settings_test

import (
	"os"
	"testing"
	"time"

	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type TestSettings struct {
	suite.Suite
}

func (ts *TestSettings) SetupTest() {
	os.Setenv("PORT", "8080")
	os.Setenv("GRPC_PORT", "8081")
	os.Setenv("DEBUG_PORT", "6070")
	os.Setenv("USE_STATSD", "true")
	os.Setenv("STATSD_HOST", "localhost")
	os.Setenv("STATSD_PORT", "8125")
	os.Setenv("RUNTIME_ROOT", "/srv/runtime_data/current")
	os.Setenv("RUNTIME_SUBDIRECTORY", "")
	os.Setenv("RUNTIME_IGNOREDOTFILES", "false")
	os.Setenv("RUNTIME_WATCH_ROOT", "true")
	os.Setenv("LOG_LEVEL", "WARN")
	os.Setenv("LOG_FORMAT", "test")
	os.Setenv("REDIS_SOCKET_TYPE", "unix")
	os.Setenv("REDIS_TYPE", "SINGLE")
	os.Setenv("REDIS_URL", "/var/run/nutcracker/ratelimit.sock")
	os.Setenv("REDIS_POOL_SIZE", "10")
	os.Setenv("REDIS_AUTH", "")
	os.Setenv("REDIS_TLS", "false")
	os.Setenv("REDIS_PIPELINE_WINDOW", "0")
	os.Setenv("REDIS_PIPELINE_LIMIT", "0")
	os.Setenv("REDIS_PERSECOND", "false")
	os.Setenv("REDIS_PERSECOND_SOCKET_TYPE", "unix")
	os.Setenv("REDIS_PERSECOND_TYPE", "SINGLE")
	os.Setenv("REDIS_PERSECOND_URL", "/var/run/nutcracker/ratelimit.sock")
	os.Setenv("REDIS_PERSECOND_POOL_SIZE", "10")
	os.Setenv("REDIS_PERSECOND_AUTH", "")
	os.Setenv("REDIS_PERSECOND_TLS", "false")
	os.Setenv("REDIS_PERSECOND_PIPELINE_WINDOW", "0")
	os.Setenv("REDIS_PERSECOND_PIPELINE_LIMIT", "0")
	os.Setenv("EXPIRATION_JITTER_MAX_SECONDS", "300")
	os.Setenv("LOCAL_CACHE_SIZE_IN_BYTES", "0")
	os.Setenv("NEAR_LIMIT_RATIO", "0.8")
}

func (ts *TestSettings) TestShouldReturnDefaultValueIfNotSet() {
	s := settings.NewSettings()

	assert.Equal(ts.T(), 8080, s.Port)
	assert.Equal(ts.T(), 8081, s.GrpcPort)
	assert.Equal(ts.T(), 6070, s.DebugPort)
	assert.Equal(ts.T(), true, s.UseStatsd)
	assert.Equal(ts.T(), "localhost", s.StatsdHost)
	assert.Equal(ts.T(), 8125, s.StatsdPort)
	assert.Equal(ts.T(), "/srv/runtime_data/current", s.RuntimePath)
	assert.Equal(ts.T(), "", s.RuntimeSubdirectory)
	assert.Equal(ts.T(), false, s.RuntimeIgnoreDotFiles)
	assert.Equal(ts.T(), true, s.RuntimeWatchRoot)
	assert.Equal(ts.T(), "WARN", s.LogLevel)
	assert.Equal(ts.T(), "test", s.LogFormat)
	assert.Equal(ts.T(), "unix", s.RedisSocketType)
	assert.Equal(ts.T(), "SINGLE", s.RedisType)
	assert.Equal(ts.T(), "/var/run/nutcracker/ratelimit.sock", s.RedisUrl)
	assert.Equal(ts.T(), 10, s.RedisPoolSize)
	assert.Equal(ts.T(), "", s.RedisAuth)
	assert.Equal(ts.T(), false, s.RedisTls)
	assert.Equal(ts.T(), time.Duration(0)*time.Second, s.RedisPipelineWindow)
	assert.Equal(ts.T(), 0, s.RedisPipelineLimit)
	assert.Equal(ts.T(), false, s.RedisPerSecond)
	assert.Equal(ts.T(), "unix", s.RedisPerSecondSocketType)
	assert.Equal(ts.T(), "SINGLE", s.RedisPerSecondType)
	assert.Equal(ts.T(), "/var/run/nutcracker/ratelimit.sock", s.RedisPerSecondUrl)
	assert.Equal(ts.T(), 10, s.RedisPerSecondPoolSize)
	assert.Equal(ts.T(), "", s.RedisPerSecondAuth)
	assert.Equal(ts.T(), false, s.RedisPerSecondTls)
	assert.Equal(ts.T(), time.Duration(0)*time.Second, s.RedisPerSecondPipelineWindow)
	assert.Equal(ts.T(), 0, s.RedisPerSecondPipelineLimit)
	assert.Equal(ts.T(), int64(300), s.ExpirationJitterMaxSeconds)
	assert.Equal(ts.T(), 0, s.LocalCacheSizeInBytes)
	assert.Equal(ts.T(), float32(0.8), s.NearLimitRatio)
}

func (ts *TestSettings) TestShouldReturnCorrectValue() {
	os.Setenv("REDIS_PIPELINE_WINDOW", "100")
	os.Setenv("REDIS_PERSECOND_PIPELINE_WINDOW", "200")

	expectedRedisPipelineWindowExpectedValue := time.Duration(100) * time.Second
	expectedRedisPerSecondPipelineWindowValue := time.Duration(200) * time.Second

	s := settings.NewSettings()

	assert.Equal(ts.T(), expectedRedisPipelineWindowExpectedValue, s.RedisPipelineWindow)
	assert.Equal(ts.T(), expectedRedisPerSecondPipelineWindowValue, s.RedisPerSecondPipelineWindow)
}

func TestSettingsSuite(t *testing.T) {
	suite.Run(t, new(TestSettings))
}

// +build integration

package integration_test

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	pb_legacy "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v2"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/memcached"
	"github.com/envoyproxy/ratelimit/src/service_cmd/runner"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/test/common"
	"github.com/golang/protobuf/ptypes/duration"
	"github.com/kelseyhightower/envconfig"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

func init() {
	os.Setenv("USE_STATSD", "false")
	// Memcache does async increments, which can cause race conditions during
	// testing. Force sync increments so the quotas are predictable during testing.
	memcached.AutoFlushForIntegrationTests = true
}

func defaultSettings() settings.Settings {
	// Fetch the default setting values.
	var s settings.Settings
	err := envconfig.Process("UNLIKELY_PREFIX_", &s)
	if err != nil {
		panic(err)
	}

	// Set some convenient defaults for all integration tests.
	s.RuntimePath = "runtime/current"
	s.RuntimeSubdirectory = "ratelimit"
	s.RedisPerSecondSocketType = "tcp"
	s.RedisSocketType = "tcp"
	s.DebugPort = 8084
	s.UseStatsd = false
	s.Port = 8082
	s.GrpcPort = 8083

	return s
}

func newDescriptorStatus(status pb.RateLimitResponse_Code, requestsPerUnit uint32, unit pb.RateLimitResponse_RateLimit_Unit, limitRemaining uint32, durRemaining *duration.Duration) *pb.RateLimitResponse_DescriptorStatus {

	limit := &pb.RateLimitResponse_RateLimit{RequestsPerUnit: requestsPerUnit, Unit: unit}

	return &pb.RateLimitResponse_DescriptorStatus{
		Code:               status,
		CurrentLimit:       limit,
		LimitRemaining:     limitRemaining,
		DurationUntilReset: &duration.Duration{Seconds: durRemaining.GetSeconds()},
	}
}

func newDescriptorStatusLegacy(
	status pb_legacy.RateLimitResponse_Code, requestsPerUnit uint32,
	unit pb_legacy.RateLimitResponse_RateLimit_Unit, limitRemaining uint32) *pb_legacy.RateLimitResponse_DescriptorStatus {

	return &pb_legacy.RateLimitResponse_DescriptorStatus{
		Code:           status,
		CurrentLimit:   &pb_legacy.RateLimitResponse_RateLimit{RequestsPerUnit: requestsPerUnit, Unit: unit},
		LimitRemaining: limitRemaining,
	}
}

func makeSimpleRedisSettings(redisPort int, perSecondPort int, perSecond bool, localCacheSize int) settings.Settings {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = localCacheSize
	s.BackendType = "redis"
	s.RedisUrl = "localhost:" + strconv.Itoa(redisPort)
	s.RedisPerSecondUrl = "localhost:" + strconv.Itoa(perSecondPort)
	return s
}

func TestBasicConfig(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6383},
		{Port: 6380},
	}, func() {
		t.Run("WithoutPerSecondRedis", testBasicConfig(makeSimpleRedisSettings(6383, 6380, false, 0)))
		t.Run("WithPerSecondRedis", testBasicConfig(makeSimpleRedisSettings(6383, 6380, true, 0)))
		t.Run("WithoutPerSecondRedisWithLocalCache", testBasicConfig(makeSimpleRedisSettings(6383, 6380, false, 1000)))
		t.Run("WithPerSecondRedisWithLocalCache", testBasicConfig(makeSimpleRedisSettings(6383, 6380, true, 1000)))
		cacheSettings := makeSimpleRedisSettings(6383, 6380, false, 0)
		cacheSettings.CacheKeyPrefix = "prefix:"
		t.Run("WithoutPerSecondRedisWithCachePrefix", testBasicConfig(cacheSettings))
	})
}

func TestBasicConfig_ExtraTags(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6383},
	}, func() {
		extraTagsSettings := makeSimpleRedisSettings(6383, 6380, false, 0)
		extraTagsSettings.ExtraTags = map[string]string{"foo": "bar", "a": "b"}
		runner := startTestRunner(t, extraTagsSettings)
		defer runner.Stop()

		assert := assert.New(t)
		conn, err := grpc.Dial(fmt.Sprintf("localhost:%v", extraTagsSettings.GrpcPort), grpc.WithInsecure())
		assert.NoError(err)
		defer conn.Close()
		c := pb.NewRateLimitServiceClient(conn)

		_, err = c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("basic", [][][2]string{{{getCacheKey("key1", false), "foo"}}}, 1))
		assert.NoError(err)

		// Manually flush the cache for local_cache stats
		runner.GetStatsStore().Flush()

		// store.NewCounter returns the existing counter.
		// This test looks for the extra tags requested.
		key1HitCounter := runner.GetStatsStore().NewCounterWithTags(
			fmt.Sprintf("ratelimit.service.rate_limit.basic.%s.total_hits", getCacheKey("key1", false)),
			extraTagsSettings.ExtraTags)
		assert.Equal(1, int(key1HitCounter.Value()))

		configLoadStat := runner.GetStatsStore().NewCounterWithTags(
			"ratelimit.service.config_load_success",
			extraTagsSettings.ExtraTags)
		assert.Equal(1, int(configLoadStat.Value()))

		// NOTE: This doesn't currently test that the extra tags are present for:
		// - local cache
		// - go runtime stats.
	})
}

func TestBasicTLSConfig(t *testing.T) {
	t.Run("WithoutPerSecondRedisTLS", testBasicConfigAuthTLS(false, 0))
	t.Run("WithPerSecondRedisTLS", testBasicConfigAuthTLS(true, 0))
	t.Run("WithoutPerSecondRedisTLSWithLocalCache", testBasicConfigAuthTLS(false, 1000))
	t.Run("WithPerSecondRedisTLSWithLocalCache", testBasicConfigAuthTLS(true, 1000))
}

func TestBasicAuthConfig(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6384, Password: "password123"},
		{Port: 6385, Password: "password123"},
	}, func() {
		t.Run("WithoutPerSecondRedisAuth", testBasicConfigAuth(false, 0))
		t.Run("WithPerSecondRedisAuth", testBasicConfigAuth(true, 0))
		t.Run("WithoutPerSecondRedisAuthWithLocalCache", testBasicConfigAuth(false, 1000))
		t.Run("WithPerSecondRedisAuthWithLocalCache", testBasicConfigAuth(true, 1000))
	})
}

func TestBasicAuthConfigWithRedisCluster(t *testing.T) {
	t.Run("WithoutPerSecondRedisAuth", testBasicConfigAuthWithRedisCluster(false, 0))
	t.Run("WithPerSecondRedisAuth", testBasicConfigAuthWithRedisCluster(true, 0))
	t.Run("WithoutPerSecondRedisAuthWithLocalCache", testBasicConfigAuthWithRedisCluster(false, 1000))
	t.Run("WithPerSecondRedisAuthWithLocalCache", testBasicConfigAuthWithRedisCluster(true, 1000))
}

func TestBasicAuthConfigWithRedisSentinel(t *testing.T) {
	t.Run("WithoutPerSecondRedisAuth", testBasicAuthConfigWithRedisSentinel(false, 0))
	t.Run("WithPerSecondRedisAuth", testBasicAuthConfigWithRedisSentinel(true, 0))
	t.Run("WithoutPerSecondRedisAuthWithLocalCache", testBasicAuthConfigWithRedisSentinel(false, 1000))
	t.Run("WithPerSecondRedisAuthWithLocalCache", testBasicAuthConfigWithRedisSentinel(true, 1000))
}

func TestBasicReloadConfig(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6383},
	}, func() {
		t.Run("BasicWithoutWatchRoot", testBasicConfigWithoutWatchRoot(false, 0))
		t.Run("ReloadWithoutWatchRoot", testBasicConfigReload(false, 0, false))
	})
}

func makeSimpleMemcacheSettings(memcachePorts []int, localCacheSize int) settings.Settings {
	s := defaultSettings()
	var memcacheHostAndPort []string
	for _, memcachePort := range memcachePorts {
		memcacheHostAndPort = append(memcacheHostAndPort, "localhost:"+strconv.Itoa(memcachePort))
	}
	s.MemcacheHostPort = memcacheHostAndPort
	s.LocalCacheSizeInBytes = localCacheSize
	s.BackendType = "memcache"
	return s
}

func TestBasicConfigMemcache(t *testing.T) {
	singleNodePort := []int{6394}
	common.WithMultiMemcache(t, []common.MemcacheConfig{
		{Port: 6394},
	}, func() {
		t.Run("Memcache", testBasicConfig(makeSimpleMemcacheSettings(singleNodePort, 0)))
		t.Run("MemcacheWithLocalCache", testBasicConfig(makeSimpleMemcacheSettings(singleNodePort, 1000)))
		cacheSettings := makeSimpleMemcacheSettings(singleNodePort, 0)
		cacheSettings.CacheKeyPrefix = "prefix:"
		t.Run("MemcacheWithPrefix", testBasicConfig(cacheSettings))
	})
}

func TestMultiNodeMemcache(t *testing.T) {
	multiNodePorts := []int{6494, 6495}
	common.WithMultiMemcache(t, []common.MemcacheConfig{
		{Port: 6494}, {Port: 6495},
	}, func() {
		t.Run("MemcacheMultipleNodes", testBasicConfig(makeSimpleMemcacheSettings(multiNodePorts, 0)))
	})
}

func testBasicConfigAuthTLS(perSecond bool, local_cache_size int) func(*testing.T) {
	s := makeSimpleRedisSettings(16381, 16382, perSecond, local_cache_size)
	s.RedisAuth = "password123"
	s.RedisTls = true
	s.RedisPerSecondAuth = "password123"
	s.RedisPerSecondTls = true

	return testBasicBaseConfig(s)
}

func testBasicConfig(s settings.Settings) func(*testing.T) {
	return testBasicBaseConfig(s)
}

func testBasicConfigAuth(perSecond bool, local_cache_size int) func(*testing.T) {
	s := makeSimpleRedisSettings(6384, 6385, perSecond, local_cache_size)
	s.RedisAuth = "password123"
	s.RedisPerSecondAuth = "password123"

	return testBasicBaseConfig(s)
}

func testBasicConfigAuthWithRedisCluster(perSecond bool, local_cache_size int) func(*testing.T) {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = local_cache_size
	s.BackendType = "redis"

	configRedisCluster(&s)

	return testBasicBaseConfig(s)
}

func configRedisSentinel(s *settings.Settings) {
	s.RedisPerSecondType = "sentinel"

	s.RedisPerSecondUrl = "mymaster,localhost:26399,localhost:26400,localhost:26401"
	s.RedisType = "sentinel"
	s.RedisUrl = "mymaster,localhost:26394,localhost:26395,localhost:26396"
	s.RedisAuth = "password123"
	s.RedisPerSecondAuth = "password123"
}

func testBasicAuthConfigWithRedisSentinel(perSecond bool, local_cache_size int) func(*testing.T) {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = local_cache_size
	s.BackendType = "redis"

	configRedisSentinel(&s)

	return testBasicBaseConfig(s)
}

func testBasicConfigWithoutWatchRoot(perSecond bool, local_cache_size int) func(*testing.T) {
	s := makeSimpleRedisSettings(6383, 6380, perSecond, local_cache_size)
	s.RuntimeWatchRoot = false

	return testBasicBaseConfig(s)
}

func configRedisCluster(s *settings.Settings) {
	s.RedisPerSecondType = "cluster"
	s.RedisPerSecondUrl = "localhost:6389,localhost:6390,localhost:6391"
	s.RedisType = "cluster"
	s.RedisUrl = "localhost:6386,localhost:6387,localhost:6388"

	s.RedisAuth = "password123"
	s.RedisPerSecondAuth = "password123"

	s.RedisPerSecondPipelineLimit = 8
	s.RedisPipelineLimit = 8
}

func testBasicConfigWithoutWatchRootWithRedisCluster(perSecond bool, local_cache_size int) func(*testing.T) {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = local_cache_size
	s.BackendType = "redis"

	s.RuntimeWatchRoot = false

	configRedisCluster(&s)

	return testBasicBaseConfig(s)
}

func testBasicConfigWithoutWatchRootWithRedisSentinel(perSecond bool, local_cache_size int) func(*testing.T) {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = local_cache_size
	s.BackendType = "redis"

	configRedisSentinel(&s)

	s.RuntimeWatchRoot = false

	return testBasicBaseConfig(s)
}

func testBasicConfigReload(perSecond bool, local_cache_size int, runtimeWatchRoot bool) func(*testing.T) {
	s := makeSimpleRedisSettings(6383, 6380, perSecond, local_cache_size)
	s.RuntimeWatchRoot = runtimeWatchRoot
	return testConfigReload(s)
}

func testBasicConfigReloadWithRedisCluster(perSecond bool, local_cache_size int, runtimeWatchRoot string) func(*testing.T) {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = local_cache_size
	s.BackendType = "redis"

	s.RuntimeWatchRoot = s.RuntimeWatchRoot

	configRedisCluster(&s)

	return testConfigReload(s)
}

func testBasicConfigReloadWithRedisSentinel(perSecond bool, local_cache_size int, runtimeWatchRoot bool) func(*testing.T) {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = local_cache_size
	s.BackendType = "redis"

	configRedisSentinel(&s)

	s.RuntimeWatchRoot = runtimeWatchRoot

	return testConfigReload(s)
}

func getCacheKey(cacheKey string, enableLocalCache bool) string {
	if enableLocalCache {
		return cacheKey + "_local"
	}

	return cacheKey
}

func testBasicBaseConfig(s settings.Settings) func(*testing.T) {
	return func(t *testing.T) {
		enable_local_cache := s.LocalCacheSizeInBytes > 0
		runner := startTestRunner(t, s)
		defer runner.Stop()

		assert := assert.New(t)
		conn, err := grpc.Dial(fmt.Sprintf("localhost:%v", s.GrpcPort), grpc.WithInsecure())
		assert.NoError(err)
		defer conn.Close()
		c := pb.NewRateLimitServiceClient(conn)

		response, err := c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("foo", [][][2]string{{{getCacheKey("hello", enable_local_cache), "world"}}}, 1))
		common.AssertProtoEqual(
			assert,
			&pb.RateLimitResponse{
				OverallCode: pb.RateLimitResponse_OK,
				Statuses:    []*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}}},
			response)
		assert.NoError(err)

		// Manually flush the cache for local_cache stats
		runner.GetStatsStore().Flush()
		localCacheHitCounter := runner.GetStatsStore().NewGauge("ratelimit.localcache.hitCount")
		assert.Equal(0, int(localCacheHitCounter.Value()))

		localCacheMissCounter := runner.GetStatsStore().NewGauge("ratelimit.localcache.missCount")
		assert.Equal(0, int(localCacheMissCounter.Value()))

		response, err = c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("basic", [][][2]string{{{getCacheKey("key1", enable_local_cache), "foo"}}}, 1))
		durRemaining := response.GetStatuses()[0].DurationUntilReset

		common.AssertProtoEqual(
			assert,
			&pb.RateLimitResponse{
				OverallCode: pb.RateLimitResponse_OK,
				Statuses: []*pb.RateLimitResponse_DescriptorStatus{
					newDescriptorStatus(pb.RateLimitResponse_OK, 50, pb.RateLimitResponse_RateLimit_SECOND, 49, durRemaining)}},
			response)
		assert.NoError(err)

		// store.NewCounter returns the existing counter.
		key1HitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.basic.%s.total_hits", getCacheKey("key1", enable_local_cache)))
		assert.Equal(1, int(key1HitCounter.Value()))

		// Manually flush the cache for local_cache stats
		runner.GetStatsStore().Flush()
		localCacheHitCounter = runner.GetStatsStore().NewGauge("ratelimit.localcache.hitCount")
		assert.Equal(0, int(localCacheHitCounter.Value()))

		localCacheMissCounter = runner.GetStatsStore().NewGauge("ratelimit.localcache.missCount")
		if enable_local_cache {
			assert.Equal(1, int(localCacheMissCounter.Value()))
		} else {
			assert.Equal(0, int(localCacheMissCounter.Value()))
		}

		// Now come up with a random key, and go over limit for a minute limit which should always work.
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		randomInt := r.Int()
		for i := 0; i < 25; i++ {
			response, err = c.ShouldRateLimit(
				context.Background(),
				common.NewRateLimitRequest(
					"another", [][][2]string{{{getCacheKey("key2", enable_local_cache), strconv.Itoa(randomInt)}}}, 1))

			status := pb.RateLimitResponse_OK
			limitRemaining := uint32(20 - (i + 1))
			if i >= 20 {
				status = pb.RateLimitResponse_OVER_LIMIT
				limitRemaining = 0
			}
			durRemaining = response.GetStatuses()[0].DurationUntilReset

			common.AssertProtoEqual(
				assert,
				&pb.RateLimitResponse{
					OverallCode: status,
					Statuses: []*pb.RateLimitResponse_DescriptorStatus{
						newDescriptorStatus(status, 20, pb.RateLimitResponse_RateLimit_MINUTE, limitRemaining, durRemaining)}},
				response)
			assert.NoError(err)
			key2HitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.total_hits", getCacheKey("key2", enable_local_cache)))
			assert.Equal(i+1, int(key2HitCounter.Value()))
			key2OverlimitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.over_limit", getCacheKey("key2", enable_local_cache)))
			if i >= 20 {
				assert.Equal(i-19, int(key2OverlimitCounter.Value()))
			} else {
				assert.Equal(0, int(key2OverlimitCounter.Value()))
			}
			key2LocalCacheOverLimitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.over_limit_with_local_cache", getCacheKey("key2", enable_local_cache)))
			if enable_local_cache && i >= 20 {
				assert.Equal(i-20, int(key2LocalCacheOverLimitCounter.Value()))
			} else {
				assert.Equal(0, int(key2LocalCacheOverLimitCounter.Value()))
			}

			// Manually flush the cache for local_cache stats
			runner.GetStatsStore().Flush()
			localCacheHitCounter = runner.GetStatsStore().NewGauge("ratelimit.localcache.hitCount")
			if enable_local_cache && i >= 20 {
				assert.Equal(i-20, int(localCacheHitCounter.Value()))
			} else {
				assert.Equal(0, int(localCacheHitCounter.Value()))
			}

			localCacheMissCounter = runner.GetStatsStore().NewGauge("ratelimit.localcache.missCount")
			if enable_local_cache {
				if i < 20 {
					assert.Equal(i+2, int(localCacheMissCounter.Value()))
				} else {
					assert.Equal(22, int(localCacheMissCounter.Value()))
				}
			} else {
				assert.Equal(0, int(localCacheMissCounter.Value()))
			}
		}

		// Limit now against 2 keys in the same domain.
		randomInt = r.Int()
		for i := 0; i < 15; i++ {
			response, err = c.ShouldRateLimit(
				context.Background(),
				common.NewRateLimitRequest(
					"another",
					[][][2]string{
						{{getCacheKey("key2", enable_local_cache), strconv.Itoa(randomInt)}},
						{{getCacheKey("key3", enable_local_cache), strconv.Itoa(randomInt)}}}, 1))

			status := pb.RateLimitResponse_OK
			limitRemaining1 := uint32(20 - (i + 1))
			limitRemaining2 := uint32(10 - (i + 1))
			if i >= 10 {
				status = pb.RateLimitResponse_OVER_LIMIT
				limitRemaining2 = 0
			}
			durRemaining1 := response.GetStatuses()[0].DurationUntilReset
			durRemaining2 := response.GetStatuses()[1].DurationUntilReset
			common.AssertProtoEqual(
				assert,
				&pb.RateLimitResponse{
					OverallCode: status,
					Statuses: []*pb.RateLimitResponse_DescriptorStatus{
						newDescriptorStatus(pb.RateLimitResponse_OK, 20, pb.RateLimitResponse_RateLimit_MINUTE, limitRemaining1, durRemaining1),
						newDescriptorStatus(status, 10, pb.RateLimitResponse_RateLimit_HOUR, limitRemaining2, durRemaining2)}},
				response)
			assert.NoError(err)

			key2HitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.total_hits", getCacheKey("key2", enable_local_cache)))
			assert.Equal(i+26, int(key2HitCounter.Value()))
			key2OverlimitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.over_limit", getCacheKey("key2", enable_local_cache)))
			assert.Equal(5, int(key2OverlimitCounter.Value()))
			key2LocalCacheOverLimitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.over_limit_with_local_cache", getCacheKey("key2", enable_local_cache)))
			if enable_local_cache {
				assert.Equal(4, int(key2LocalCacheOverLimitCounter.Value()))
			} else {
				assert.Equal(0, int(key2LocalCacheOverLimitCounter.Value()))
			}

			key3HitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.total_hits", getCacheKey("key3", enable_local_cache)))
			assert.Equal(i+1, int(key3HitCounter.Value()))
			key3OverlimitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.over_limit", getCacheKey("key3", enable_local_cache)))
			if i >= 10 {
				assert.Equal(i-9, int(key3OverlimitCounter.Value()))
			} else {
				assert.Equal(0, int(key3OverlimitCounter.Value()))
			}
			key3LocalCacheOverLimitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.over_limit_with_local_cache", getCacheKey("key3", enable_local_cache)))
			if enable_local_cache && i >= 10 {
				assert.Equal(i-10, int(key3LocalCacheOverLimitCounter.Value()))
			} else {
				assert.Equal(0, int(key3LocalCacheOverLimitCounter.Value()))
			}

			// Manually flush the cache for local_cache stats
			runner.GetStatsStore().Flush()
			localCacheHitCounter = runner.GetStatsStore().NewGauge("ratelimit.localcache.hitCount")
			if enable_local_cache {
				if i < 10 {
					assert.Equal(4, int(localCacheHitCounter.Value()))
				} else {
					// key3 caches hit
					assert.Equal(i-6, int(localCacheHitCounter.Value()))
				}
			} else {
				assert.Equal(0, int(localCacheHitCounter.Value()))
			}

			localCacheMissCounter = runner.GetStatsStore().NewGauge("ratelimit.localcache.missCount")
			if enable_local_cache {
				if i < 10 {
					// both key2 and key3 cache miss.
					assert.Equal(i*2+24, int(localCacheMissCounter.Value()))
				} else {
					// key2 caches miss.
					assert.Equal(i+34, int(localCacheMissCounter.Value()))
				}
			} else {
				assert.Equal(0, int(localCacheMissCounter.Value()))
			}
		}

		// Test DurationUntilReset by hitting same key twice
		resp1, err := c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("another", [][][2]string{{{getCacheKey("key4", enable_local_cache), "durTest"}}}, 1))

		time.Sleep(2 * time.Second) // Wait to allow duration to tick down

		resp2, err := c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("another", [][][2]string{{{getCacheKey("key4", enable_local_cache), "durTest"}}}, 1))

		assert.Less(resp2.GetStatuses()[0].DurationUntilReset.GetSeconds(), resp1.GetStatuses()[0].DurationUntilReset.GetSeconds())
	}
}

func TestBasicConfigLegacy(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6383},
	}, func() {
		testBasicConfigLegacy(t)
	})
}

func testBasicConfigLegacy(t *testing.T) {
	s := makeSimpleRedisSettings(6383, 6380, false, 0)

	runner := startTestRunner(t, s)
	defer runner.Stop()

	assert := assert.New(t)
	conn, err := grpc.Dial("localhost:8083", grpc.WithInsecure())

	assert.NoError(err)
	defer conn.Close()
	c := pb_legacy.NewRateLimitServiceClient(conn)

	response, err := c.ShouldRateLimit(
		context.Background(),
		common.NewRateLimitRequestLegacy("foo", [][][2]string{{{"hello", "world"}}}, 1))
	common.AssertProtoEqual(
		assert,
		&pb_legacy.RateLimitResponse{
			OverallCode: pb_legacy.RateLimitResponse_OK,
			Statuses:    []*pb_legacy.RateLimitResponse_DescriptorStatus{{Code: pb_legacy.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}}},
		response)
	assert.NoError(err)

	response, err = c.ShouldRateLimit(
		context.Background(),
		common.NewRateLimitRequestLegacy("basic_legacy", [][][2]string{{{"key1", "foo"}}}, 1))
	common.AssertProtoEqual(
		assert,
		&pb_legacy.RateLimitResponse{
			OverallCode: pb_legacy.RateLimitResponse_OK,
			Statuses: []*pb_legacy.RateLimitResponse_DescriptorStatus{
				newDescriptorStatusLegacy(pb_legacy.RateLimitResponse_OK, 50, pb_legacy.RateLimitResponse_RateLimit_SECOND, 49)}},
		response)
	assert.NoError(err)

	// Now come up with a random key, and go over limit for a minute limit which should always work.
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	randomInt := r.Int()
	for i := 0; i < 25; i++ {
		response, err = c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequestLegacy(
				"another", [][][2]string{{{"key2", strconv.Itoa(randomInt)}}}, 1))

		status := pb_legacy.RateLimitResponse_OK
		limitRemaining := uint32(20 - (i + 1))
		if i >= 20 {
			status = pb_legacy.RateLimitResponse_OVER_LIMIT
			limitRemaining = 0
		}

		common.AssertProtoEqual(
			assert,
			&pb_legacy.RateLimitResponse{
				OverallCode: status,
				Statuses: []*pb_legacy.RateLimitResponse_DescriptorStatus{
					newDescriptorStatusLegacy(status, 20, pb_legacy.RateLimitResponse_RateLimit_MINUTE, limitRemaining)}},
			response)
		assert.NoError(err)
	}

	// Limit now against 2 keys in the same domain.
	randomInt = r.Int()
	for i := 0; i < 15; i++ {
		response, err = c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequestLegacy(
				"another_legacy",
				[][][2]string{
					{{"key2", strconv.Itoa(randomInt)}},
					{{"key3", strconv.Itoa(randomInt)}}}, 1))

		status := pb_legacy.RateLimitResponse_OK
		limitRemaining1 := uint32(20 - (i + 1))
		limitRemaining2 := uint32(10 - (i + 1))
		if i >= 10 {
			status = pb_legacy.RateLimitResponse_OVER_LIMIT
			limitRemaining2 = 0
		}

		common.AssertProtoEqual(
			assert,
			&pb_legacy.RateLimitResponse{
				OverallCode: status,
				Statuses: []*pb_legacy.RateLimitResponse_DescriptorStatus{
					newDescriptorStatusLegacy(pb_legacy.RateLimitResponse_OK, 20, pb_legacy.RateLimitResponse_RateLimit_MINUTE, limitRemaining1),
					newDescriptorStatusLegacy(status, 10, pb_legacy.RateLimitResponse_RateLimit_HOUR, limitRemaining2)}},
			response)
		assert.NoError(err)
	}
}

func startTestRunner(t *testing.T, s settings.Settings) *runner.Runner {
	t.Helper()
	runner := runner.NewRunner(s)

	go func() {
		// Catch a panic() to ensure that test name is printed.
		// Otherwise go doesn't know what test this goroutine is
		// associated with.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Uncaught panic(): %v", r)
			}
		}()
		runner.Run()
	}()

	// HACK: Wait for the server to come up. Make a hook that we can wait on.
	common.WaitForTcpPort(context.Background(), s.GrpcPort, 1*time.Second)

	return &runner
}

func testConfigReload(s settings.Settings) func(*testing.T) {
	return func(t *testing.T) {
		enable_local_cache := s.LocalCacheSizeInBytes > 0
		runner := startTestRunner(t, s)
		defer runner.Stop()

		assert := assert.New(t)
		conn, err := grpc.Dial(fmt.Sprintf("localhost:%v", s.GrpcPort), grpc.WithInsecure())
		assert.NoError(err)
		defer conn.Close()
		c := pb.NewRateLimitServiceClient(conn)

		response, err := c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("reload", [][][2]string{{{getCacheKey("block", enable_local_cache), "foo"}}}, 1))
		common.AssertProtoEqual(
			assert,
			&pb.RateLimitResponse{
				OverallCode: pb.RateLimitResponse_OK,
				Statuses:    []*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK}}},
			response)
		assert.NoError(err)

		runner.GetStatsStore().Flush()
		loadCount1 := runner.GetStatsStore().NewCounter("ratelimit.service.config_load_success").Value()

		// Copy a new file to config folder to test config reload functionality
		in, err := os.Open("runtime/current/ratelimit/reload.yaml")
		if err != nil {
			panic(err)
		}
		defer in.Close()
		out, err := os.Create("runtime/current/ratelimit/config/reload.yaml")
		if err != nil {
			panic(err)
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		if err != nil {
			panic(err)
		}
		err = out.Close()
		if err != nil {
			panic(err)
		}

		// Need to wait for config reload to take place and new descriptors to be loaded.
		// Shouldn't take more than 5 seconds but wait 120 at most just to be safe.
		wait := 120
		reloaded := false
		loadCount2 := uint64(0)

		for i := 0; i < wait; i++ {
			time.Sleep(1 * time.Second)
			runner.GetStatsStore().Flush()
			loadCount2 = runner.GetStatsStore().NewCounter("ratelimit.service.config_load_success").Value()

			// Check that successful loads count has increased before continuing.
			if loadCount2 > loadCount1 {
				reloaded = true
				break
			}
		}

		assert.True(reloaded)
		assert.Greater(loadCount2, loadCount1)

		response, err = c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("reload", [][][2]string{{{getCacheKey("key1", enable_local_cache), "foo"}}}, 1))

		durRemaining := response.GetStatuses()[0].DurationUntilReset
		common.AssertProtoEqual(
			assert,
			&pb.RateLimitResponse{
				OverallCode: pb.RateLimitResponse_OK,
				Statuses: []*pb.RateLimitResponse_DescriptorStatus{
					newDescriptorStatus(pb.RateLimitResponse_OK, 50, pb.RateLimitResponse_RateLimit_SECOND, 49, durRemaining)}},
			response)
		assert.NoError(err)

		err = os.Remove("runtime/current/ratelimit/config/reload.yaml")
		if err != nil {
			panic(err)
		}
	}
}

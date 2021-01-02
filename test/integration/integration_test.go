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
	"github.com/envoyproxy/ratelimit/src/service_cmd/runner"
	"github.com/envoyproxy/ratelimit/test/common"
	"github.com/golang/protobuf/ptypes/duration"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

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

// TODO: Once adding the ability of stopping the server in the runner (https://github.com/envoyproxy/ratelimit/issues/119),
//  stop the server at the end of each test, thus we can reuse the grpc port among these integration tests.
func TestBasicConfig(t *testing.T) {
	t.Run("WithoutPerSecondRedis", testBasicConfig("8083", "false", "0", ""))
	t.Run("WithPerSecondRedis", testBasicConfig("8085", "true", "0", "redis"))
	t.Run("WithoutPerSecondRedisWithLocalCache", testBasicConfig("18083", "false", "1000", ""))
	t.Run("WithPerSecondRedisWithLocalCache", testBasicConfig("18085", "true", "1000", "redis"))
}

func TestBasicTLSConfig(t *testing.T) {
	t.Run("WithoutPerSecondRedisTLS", testBasicConfigAuthTLS("8087", "false", "0"))
	t.Run("WithPerSecondRedisTLS", testBasicConfigAuthTLS("8089", "true", "0"))
	t.Run("WithoutPerSecondRedisTLSWithLocalCache", testBasicConfigAuthTLS("18087", "false", "1000"))
	t.Run("WithPerSecondRedisTLSWithLocalCache", testBasicConfigAuthTLS("18089", "true", "1000"))
}

func TestBasicAuthConfig(t *testing.T) {
	t.Run("WithoutPerSecondRedisAuth", testBasicConfigAuth("8091", "false", "0"))
	t.Run("WithPerSecondRedisAuth", testBasicConfigAuth("8093", "true", "0"))
	t.Run("WithoutPerSecondRedisAuthWithLocalCache", testBasicConfigAuth("18091", "false", "1000"))
	t.Run("WithPerSecondRedisAuthWithLocalCache", testBasicConfigAuth("18093", "true", "1000"))
}

func TestBasicAuthConfigWithRedisCluster(t *testing.T) {
	t.Run("WithoutPerSecondRedisAuth", testBasicConfigAuthWithRedisCluster("8191", "false", "0"))
	t.Run("WithPerSecondRedisAuth", testBasicConfigAuthWithRedisCluster("8193", "true", "0"))
	t.Run("WithoutPerSecondRedisAuthWithLocalCache", testBasicConfigAuthWithRedisCluster("18191", "false", "1000"))
	t.Run("WithPerSecondRedisAuthWithLocalCache", testBasicConfigAuthWithRedisCluster("18193", "true", "1000"))
}

func TestBasicAuthConfigWithRedisSentinel(t *testing.T) {
	t.Run("WithoutPerSecondRedisAuth", testBasicAuthConfigWithRedisSentinel("8291", "false", "0"))
	t.Run("WithPerSecondRedisAuth", testBasicAuthConfigWithRedisSentinel("8293", "true", "0"))
	t.Run("WithoutPerSecondRedisAuthWithLocalCache", testBasicAuthConfigWithRedisSentinel("18291", "false", "1000"))
	t.Run("WithPerSecondRedisAuthWithLocalCache", testBasicAuthConfigWithRedisSentinel("18293", "true", "1000"))
}

func TestBasicReloadConfig(t *testing.T) {
	t.Run("BasicWithoutWatchRoot", testBasicConfigWithoutWatchRoot("8095", "false", "0"))
	t.Run("ReloadWithoutWatchRoot", testBasicConfigReload("8097", "false", "0", "false"))
}

func TestBasicConfigMemcache(t *testing.T) {
	t.Run("Memcache", testBasicConfig("8098", "false", "0", "memcache"))
	t.Run("MemcacheWithLocalCache", testBasicConfig("18099", "false", "1000", "memcache"))
}

func testBasicConfigAuthTLS(grpcPort, perSecond string, local_cache_size string) func(*testing.T) {
	os.Setenv("REDIS_PERSECOND_URL", "localhost:16382")
	os.Setenv("REDIS_URL", "localhost:16381")
	os.Setenv("REDIS_AUTH", "password123")
	os.Setenv("REDIS_TLS", "true")
	os.Setenv("REDIS_PERSECOND_AUTH", "password123")
	os.Setenv("REDIS_PERSECOND_TLS", "true")
	os.Setenv("REDIS_TYPE", "single")
	os.Setenv("REDIS_PERSECOND_TYPE", "single")

	return testBasicBaseConfig(grpcPort, perSecond, local_cache_size, "")
}

func testBasicConfig(grpcPort, perSecond string, local_cache_size string, backend_type string) func(*testing.T) {
	os.Setenv("REDIS_PERSECOND_URL", "localhost:6380")
	os.Setenv("REDIS_URL", "localhost:6379")
	os.Setenv("MEMCACHE_HOST_PORT", "localhost:6394")
	os.Setenv("REDIS_AUTH", "")
	os.Setenv("REDIS_TLS", "false")
	os.Setenv("REDIS_PERSECOND_AUTH", "")
	os.Setenv("REDIS_PERSECOND_TLS", "false")
	os.Setenv("REDIS_TYPE", "single")
	os.Setenv("REDIS_PERSECOND_TYPE", "single")

	return testBasicBaseConfig(grpcPort, perSecond, local_cache_size, backend_type)
}

func testBasicConfigAuth(grpcPort, perSecond string, local_cache_size string) func(*testing.T) {
	os.Setenv("REDIS_PERSECOND_URL", "localhost:6385")
	os.Setenv("REDIS_URL", "localhost:6384")
	os.Setenv("REDIS_TLS", "false")
	os.Setenv("REDIS_AUTH", "password123")
	os.Setenv("REDIS_PERSECOND_TLS", "false")
	os.Setenv("REDIS_PERSECOND_AUTH", "password123")
	os.Setenv("REDIS_TYPE", "single")
	os.Setenv("REDIS_PERSECOND_TYPE", "single")

	return testBasicBaseConfig(grpcPort, perSecond, local_cache_size, "")
}

func testBasicConfigAuthWithRedisCluster(grpcPort, perSecond string, local_cache_size string) func(*testing.T) {
	os.Setenv("REDIS_PERSECOND_TYPE", "cluster")
	os.Setenv("REDIS_PERSECOND_URL", "localhost:6389,localhost:6390,localhost:6391")
	os.Setenv("REDIS_TYPE", "cluster")
	os.Setenv("REDIS_URL", "localhost:6386,localhost:6387,localhost:6388")
	os.Setenv("REDIS_TLS", "false")
	os.Setenv("REDIS_AUTH", "password123")
	os.Setenv("REDIS_PERSECOND_TLS", "false")
	os.Setenv("REDIS_PERSECOND_AUTH", "password123")
	os.Setenv("REDIS_PERSECOND_PIPELINE_LIMIT", "8")
	os.Setenv("REDIS_PIPELINE_LIMIT", "8")

	return testBasicBaseConfig(grpcPort, perSecond, local_cache_size, "")
}

func testBasicAuthConfigWithRedisSentinel(grpcPort, perSecond string, local_cache_size string) func(*testing.T) {
	os.Setenv("REDIS_PERSECOND_TLS", "false")
	os.Setenv("REDIS_PERSECOND_TYPE", "sentinel")
	os.Setenv("REDIS_PERSECOND_URL", "mymaster,localhost:26399,localhost:26400,localhost:26401")
	os.Setenv("REDIS_TYPE", "sentinel")
	os.Setenv("REDIS_URL", "mymaster,localhost:26394,localhost:26395,localhost:26396")
	os.Setenv("REDIS_TLS", "false")

	return testBasicBaseConfig(grpcPort, perSecond, local_cache_size, "")
}

func testBasicConfigWithoutWatchRoot(grpcPort, perSecond string, local_cache_size string) func(*testing.T) {
	os.Setenv("REDIS_PERSECOND_URL", "localhost:6380")
	os.Setenv("REDIS_URL", "localhost:6379")
	os.Setenv("REDIS_AUTH", "")
	os.Setenv("REDIS_TLS", "false")
	os.Setenv("REDIS_PERSECOND_AUTH", "")
	os.Setenv("REDIS_PERSECOND_TLS", "false")
	os.Setenv("RUNTIME_WATCH_ROOT", "false")
	os.Setenv("REDIS_TYPE", "single")
	os.Setenv("REDIS_PERSECOND_TYPE", "single")
	return testBasicBaseConfig(grpcPort, perSecond, local_cache_size, "")
}

func testBasicConfigWithoutWatchRootWithRedisCluster(grpcPort, perSecond string, local_cache_size string) func(*testing.T) {
	os.Setenv("REDIS_PERSECOND_TYPE", "cluster")
	os.Setenv("REDIS_PERSECOND_URL", "localhost:6389,localhost:6390,localhost:6391")
	os.Setenv("REDIS_TYPE", "cluster")
	os.Setenv("REDIS_URL", "localhost:6386,localhost:6387,localhost:6388")
	os.Setenv("REDIS_AUTH", "password123")
	os.Setenv("REDIS_TLS", "false")
	os.Setenv("REDIS_PERSECOND_AUTH", "password123")
	os.Setenv("REDIS_PERSECOND_TLS", "false")
	os.Setenv("RUNTIME_WATCH_ROOT", "false")
	os.Setenv("REDIS_PERSECOND_PIPELINE_LIMIT", "8")
	os.Setenv("REDIS_PIPELINE_LIMIT", "8")

	return testBasicBaseConfig(grpcPort, perSecond, local_cache_size, "")
}

func testBasicConfigWithoutWatchRootWithRedisSentinel(grpcPort, perSecond string, local_cache_size string) func(*testing.T) {
	os.Setenv("REDIS_PERSECOND_TYPE", "sentinel")
	os.Setenv("REDIS_PERSECOND_URL", "mymaster,localhost:26399,localhost:26400,localhost:26401")
	os.Setenv("REDIS_TYPE", "sentinel")
	os.Setenv("REDIS_URL", "mymaster,localhost:26394,localhost:26395,localhost:26396")
	os.Setenv("REDIS_TLS", "false")
	os.Setenv("REDIS_PERSECOND_TLS", "false")
	os.Setenv("RUNTIME_WATCH_ROOT", "false")

	return testBasicBaseConfig(grpcPort, perSecond, local_cache_size, "")
}

func testBasicConfigReload(grpcPort, perSecond string, local_cache_size, runtimeWatchRoot string) func(*testing.T) {
	os.Setenv("REDIS_PERSECOND_URL", "localhost:6380")
	os.Setenv("REDIS_URL", "localhost:6379")
	os.Setenv("REDIS_AUTH", "")
	os.Setenv("REDIS_TLS", "false")
	os.Setenv("REDIS_PERSECOND_AUTH", "")
	os.Setenv("REDIS_PERSECOND_TLS", "false")
	os.Setenv("RUNTIME_WATCH_ROOT", runtimeWatchRoot)
	os.Setenv("REDIS_TYPE", "single")
	os.Setenv("REDIS_PERSECOND_TYPE", "single")

	return testConfigReload(grpcPort, perSecond, local_cache_size)
}

func testBasicConfigReloadWithRedisCluster(grpcPort, perSecond string, local_cache_size, runtimeWatchRoot string) func(*testing.T) {
	os.Setenv("REDIS_PERSECOND_TYPE", "cluster")
	os.Setenv("REDIS_PERSECOND_URL", "localhost:6389,localhost:6390,localhost:6391")
	os.Setenv("REDIS_TYPE", "cluster")
	os.Setenv("REDIS_URL", "localhost:6386,localhost:6387,localhost:6388")
	os.Setenv("REDIS_PERSECOND_PIPELINE_LIMIT", "8")
	os.Setenv("REDIS_PIPELINE_LIMIT", "8")
	os.Setenv("REDIS_TLS", "false")
	os.Setenv("REDIS_AUTH", "password123")
	os.Setenv("REDIS_PERSECOND_TLS", "false")
	os.Setenv("REDIS_PERSECOND_AUTH", "password123")
	os.Setenv("RUNTIME_WATCH_ROOT", runtimeWatchRoot)

	return testConfigReload(grpcPort, perSecond, local_cache_size)
}

func testBasicConfigReloadWithRedisSentinel(grpcPort, perSecond string, local_cache_size, runtimeWatchRoot string) func(*testing.T) {
	os.Setenv("REDIS_PERSECOND_TYPE", "sentinel")
	os.Setenv("REDIS_PERSECOND_URL", "mymaster,localhost:26399,localhost:26400,localhost:26401")
	os.Setenv("REDIS_TYPE", "sentinel")
	os.Setenv("REDIS_URL", "mymaster,localhost:26394,localhost:26395,localhost:26396")
	os.Setenv("REDIS_TLS", "false")
	os.Setenv("REDIS_PERSECOND_TLS", "false")
	os.Setenv("RUNTIME_WATCH_ROOT", runtimeWatchRoot)

	return testConfigReload(grpcPort, perSecond, local_cache_size)
}

func getCacheKey(cacheKey string, enableLocalCache bool) string {
	if enableLocalCache {
		return cacheKey + "_local"
	}

	return cacheKey
}

func testBasicBaseConfig(grpcPort, perSecond string, local_cache_size string, backend_type string) func(*testing.T) {
	return func(t *testing.T) {
		os.Setenv("REDIS_PERSECOND", perSecond)
		os.Setenv("PORT", "8082")
		os.Setenv("GRPC_PORT", grpcPort)
		os.Setenv("DEBUG_PORT", "8084")
		os.Setenv("RUNTIME_ROOT", "runtime/current")
		os.Setenv("RUNTIME_SUBDIRECTORY", "ratelimit")
		os.Setenv("REDIS_PERSECOND_SOCKET_TYPE", "tcp")
		os.Setenv("REDIS_SOCKET_TYPE", "tcp")
		os.Setenv("LOCAL_CACHE_SIZE_IN_BYTES", local_cache_size)
		os.Setenv("USE_STATSD", "false")
		os.Setenv("BACKEND_TYPE", backend_type)

		local_cache_size_val, _ := strconv.Atoi(local_cache_size)
		enable_local_cache := local_cache_size_val > 0
		runner := runner.NewRunner()

		go func() {
			runner.Run()
		}()

		// HACK: Wait for the server to come up. Make a hook that we can wait on.
		time.Sleep(1 * time.Second)

		assert := assert.New(t)
		conn, err := grpc.Dial(fmt.Sprintf("localhost:%s", grpcPort), grpc.WithInsecure())
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
	os.Setenv("PORT", "8082")
	os.Setenv("GRPC_PORT", "8083")
	os.Setenv("DEBUG_PORT", "8084")
	os.Setenv("RUNTIME_ROOT", "runtime/current")
	os.Setenv("RUNTIME_SUBDIRECTORY", "ratelimit")

	os.Setenv("REDIS_PERSECOND_URL", "localhost:6380")
	os.Setenv("REDIS_URL", "localhost:6379")
	os.Setenv("REDIS_TLS", "false")
	os.Setenv("REDIS_AUTH", "")
	os.Setenv("REDIS_PERSECOND_TLS", "false")
	os.Setenv("REDIS_PERSECOND_AUTH", "")
	os.Setenv("BACKEND_TYPE", "")
	os.Setenv("REDIS_TYPE", "single")
	os.Setenv("REDIS_PERSECOND_TYPE", "single")

	runner := runner.NewRunner()
	go func() {
		runner.Run()
	}()

	// HACK: Wait for the server to come up. Make a hook that we can wait on.
	time.Sleep(100 * time.Millisecond)

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

func testConfigReload(grpcPort, perSecond string, local_cache_size string) func(*testing.T) {
	return func(t *testing.T) {
		os.Setenv("REDIS_PERSECOND", perSecond)
		os.Setenv("PORT", "8082")
		os.Setenv("GRPC_PORT", grpcPort)
		os.Setenv("DEBUG_PORT", "8084")
		os.Setenv("RUNTIME_ROOT", "runtime/current")
		os.Setenv("RUNTIME_SUBDIRECTORY", "ratelimit")
		os.Setenv("REDIS_PERSECOND_SOCKET_TYPE", "tcp")
		os.Setenv("REDIS_SOCKET_TYPE", "tcp")
		os.Setenv("LOCAL_CACHE_SIZE_IN_BYTES", local_cache_size)
		os.Setenv("USE_STATSD", "false")
		os.Setenv("BACKEND_TYPE", "")

		local_cache_size_val, _ := strconv.Atoi(local_cache_size)
		enable_local_cache := local_cache_size_val > 0
		runner := runner.NewRunner()

		go func() {
			runner.Run()
		}()

		// HACK: Wait for the server to come up. Make a hook that we can wait on.
		time.Sleep(1 * time.Second)

		assert := assert.New(t)
		conn, err := grpc.Dial(fmt.Sprintf("localhost:%s", grpcPort), grpc.WithInsecure())
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

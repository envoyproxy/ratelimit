package redis_decay_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/golang/mock/gomock"
	gostats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/redis_decay"
	"github.com/envoyproxy/ratelimit/test/common"
	stats "github.com/envoyproxy/ratelimit/test/mocks/stats"
	mock_utils "github.com/envoyproxy/ratelimit/test/mocks/utils"
)

func mustNewRedisServer() *miniredis.Miniredis {
	srv, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	return srv
}

func mkClient(addr string) redis.Client {
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	return redis.NewClientImpl(context.Background(), statsStore, false, "", "tcp", "single", addr, 1,
		0, 0, nil, false, nil, 10*time.Second, "", "", time.Second, 30*time.Second, 100*time.Millisecond, false)
}

// Sunday-path: a fresh key admits the request and reports limit-1 remaining,
// and the cache key carries no window timestamp (the property that makes the
// boundary-burst impossible by construction).
func TestFirstRequestAllowedAndKeyHasNoWindowTimestamp(t *testing.T) {
	assert := assert.New(t)
	rs := mustNewRedisServer()
	defer rs.Close()
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	timeSource := mock_utils.NewMockTimeSource(controller)
	timeSource.EXPECT().UnixNow().Return(int64(1000)).AnyTimes()

	cache := redis_decay.NewDecayRateLimitCacheImpl(mkClient(rs.Addr()), timeSource, "")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key_value"), false, false, false, "", nil, false)}

	statuses := cache.DoLimit(context.Background(), request, limits)
	assert.Equal(pb.RateLimitResponse_OK, statuses[0].Code)
	assert.Equal(uint32(9), statuses[0].LimitRemaining)
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	keys := rs.Keys()
	assert.Len(keys, 1)
	assert.Equal("decay_domain_key_value", keys[0])
}

// With time frozen there is no decay: exactly limit requests pass, the next is rejected.
func TestExhaustionThenOverLimit(t *testing.T) {
	assert := assert.New(t)
	rs := mustNewRedisServer()
	defer rs.Close()
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	timeSource := mock_utils.NewMockTimeSource(controller)
	timeSource.EXPECT().UnixNow().Return(int64(1000)).AnyTimes()

	cache := redis_decay.NewDecayRateLimitCacheImpl(mkClient(rs.Addr()), timeSource, "")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key_value"), false, false, false, "", nil, false)}

	for i := 0; i < 10; i++ {
		statuses := cache.DoLimit(context.Background(), request, limits)
		assert.Equal(pb.RateLimitResponse_OK, statuses[0].Code, "request %d should be admitted", i+1)
	}
	statuses := cache.DoLimit(context.Background(), request, limits)
	assert.Equal(pb.RateLimitResponse_OVER_LIMIT, statuses[0].Code)
	assert.Equal(uint32(0), statuses[0].LimitRemaining)
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
}

// The defining decay property: crossing a minute boundary right after
// exhaustion does NOT grant a fresh budget (the fixed-window backend would).
func TestNoBudgetResetAtWindowBoundary(t *testing.T) {
	assert := assert.New(t)
	rs := mustNewRedisServer()
	defer rs.Close()
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	timeSource := mock_utils.NewMockTimeSource(controller)
	// t0 is 2s before a minute boundary; the follow-up burst is 2s after it.
	t0 := int64(1787061958)
	timeSource.EXPECT().UnixNow().Return(t0).Times(11)
	timeSource.EXPECT().UnixNow().Return(t0 + 4).Times(3)

	cache := redis_decay.NewDecayRateLimitCacheImpl(mkClient(rs.Addr()), timeSource, "")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key_value"), false, false, false, "", nil, false)}

	for i := 0; i < 11; i++ {
		cache.DoLimit(context.Background(), request, limits)
	}
	// 4s elapsed decays only 4/60*10 = 0.67 of a token; still over on the far
	// side of the boundary.
	for i := 0; i < 3; i++ {
		statuses := cache.DoLimit(context.Background(), request, limits)
		assert.Equal(pb.RateLimitResponse_OVER_LIMIT, statuses[0].Code, "burst request %d must stay rejected across the boundary", i+1)
	}
}

// Budget returns gradually at limit/period, not all at once.
func TestDecayRecovery(t *testing.T) {
	assert := assert.New(t)
	rs := mustNewRedisServer()
	defer rs.Close()
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	timeSource := mock_utils.NewMockTimeSource(controller)
	t0 := int64(1000)
	timeSource.EXPECT().UnixNow().Return(t0).Times(10)
	timeSource.EXPECT().UnixNow().Return(t0 + 30).Times(6)

	cache := redis_decay.NewDecayRateLimitCacheImpl(mkClient(rs.Addr()), timeSource, "")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key_value"), false, false, false, "", nil, false)}

	for i := 0; i < 10; i++ {
		cache.DoLimit(context.Background(), request, limits)
	}
	// 30s at 10/min recovers 5 tokens: 5 admitted, the 6th rejected.
	for i := 0; i < 5; i++ {
		statuses := cache.DoLimit(context.Background(), request, limits)
		assert.Equal(pb.RateLimitResponse_OK, statuses[0].Code, "recovered request %d should be admitted", i+1)
	}
	statuses := cache.DoLimit(context.Background(), request, limits)
	assert.Equal(pb.RateLimitResponse_OVER_LIMIT, statuses[0].Code)
}

// Rejected requests still increment the counter (parity with the production
// Lua): a flood while over limit delays recovery.
func TestRejectedRequestsConsumeBudget(t *testing.T) {
	assert := assert.New(t)
	rs := mustNewRedisServer()
	defer rs.Close()
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	timeSource := mock_utils.NewMockTimeSource(controller)
	t0 := int64(1000)
	timeSource.EXPECT().UnixNow().Return(t0).Times(15)
	timeSource.EXPECT().UnixNow().Return(t0 + 30).Times(1)

	cache := redis_decay.NewDecayRateLimitCacheImpl(mkClient(rs.Addr()), timeSource, "")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key_value"), false, false, false, "", nil, false)}

	// 10 admitted + 5 rejected leaves the counter at 15.
	for i := 0; i < 15; i++ {
		cache.DoLimit(context.Background(), request, limits)
	}
	// 30s decays 5: counter 10 + this request = 11 > 10, still rejected.
	statuses := cache.DoLimit(context.Background(), request, limits)
	assert.Equal(pb.RateLimitResponse_OVER_LIMIT, statuses[0].Code)
}

// Corrupt stored data must reset cleanly, not crash or deny.
func TestCorruptRedisDataResets(t *testing.T) {
	assert := assert.New(t)
	rs := mustNewRedisServer()
	defer rs.Close()
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	timeSource := mock_utils.NewMockTimeSource(controller)
	timeSource.EXPECT().UnixNow().Return(int64(1000)).AnyTimes()

	rs.Set("decay_domain_key_value", "not-a-decay-record")

	cache := redis_decay.NewDecayRateLimitCacheImpl(mkClient(rs.Addr()), timeSource, "")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key_value"), false, false, false, "", nil, false)}

	statuses := cache.DoLimit(context.Background(), request, limits)
	assert.Equal(pb.RateLimitResponse_OK, statuses[0].Code)
	assert.Equal(uint32(9), statuses[0].LimitRemaining)
}

// A backwards clock step must not mint free budget (elapsed clamps to zero).
func TestBackwardClockStepDoesNotMintBudget(t *testing.T) {
	assert := assert.New(t)
	rs := mustNewRedisServer()
	defer rs.Close()
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	timeSource := mock_utils.NewMockTimeSource(controller)
	timeSource.EXPECT().UnixNow().Return(int64(1000)).Times(1)
	timeSource.EXPECT().UnixNow().Return(int64(900)).Times(1)

	cache := redis_decay.NewDecayRateLimitCacheImpl(mkClient(rs.Addr()), timeSource, "")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key_value"), false, false, false, "", nil, false)}

	cache.DoLimit(context.Background(), request, limits)
	statuses := cache.DoLimit(context.Background(), request, limits)
	assert.Equal(pb.RateLimitResponse_OK, statuses[0].Code)
	// Second request must count on top of the first (remaining 8), not be
	// treated as a fresh window (remaining 9).
	assert.Equal(uint32(8), statuses[0].LimitRemaining)
}

// Descriptors with no configured limit are admitted without touching Redis.
func TestNilLimitSkipped(t *testing.T) {
	assert := assert.New(t)
	rs := mustNewRedisServer()
	defer rs.Close()
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	timeSource.EXPECT().UnixNow().Return(int64(1000)).AnyTimes()

	cache := redis_decay.NewDecayRateLimitCacheImpl(mkClient(rs.Addr()), timeSource, "")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{nil}

	statuses := cache.DoLimit(context.Background(), request, limits)
	assert.Equal(pb.RateLimitResponse_OK, statuses[0].Code)
	assert.Equal(uint32(math.MaxUint32), statuses[0].LimitRemaining)
	assert.Empty(rs.Keys())
}

// Two descriptors in one request are limited independently.
func TestMultipleDescriptorsIndependent(t *testing.T) {
	assert := assert.New(t)
	rs := mustNewRedisServer()
	defer rs.Close()
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	timeSource := mock_utils.NewMockTimeSource(controller)
	timeSource.EXPECT().UnixNow().Return(int64(1000)).AnyTimes()

	cache := redis_decay.NewDecayRateLimitCacheImpl(mkClient(rs.Addr()), timeSource, "")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"keyA", "a"}}, {{"keyB", "b"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(1, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("keyA_a"), false, false, false, "", nil, false),
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("keyB_b"), false, false, false, "", nil, false),
	}

	cache.DoLimit(context.Background(), request, limits)
	statuses := cache.DoLimit(context.Background(), request, limits)
	assert.Equal(pb.RateLimitResponse_OVER_LIMIT, statuses[0].Code)
	assert.Equal(pb.RateLimitResponse_OK, statuses[1].Code)
}

// Redis being down fails open: requests are admitted, nothing panics.
func TestRedisDownFailsOpen(t *testing.T) {
	assert := assert.New(t)
	rs := mustNewRedisServer()
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	timeSource := mock_utils.NewMockTimeSource(controller)
	timeSource.EXPECT().UnixNow().Return(int64(1000)).AnyTimes()

	client := mkClient(rs.Addr())
	rs.Close()

	cache := redis_decay.NewDecayRateLimitCacheImpl(client, timeSource, "")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key_value"), false, false, false, "", nil, false)}

	assert.NotPanics(func() {
		statuses := cache.DoLimit(context.Background(), request, limits)
		assert.Equal(pb.RateLimitResponse_OK, statuses[0].Code)
	})
}

// Envoy's hits_addend must be honored: one request can consume several hits.
func TestHitsAddendConsumesMultipleTokens(t *testing.T) {
	assert := assert.New(t)
	rs := mustNewRedisServer()
	defer rs.Close()
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	timeSource := mock_utils.NewMockTimeSource(controller)
	timeSource.EXPECT().UnixNow().Return(int64(1000)).AnyTimes()

	cache := redis_decay.NewDecayRateLimitCacheImpl(mkClient(rs.Addr()), timeSource, "")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 5)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key_value"), false, false, false, "", nil, false)}

	statuses := cache.DoLimit(context.Background(), request, limits)
	assert.Equal(pb.RateLimitResponse_OK, statuses[0].Code)
	assert.Equal(uint32(5), statuses[0].LimitRemaining, "an addend of 5 must consume 5 tokens")

	statuses = cache.DoLimit(context.Background(), request, limits)
	assert.Equal(pb.RateLimitResponse_OK, statuses[0].Code)
	assert.Equal(uint32(0), statuses[0].LimitRemaining)

	statuses = cache.DoLimit(context.Background(), request, limits)
	assert.Equal(pb.RateLimitResponse_OVER_LIMIT, statuses[0].Code)
}

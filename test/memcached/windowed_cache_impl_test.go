package memcached_test

import (
	"strconv"
	"testing"

	"github.com/bradfitz/gomemcache/memcache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/memcached"
	"github.com/envoyproxy/ratelimit/test/common"
	mock_memcached "github.com/envoyproxy/ratelimit/test/mocks/memcached"
	mock_utils "github.com/envoyproxy/ratelimit/test/mocks/utils"
	"github.com/golang/mock/gomock"
	"github.com/golang/protobuf/ptypes/duration"
	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
)

func TestMemcachedWindowed(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	client := mock_memcached.NewMockClient(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	cache := memcached.NewWindowedRateLimitCacheImpl(client, timeSource, nil, 0, nil, statsStore, 0.8, "")

	// Test 1
	// periode = 1 second
	// limit = 10 request/second
	// emissionInterval = 0.1 second
	// request = 1
	// increment = emissionInterval*request = 0.1 second

	// arriveAt = 1 second
	// tat = 1 second

	// newTat should be max(arriveAt,tat)+increment = 1.1 second
	// DurationUntilReset should be newTat-arriveat = 0.1 minute
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)

	client.EXPECT().GetMulti([]string{"domain_key_value_0"}).Return(
		getMultiResult(map[string]int{"domain_key_value_0": 1e9}), nil,
	)
	client.EXPECT().Set(&memcache.Item{
		Key:        "domain_key_value_0",
		Value:      []byte(strconv.FormatInt(int64(1e9+1e8), 10)),
		Expiration: int32(1),
	})

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: &duration.Duration{Nanos: 1e8}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
}

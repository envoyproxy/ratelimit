package redis_test

import (
	"testing"

	"github.com/lyft/gostats"
	pb "github.com/lyft/ratelimit/proto/ratelimit"
	"github.com/lyft/ratelimit/src/config"
	"github.com/lyft/ratelimit/src/redis"

	"github.com/golang/mock/gomock"
	"github.com/lyft/ratelimit/test/common"
	"github.com/lyft/ratelimit/test/mocks/redis"
	"github.com/stretchr/testify/assert"
)

func TestRedis(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	pool := mock_redis.NewMockPool(controller)
	timeSource := mock_redis.NewMockTimeSource(controller)
	connection := mock_redis.NewMockConnection(controller)
	response := mock_redis.NewMockResponse(controller)
	cache := redis.NewRateLimitCacheImpl(pool, timeSource)
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	pool.EXPECT().Get().Return(connection)
	timeSource.EXPECT().UnixNow().Return(int64(1234))
	connection.EXPECT().PipeAppend("INCR", "hello_hello_world_1234")
	connection.EXPECT().PipeAppend("EXPIRE", "hello_hello_world_1234", int64(1))
	connection.EXPECT().PipeResponse().Return(response)
	response.EXPECT().Int().Return(int64(5))
	connection.EXPECT().PipeResponse()
	pool.EXPECT().Put(connection)

	request := common.NewRateLimitRequest("hello", [][][2]string{{{"hello", "world"}}})
	limits := []*config.RateLimit{
		{"", config.RateLimitStats{},
			&pb.RateLimit{RequestsPerUnit: 10, Unit: pb.RateLimit_SECOND}}}
	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{pb.RateLimitResponse_OK, limits[0].Limit, 5}},
		cache.DoLimit(nil, request, limits))

	pool.EXPECT().Get().Return(connection)
	timeSource.EXPECT().UnixNow().Return(int64(1234))
	connection.EXPECT().PipeAppend("INCR", "test-domain_hello_world_hello2_world2_1200")
	connection.EXPECT().PipeAppend(
		"EXPIRE", "test-domain_hello_world_hello2_world2_1200", int64(60))
	connection.EXPECT().PipeResponse().Return(response)
	response.EXPECT().Int().Return(int64(11))
	connection.EXPECT().PipeResponse()
	pool.EXPECT().Put(connection)

	request = common.NewRateLimitRequest(
		"test-domain",
		[][][2]string{
			{{"hello", "world"}},
			{{"hello", "world"}, {"hello2", "world2"}},
		})
	limits = []*config.RateLimit{
		nil,
		{"", config.RateLimitStats{},
			&pb.RateLimit{RequestsPerUnit: 10, Unit: pb.RateLimit_MINUTE}}}
	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{pb.RateLimitResponse_OK, nil, 0},
			{pb.RateLimitResponse_OVER_LIMIT, limits[1].Limit, 0}},
		cache.DoLimit(nil, request, limits))

	pool.EXPECT().Get().Return(connection)
	timeSource.EXPECT().UnixNow().Return(int64(1000000))
	connection.EXPECT().PipeAppend("INCR", "test-domain_hello_world_997200")
	connection.EXPECT().PipeAppend(
		"EXPIRE", "test-domain_hello_world_997200", int64(3600))
	connection.EXPECT().PipeAppend("INCR", "test-domain_hello_world_hello2_world2_950400")
	connection.EXPECT().PipeAppend(
		"EXPIRE", "test-domain_hello_world_hello2_world2_950400", int64(86400))
	connection.EXPECT().PipeResponse().Return(response)
	response.EXPECT().Int().Return(int64(11))
	connection.EXPECT().PipeResponse()
	connection.EXPECT().PipeResponse().Return(response)
	response.EXPECT().Int().Return(int64(13))
	connection.EXPECT().PipeResponse()
	pool.EXPECT().Put(connection)

	request = common.NewRateLimitRequest(
		"test-domain",
		[][][2]string{
			{{"hello", "world"}},
			{{"hello", "world"}, {"hello2", "world2"}},
		})
	limits = []*config.RateLimit{
		{"", config.RateLimitStats{}, &pb.RateLimit{RequestsPerUnit: 10, Unit: pb.RateLimit_HOUR}},
		{"", config.RateLimitStats{}, &pb.RateLimit{RequestsPerUnit: 10, Unit: pb.RateLimit_DAY}}}
	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{pb.RateLimitResponse_OVER_LIMIT, limits[0].Limit, 0},
			{pb.RateLimitResponse_OVER_LIMIT, limits[1].Limit, 0}},
		cache.DoLimit(nil, request, limits))

	// Test Near Limit Stats. Under Near Limit Ratio
	pool.EXPECT().Get().Return(connection)
	timeSource.EXPECT().UnixNow().Return(int64(1000000))
	connection.EXPECT().PipeAppend("INCR", "test-domain_hello_world_997200")
	connection.EXPECT().PipeAppend(
		"EXPIRE", "test-domain_hello_world_997200", int64(3600))
	connection.EXPECT().PipeResponse().Return(response)
	response.EXPECT().Int().Return(int64(11))
	connection.EXPECT().PipeResponse()
	pool.EXPECT().Put(connection)

	request = common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}})

	limits = []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimit_HOUR, "key", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{pb.RateLimitResponse_OK, limits[0].Limit, 4}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	pool.EXPECT().Get().Return(connection)
	timeSource.EXPECT().UnixNow().Return(int64(1000000))
	connection.EXPECT().PipeAppend("INCR", "test-domain_hello_world_997200")
	connection.EXPECT().PipeAppend(
		"EXPIRE", "test-domain_hello_world_997200", int64(3600))
	connection.EXPECT().PipeResponse().Return(response)
	response.EXPECT().Int().Return(int64(12))
	connection.EXPECT().PipeResponse()
	pool.EXPECT().Put(connection)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{pb.RateLimitResponse_OK, limits[0].Limit, 3}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Test Near Limit Stats. We went OVER_LIMIT, but the near_limit counter only increases
	// when we are near limit, not after we have passed the limit.
	pool.EXPECT().Get().Return(connection)
	timeSource.EXPECT().UnixNow().Return(int64(1000000))
	connection.EXPECT().PipeAppend("INCR", "test-domain_hello_world_997200")
	connection.EXPECT().PipeAppend(
		"EXPIRE", "test-domain_hello_world_997200", int64(3600))
	connection.EXPECT().PipeResponse().Return(response)
	response.EXPECT().Int().Return(int64(16))
	connection.EXPECT().PipeResponse()
	pool.EXPECT().Put(connection)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{pb.RateLimitResponse_OVER_LIMIT, limits[0].Limit, 0}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
}

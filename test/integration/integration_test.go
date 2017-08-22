// +build integration

package integration_test

import (
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	pb "github.com/lyft/ratelimit/proto/ratelimit"
	"github.com/lyft/ratelimit/src/service_cmd/runner"
	"golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/lyft/ratelimit/test/common"
	"github.com/stretchr/testify/assert"
)

func newDescriptorStatus(
	status pb.RateLimitResponse_Code, requestsPerUnit uint32,
	unit pb.RateLimit_Unit, limitRemaining uint32) *pb.RateLimitResponse_DescriptorStatus {

	return &pb.RateLimitResponse_DescriptorStatus{
		status, &pb.RateLimit{RequestsPerUnit: requestsPerUnit, Unit: unit}, limitRemaining}
}

func TestBasicConfig(t *testing.T) {
	os.Setenv("PORT", "8082")
	os.Setenv("GRPC_PORT", "8083")
	os.Setenv("DEBUG_PORT", "8084")
	os.Setenv("RUNTIME_ROOT", "runtime/current")
	os.Setenv("RUNTIME_SUBDIRECTORY", "ratelimit")

	go func() {
		runner.Run()
	}()

	// HACK: Wait for the server to come up. Make a hook that we can wait on.
	time.Sleep(100 * time.Millisecond)

	assert := assert.New(t)
	conn, err := grpc.Dial("localhost:8083", grpc.WithInsecure())
	assert.NoError(err)
	defer conn.Close()
	c := pb.NewRateLimitServiceClient(conn)

	response, err := c.ShouldRateLimit(
		context.Background(),
		common.NewRateLimitRequest("foo", [][][2]string{{{"hello", "world"}}}, 1))
	assert.Equal(
		&pb.RateLimitResponse{
			pb.RateLimitResponse_OK,
			[]*pb.RateLimitResponse_DescriptorStatus{{pb.RateLimitResponse_OK, nil, 0}}},
		response)
	assert.NoError(err)

	response, err = c.ShouldRateLimit(
		context.Background(),
		common.NewRateLimitRequest("basic", [][][2]string{{{"key1", "foo"}}}, 1))
	assert.Equal(
		&pb.RateLimitResponse{
			pb.RateLimitResponse_OK,
			[]*pb.RateLimitResponse_DescriptorStatus{
				newDescriptorStatus(pb.RateLimitResponse_OK, 50, pb.RateLimit_SECOND, 49)}},
		response)
	assert.NoError(err)

	// Now come up with a random key, and go over limit for a minute limit which should always work.
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	randomInt := r.Int()
	for i := 0; i < 25; i++ {
		response, err = c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest(
				"another", [][][2]string{{{"key2", strconv.Itoa(randomInt)}}}, 1))

		status := pb.RateLimitResponse_OK
		limitRemaining := uint32(20 - (i + 1))
		if i >= 20 {
			status = pb.RateLimitResponse_OVER_LIMIT
			limitRemaining = 0
		}

		assert.Equal(
			&pb.RateLimitResponse{
				status,
				[]*pb.RateLimitResponse_DescriptorStatus{
					newDescriptorStatus(status, 20, pb.RateLimit_MINUTE, limitRemaining)}},
			response)
		assert.NoError(err)
	}

	// Limit now against 2 keys in the same domain.
	randomInt = r.Int()
	for i := 0; i < 15; i++ {
		response, err = c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest(
				"another",
				[][][2]string{
					{{"key2", strconv.Itoa(randomInt)}},
					{{"key3", strconv.Itoa(randomInt)}}}, 1))

		status := pb.RateLimitResponse_OK
		limitRemaining1 := uint32(20 - (i + 1))
		limitRemaining2 := uint32(10 - (i + 1))
		if i >= 10 {
			status = pb.RateLimitResponse_OVER_LIMIT
			limitRemaining2 = 0
		}

		assert.Equal(
			&pb.RateLimitResponse{
				status,
				[]*pb.RateLimitResponse_DescriptorStatus{
					newDescriptorStatus(pb.RateLimitResponse_OK, 20, pb.RateLimit_MINUTE, limitRemaining1),
					newDescriptorStatus(status, 10, pb.RateLimit_HOUR, limitRemaining2)}},
			response)
		assert.NoError(err)
	}
}

package limiter

import (
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"golang.org/x/net/context"
)

// Interface for interacting with a cache backend for rate limiting.
type RateLimitCache interface {
	// Contact the cache and perform rate limiting for a set of descriptors and limits.
	// @param ctx supplies the request context.
	// @param request supplies the ShouldRateLimit service request.
	// @param limits supplies the list of associated limits. It's possible for a limit to be nil
	//               which means that the associated descriptor does not need to be checked. This
	//               is done for simplicity reasons in the overall service API. The length of this
	//               list must be same as the length of the descriptors list.
	// @return a list of DescriptorStatuses which corresponds to each passed in descriptor/limit pair.
	// 				 Throws RedisError if there was any error talking to the cache.
	DoLimit(
		ctx context.Context,
		request *pb.RateLimitRequest,
		limits []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus

	// Waits for any unfinished asynchronous work. This may be used by unit tests,
	// since the memcache cache does increments in a background gorountine.
	Flush()
}

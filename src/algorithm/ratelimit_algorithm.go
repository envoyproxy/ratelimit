package algorithm

import (
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/utils"
	"google.golang.org/protobuf/types/known/durationpb"
)

type RatelimitAlgorithm interface {
	IsOverLimit(limit *config.RateLimit, results int64, hitsAddend int64) (bool, int64, *durationpb.Duration)
	IsOverLimitWithLocalCache(key string) bool

	GetResponseDescriptorStatus(key string, limit *config.RateLimit, results int64, isOverLimitWithLocalCache bool, hitsAddend int64) *pb.RateLimitResponse_DescriptorStatus
	GetNewTat() int64
	GetArrivedAt() int64

	GenerateCacheKeys(request *pb.RateLimitRequest,
		limits []*config.RateLimit, hitsAddend int64) []utils.CacheKey
	PopulateStats(limit *config.RateLimit, nearLimit uint64, overLimit uint64, overLimitWithLocalCache uint64)
}

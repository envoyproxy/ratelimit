package stats

import (
	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
)

type Manager interface {
	AddTotalHits(u uint64, rlStats RateLimitStats, domain string, descriptor *pb_struct.RateLimitDescriptor)
	AddOverLimit(u uint64, rlStats RateLimitStats, domain string, descriptor *pb_struct.RateLimitDescriptor)
	AddNearLimit(u uint64, rlStats RateLimitStats, domain string, descriptor *pb_struct.RateLimitDescriptor)
	AddOverLimitWithLocalCache(u uint64, rlStats RateLimitStats, domain string, descriptor *pb_struct.RateLimitDescriptor)
	NewStats(key string) RateLimitStats
}

package ratelimit

import (
	"strconv"

	"github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v2"
)

func limitHeader(descriptor *pb.RateLimitResponse_DescriptorStatus) *core.HeaderValue {
	return &core.HeaderValue{
		Key:   "X-RateLimit-Limit",
		Value: strconv.FormatUint(uint64(descriptor.CurrentLimit.RequestsPerUnit), 10),
	}
}

func remainingHeader(descriptor *pb.RateLimitResponse_DescriptorStatus) *core.HeaderValue {
	return &core.HeaderValue{
		Key:   "X-RateLimit-Remaining",
		Value: strconv.FormatUint(uint64(descriptor.LimitRemaining), 10),
	}
}

func resetHeader(
	descriptor *pb.RateLimitResponse_DescriptorStatus, now int64) *core.HeaderValue {

	return &core.HeaderValue{
		Key:   "X-RateLimit-Reset",
		Value: strconv.FormatInt(calculateReset(descriptor, now), 10),
	}
}

func calculateReset(descriptor *pb.RateLimitResponse_DescriptorStatus, now int64) int64 {
	sec := unitInSeconds(descriptor.CurrentLimit.Unit)
	return sec - now%sec
}

func unitInSeconds(unit pb.RateLimitResponse_RateLimit_Unit) int64 {
	switch unit {
	case pb.RateLimitResponse_RateLimit_SECOND:
		return 1
	case pb.RateLimitResponse_RateLimit_MINUTE:
		return 60
	case pb.RateLimitResponse_RateLimit_HOUR:
		return 60 * 60
	case pb.RateLimitResponse_RateLimit_DAY:
		return 60 * 60 * 24
	default:
		panic("unknown rate limit unit")
	}
}

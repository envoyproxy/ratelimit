package utils

import (
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/golang/protobuf/ptypes/duration"
)

// Interface for a time source.
type TimeSource interface {
	// @return the current unix time in seconds.
	UnixNow() int64
}

// Convert a rate limit into a time divider.
// @param unit supplies the unit to convert.
// @return the divider to use in time computations.
func UnitToDivider(unit pb.RateLimitResponse_RateLimit_Unit) int64 {
	switch unit {
	case pb.RateLimitResponse_RateLimit_SECOND:
		return 1
	case pb.RateLimitResponse_RateLimit_MINUTE:
		return 60
	case pb.RateLimitResponse_RateLimit_HOUR:
		return 60 * 60
	case pb.RateLimitResponse_RateLimit_DAY:
		return 60 * 60 * 24
	}

	panic("should not get here")
}

func CalculateReset(currentLimit *pb.RateLimitResponse_RateLimit, timeSource TimeSource) *duration.Duration {
	sec := UnitToDivider(currentLimit.Unit)
	now := timeSource.UnixNow()
	return &duration.Duration{Seconds: sec - now%sec}
}

func Max(a uint32, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

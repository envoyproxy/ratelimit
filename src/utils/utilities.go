package utils

import (
	"time"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/golang/protobuf/ptypes/duration"
)

const secondToNanosecondRate = 1e9

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

func MaxInt64(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func MinInt64(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func MaxUint32(a uint32, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

func NanosecondsToSeconds(nanoseconds int64) int64 {
	return nanoseconds / secondToNanosecondRate
}

func NanosecondsToDuration(nanoseconds int64) *duration.Duration {
	nanos := nanoseconds
	secs := nanos / secondToNanosecondRate
	nanos -= secs * secondToNanosecondRate
	return &duration.Duration{Seconds: secs, Nanos: int32(nanos)}
}

func SecondsToNanoseconds(second int64) int64 {
	time.Now()
	return second * secondToNanosecondRate
}

func CalculateFixedReset(currentLimit *pb.RateLimitResponse_RateLimit, timeSource TimeSource) *duration.Duration {
	sec := UnitToDivider(currentLimit.Unit)
	now := timeSource.UnixNow()
	return &duration.Duration{Seconds: sec - now%sec}
}

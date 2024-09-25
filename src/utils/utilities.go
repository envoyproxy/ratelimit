package utils

import (
	"regexp"
	"strings"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"google.golang.org/protobuf/types/known/durationpb"
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

func CalculateReset(unit *pb.RateLimitResponse_RateLimit_Unit, timeSource TimeSource) *durationpb.Duration {
	sec := UnitToDivider(*unit)
	now := timeSource.UnixNow()
	return &durationpb.Duration{Seconds: sec - now%sec}
}

func Max(a uint32, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

// Mask credentials from a redis connection string like
// foo,redis://user:pass@redisurl1,redis://user:pass@redisurl2
// resulting in
// foo,redis://*****@redisurl1,redis://*****@redisurl2
func MaskCredentialsInUrl(url string) string {
	urls := strings.Split(url, ",")

	for i := 0; i < len(urls); i++ {
		url := urls[i]
		authUrlParts := strings.Split(url, "@")
		if len(authUrlParts) > 1 && strings.HasPrefix(authUrlParts[0], "redis://") {
			urls[i] = "redis://*****@" + authUrlParts[len(authUrlParts)-1]
		}
	}

	return strings.Join(urls, ",")
}

var (
	// remoteAddressRegex is used to replace remote addresses in stat names with a sanitized version.
	// 1. replace masked_remote_address_1.2.3.4/32 with masked_remote_address_1_2_3_4_32
	// 2. replace remote_address_1.2.3.4 with remote_address_1_2_3_4
	remoteAddressRegex = regexp.MustCompile(`remote_address_\d+\.\d+\.\d+\.\d+`)
)

// SanitizeStatName remove invalid characters from the stat name.
func SanitizeStatName(s string) string {
	r := strings.NewReplacer(":", "_", "|", "_")
	result := r.Replace(s)

	for _, m := range remoteAddressRegex.FindAllString(s, -1) {
		result = strings.Replace(result, m, strings.Replace(m, ".", "_", -1), -1)
	}
	return result
}

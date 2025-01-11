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
	case pb.RateLimitResponse_RateLimit_WEEK:
		return 60 * 60 * 24 * 7
	case pb.RateLimitResponse_RateLimit_MONTH:
		return 60 * 60 * 24 * 30
	case pb.RateLimitResponse_RateLimit_YEAR:
		return 60 * 60 * 24 * 365
	}

	panic("should not get here")
}

func CalculateReset(unit *pb.RateLimitResponse_RateLimit_Unit, timeSource TimeSource) *durationpb.Duration {
	sec := UnitToDivider(*unit)
	now := timeSource.UnixNow()
	return &durationpb.Duration{Seconds: sec - now%sec}
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

var ipv4Regex = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)

// Remove invalid characters from the stat name.
func SanitizeStatName(s string) string {
	r := strings.NewReplacer(":", "_", "|", "_")
	return ipv4Regex.ReplaceAllStringFunc(r.Replace(s), func(ip string) string {
		return strings.ReplaceAll(ip, ".", "_")
	})
}

func GetHitsAddends(request *pb.RateLimitRequest) []uint64 {
	hitsAddends := make([]uint64, len(request.Descriptors))

	for i, descriptor := range request.Descriptors {
		if descriptor.HitsAddend != nil {
			// If the per descriptor hits_addend is set, use that. It allows to be zero. The zero value is
			// means check only by no increment the hits.
			hitsAddends[i] = descriptor.HitsAddend.Value
		} else {
			// If the per descriptor hits_addend is not set, use the request's hits_addend. If the value is
			// zero (default value if not specified by the caller), use 1 for backward compatibility.
			hitsAddends[i] = uint64(max(1, uint64(request.HitsAddend)))
		}
	}
	return hitsAddends
}

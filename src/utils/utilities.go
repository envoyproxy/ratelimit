package utils

import (
	"regexp"
	"strings"
	"time"

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

// ExpirationSeconds returns the number of seconds, evaluated from the
// current time, until the given rate limit unit's window ends. When
// useCalendarMonth is true, MONTH reflects the actual calendar-aligned
// window (the 1st through the last day of the month, UTC) instead of the
// fixed-length UnitToDivider approximation. WEEK keeps the fixed 7-day TTL
// regardless of the configured week start: it always outlives the remaining
// window, and keeps existing TTL behaviour unchanged.
func ExpirationSeconds(unit pb.RateLimitResponse_RateLimit_Unit, timeSource TimeSource, useCalendarMonth bool) int64 {
	return RateLimitExpirationSeconds(&pb.RateLimitResponse_RateLimit{Unit: unit}, timeSource, useCalendarMonth)
}

func CalculateReset(unit *pb.RateLimitResponse_RateLimit_Unit, timeSource TimeSource, useCalendarMonth bool, weekStart time.Weekday) *durationpb.Duration {
	return CalculateRateLimitReset(&pb.RateLimitResponse_RateLimit{Unit: *unit}, timeSource, useCalendarMonth, weekStart)
}

// RateLimitMultiplier returns the number of units in a window. An omitted
// multiplier defaults to one. Treat zero defensively as one; request and
// configuration validation reject explicitly configured zero values.
func RateLimitMultiplier(limit *pb.RateLimitResponse_RateLimit) int64 {
	return int64(max(limit.GetUnitMultiplier().GetValue(), 1))
}

// RateLimitWindow returns the UTC start and end of the window containing now.
// Fixed windows are anchored to the Unix epoch. Calendar month windows are
// anchored to January 1970; week windows use the configured start of that week.
// All arithmetic is in seconds, avoiding time.Duration's much smaller range.
func RateLimitWindow(limit *pb.RateLimitResponse_RateLimit, now int64, useCalendarMonth bool, weekStart time.Weekday) (int64, int64) {
	multiplier := RateLimitMultiplier(limit)
	if useCalendarMonth && limit.Unit == pb.RateLimitResponse_RateLimit_MONTH {
		t := time.Unix(now, 0).UTC()
		month := int64(t.Year()-1970)*12 + int64(t.Month()-1)
		startMonth := floorDivide(month, multiplier) * multiplier
		start := time.Date(1970, time.Month(startMonth+1), 1, 0, 0, 0, 0, time.UTC).Unix()
		end := time.Date(1970, time.Month(startMonth+multiplier+1), 1, 0, 0, 0, 0, time.UTC).Unix()
		return start, end
	}
	period := UnitToDivider(limit.Unit) * multiplier
	var anchor int64
	if limit.Unit == pb.RateLimitResponse_RateLimit_WEEK {
		anchor = WeekStartUnix(0, weekStart)
	}
	start := anchor + floorDivide(now-anchor, period)*period
	return start, start + period
}

func floorDivide(value, divisor int64) int64 {
	quotient := value / divisor
	if value%divisor < 0 {
		quotient--
	}
	return quotient
}

// RateLimitExpirationSeconds preserves the full-window TTL used by fixed
// windows. Calendar month TTLs end at the next window boundary.
func RateLimitExpirationSeconds(limit *pb.RateLimitResponse_RateLimit, timeSource TimeSource, useCalendarMonth bool) int64 {
	if useCalendarMonth && limit.Unit == pb.RateLimitResponse_RateLimit_MONTH {
		now := timeSource.UnixNow()
		// MONTH windows ignore the week-start argument.
		_, end := RateLimitWindow(limit, now, true, time.Thursday)
		return end - now
	}
	return UnitToDivider(limit.Unit) * RateLimitMultiplier(limit)
}

func CalculateRateLimitReset(limit *pb.RateLimitResponse_RateLimit, timeSource TimeSource, useCalendarMonth bool, weekStart time.Weekday) *durationpb.Duration {
	now := timeSource.UnixNow()
	_, end := RateLimitWindow(limit, now, useCalendarMonth, weekStart)
	// Protobuf Duration is limited to 10,000 years, even though the unit and
	// multiplier can describe a longer window. Keep the wire response valid.
	const maxDurationSeconds = 315576000000
	return &durationpb.Duration{Seconds: min(end-now, maxDurationSeconds)}
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

// SanitizeStatKeyValue replaces the statsd hierarchy separator '.' with '_' so that
// dots in a descriptor key or value do not create unintended metric hierarchy levels
// (which, for example, break the Prometheus statsd_exporter metric-name -> label mapping).
func SanitizeStatKeyValue(s string) string {
	return strings.ReplaceAll(s, ".", "_")
}

type HitsAddend struct {
	Value      uint64
	IsNegative bool
}

func GetHitsAddends(request *pb.RateLimitRequest) []HitsAddend {
	hitsAddends := make([]HitsAddend, len(request.Descriptors))

	for i, descriptor := range request.Descriptors {
		if descriptor.HitsAddend != nil {
			// If the per descriptor hits_addend is set, use that. It allows to be zero. The zero value is
			// means check only by no increment the hits.
			hitsAddends[i].Value = descriptor.HitsAddend.Value
		} else {
			// If the per descriptor hits_addend is not set, use the request's hits_addend. If the value is
			// zero (default value if not specified by the caller), use 1 for backward compatibility.
			hitsAddends[i].Value = uint64(max(1, uint64(request.HitsAddend)))
		}
		hitsAddends[i].IsNegative = descriptor.GetIsNegativeHits()
	}
	return hitsAddends
}

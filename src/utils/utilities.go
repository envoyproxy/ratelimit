package utils

import (
	"strings"
	"time"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/golang/protobuf/ptypes/duration"
	logger "github.com/sirupsen/logrus"
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
	case pb.RateLimitResponse_RateLimit_MONTH:
		// This cannot be hardcoded to 30 days, as there are months with 31 days and the TTL will expire before the end of the month
		return 60 * 60 * 24 * daysOfCurrentMonth(time.Now().Unix()) //todo: get timesource instead of time.now.
	}
	panic("should not get here")
}

func daysOfCurrentMonth(unix int64) int64 {
	y, m, _ := CurrentTime(unix).Date()

	return daysIn(m, y)

}

// daysIn returns the number of days in a month for a given year.
func daysIn(m time.Month, year int) int64 {
	// This is equivalent to time.daysIn(m, year).
	return int64(time.Date(year, m+1, 0, 0, 0, 0, 0, time.UTC).Day())
}

// CalculateReset calculates the reset time for a given unit and time source.
func CalculateReset(unit *pb.RateLimitResponse_RateLimit_Unit, timeSource TimeSource) *duration.Duration {
	now := timeSource.UnixNow()
	unitInSec := UnitToDivider(*unit)

	if *unit == pb.RateLimitResponse_RateLimit_MONTH {
		y, m, _ := CurrentTime(timeSource.UnixNow()).Date()
		_, lastDayOfMonth := MonthInterval(y, m)

		// calculate seconds between now and the end of the month
		seconds := lastDayOfMonth.Unix() - now
		logger.Debugf("the reset duration for until the end of the current month is %v seconds", seconds)

		return &duration.Duration{Seconds: seconds}
	}

	// This doesn't work for months or years, because the exact number of days in a month or year are not always the same.
	return &duration.Duration{Seconds: unitInSec - now%unitInSec}

}

func CurrentTime(unixNow int64) time.Time {
	return time.Unix(unixNow, 0)
}

func MonthInterval(y int, m time.Month) (firstDay, lastDay time.Time) {
	firstDay = time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	lastDay = time.Date(y, m+1, 1, 0, 0, 0, -1, time.UTC)
	return firstDay, lastDay
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

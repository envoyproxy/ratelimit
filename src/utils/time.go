package utils

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// Interface for a rand Source for expiration jitter.
type JitterRandSource interface {
	// @return a non-negative pseudo-random 63-bit integer as an int64.
	Int63() int64
	// @param seed initializes pseudo-random generator to a deterministic state.
	Seed(seed int64)
}

type timeSourceImpl struct{}

func NewTimeSourceImpl() TimeSource {
	return &timeSourceImpl{}
}

func (this *timeSourceImpl) UnixNow() int64 {
	return time.Now().Unix()
}

// expiryUntilMonthEnd returns the duration remaining until the start of the
// next calendar month, evaluated in UTC so the result does not depend on the
// server's local timezone or DST transitions.
func expiryUntilMonthEnd(now time.Time) time.Duration {
	// Always operate in UTC to avoid timezone/DST drift
	nowUTC := now.UTC()
	// Calculate the start of the next month in UTC
	nextMonth := nowUTC.AddDate(0, 1, -nowUTC.Day()+1)
	nextMonthStart := time.Date(
		nextMonth.Year(), nextMonth.Month(), 1,
		0, 0, 0, 0, time.UTC,
	)
	// Return the duration between now and the next month boundary
	return nextMonthStart.Sub(nowUTC)
}

// MonthExpirationSeconds returns the number of seconds remaining until the
// end of the calendar month (UTC) containing the instant represented by
// nowUnix. Used as the TTL/expiration for a MONTH-unit rate limit entry.
func MonthExpirationSeconds(nowUnix int64) int64 {
	return int64(expiryUntilMonthEnd(time.Unix(nowUnix, 0)).Seconds())
}

// MonthStartUnix returns the Unix timestamp (UTC) of the first moment of the
// calendar month containing the instant represented by nowUnix. Used to
// bucket a MONTH-unit rate limit's cache key by calendar month.
func MonthStartUnix(nowUnix int64) int64 {
	t := time.Unix(nowUnix, 0).UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
}

// WeekExpirationSeconds returns the number of seconds remaining until the end of
// the current calendar week (UTC) containing nowUnix, where the week starts on
// weekStart. Used as the reported reset time for a WEEK-unit rate limit.
func WeekExpirationSeconds(nowUnix int64, weekStart time.Weekday) int64 {
	weekStartTs := WeekStartUnix(nowUnix, weekStart)
	nextWeekStart := weekStartTs + 7*24*60*60
	return nextWeekStart - nowUnix
}

// WeekStartUnix returns the Unix timestamp (UTC) of the most recent start-of-week
// boundary at or before nowUnix, where the week starts on weekStart. Used to
// bucket a WEEK-unit rate limit's cache key by calendar week.
func WeekStartUnix(nowUnix int64, weekStart time.Weekday) int64 {
	t := time.Unix(nowUnix, 0).UTC()
	offset := int(t.Weekday()) - int(weekStart)
	if offset < 0 {
		offset += 7
	}
	start := t.AddDate(0, 0, -offset)
	return time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC).Unix()
}

// ParseWeekday converts a weekday name (case-insensitive) to a time.Weekday.
// Any other value, including an empty string, returns time.Thursday (the legacy
// behaviour) and an error.
func ParseWeekday(s string) (time.Weekday, error) {
	switch strings.ToLower(s) {
	case "sunday":
		return time.Sunday, nil
	case "monday":
		return time.Monday, nil
	case "tuesday":
		return time.Tuesday, nil
	case "wednesday":
		return time.Wednesday, nil
	case "thursday":
		return time.Thursday, nil
	case "friday":
		return time.Friday, nil
	case "saturday":
		return time.Saturday, nil
	default:
		return time.Thursday, fmt.Errorf("invalid weekday %q: expected one of Sunday, Monday, Tuesday, Wednesday, Thursday, Friday, Saturday", s)
	}
}

// rand for jitter.
type lockedSource struct {
	lk  sync.Mutex
	src rand.Source
}

func NewLockedSource(seed int64) JitterRandSource {
	return &lockedSource{src: rand.NewSource(seed)}
}

func (r *lockedSource) Int63() (n int64) {
	r.lk.Lock()
	n = r.src.Int63()
	r.lk.Unlock()
	return
}

func (r *lockedSource) Seed(seed int64) {
	r.lk.Lock()
	r.src.Seed(seed)
	r.lk.Unlock()
}

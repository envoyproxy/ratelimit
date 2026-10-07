package utils_test

import (
	"testing"
	"time"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	gomock "github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/utils"
	mock_utils "github.com/envoyproxy/ratelimit/test/mocks/utils"
)

func TestMaskCredentialsInUrl(t *testing.T) {
	url := "redis:6379"
	assert.Equal(t, url, utils.MaskCredentialsInUrl(url))

	url = "redis://foo:bar@redis:6379"
	expected := "redis://*****@redis:6379"
	assert.Equal(t, expected, utils.MaskCredentialsInUrl(url))
}

func TestMaskCredentialsInUrlCluster(t *testing.T) {
	url := "redis1:6379,redis2:6379"
	assert.Equal(t, url, utils.MaskCredentialsInUrl(url))

	url = "redis://foo:bar@redis1:6379,redis://foo:bar@redis2:6379"
	expected := "redis://*****@redis1:6379,redis://*****@redis2:6379"
	assert.Equal(t, expected, utils.MaskCredentialsInUrl(url))

	url = "redis://foo:b@r@redis1:6379,redis://foo:b@r@redis2:6379"
	expected = "redis://*****@redis1:6379,redis://*****@redis2:6379"
	assert.Equal(t, expected, utils.MaskCredentialsInUrl(url))
}

func TestMaskCredentialsInUrlSentinel(t *testing.T) {
	url := "foobar,redis://foo:bar@redis1:6379,redis://foo:bar@redis2:6379"
	expected := "foobar,redis://*****@redis1:6379,redis://*****@redis2:6379"
	assert.Equal(t, expected, utils.MaskCredentialsInUrl(url))

	url = "foob@r,redis://foo:b@r@redis1:6379,redis://foo:b@r@redis2:6379"
	expected = "foob@r,redis://*****@redis1:6379,redis://*****@redis2:6379"
	assert.Equal(t, expected, utils.MaskCredentialsInUrl(url))
}

func TestCalculateResetMonthEndOfMonth(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	// 2024-01-31T23:00:00Z, one hour before the February rollover.
	now := time.Date(2024, time.January, 31, 23, 0, 0, 0, time.UTC).Unix()
	timeSource.EXPECT().UnixNow().Return(now)

	unit := pb.RateLimitResponse_RateLimit_MONTH
	reset := utils.CalculateReset(&unit, timeSource, true, time.Thursday)

	assert.EqualValues(t, (1 * time.Hour).Seconds(), reset.Seconds)
}

func TestCalculateResetMonthDisabledUsesLegacyDivider(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	// With the feature flag off, MONTH must keep behaving like the legacy
	// fixed 30-day divider, regardless of the actual calendar date.
	now := time.Date(2024, time.January, 31, 23, 0, 0, 0, time.UTC).Unix()
	timeSource.EXPECT().UnixNow().Return(now)

	unit := pb.RateLimitResponse_RateLimit_MONTH
	reset := utils.CalculateReset(&unit, timeSource, false, time.Thursday)

	sec := utils.UnitToDivider(unit)
	assert.EqualValues(t, sec-now%sec, reset.Seconds)
}

func TestCalculateResetMonthLeapYear(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	// 2024 is a leap year, so February has 29 days: Feb 28 -> Mar 1 is 2 days away.
	now := time.Date(2024, time.February, 28, 0, 0, 0, 0, time.UTC).Unix()
	timeSource.EXPECT().UnixNow().Return(now)

	unit := pb.RateLimitResponse_RateLimit_MONTH
	reset := utils.CalculateReset(&unit, timeSource, true, time.Thursday)

	assert.EqualValues(t, (48 * time.Hour).Seconds(), reset.Seconds)
}

func TestMonthStartUnix(t *testing.T) {
	midMonth := time.Date(2024, time.January, 15, 12, 30, 0, 0, time.UTC).Unix()
	expected := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC).Unix()
	assert.Equal(t, expected, utils.MonthStartUnix(midMonth))

	// A non-UTC instant must still bucket by its UTC calendar month.
	inTokyo := time.Date(2024, time.February, 1, 5, 0, 0, 0, time.FixedZone("JST", 9*60*60)).Unix()
	expected = time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC).Unix()
	assert.Equal(t, expected, utils.MonthStartUnix(inTokyo))
}

func TestExpirationSecondsMonth(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	now := time.Date(2024, time.February, 28, 0, 0, 0, 0, time.UTC).Unix()
	timeSource.EXPECT().UnixNow().Return(now)

	seconds := utils.ExpirationSeconds(pb.RateLimitResponse_RateLimit_MONTH, timeSource, true)
	assert.EqualValues(t, (48 * time.Hour).Seconds(), seconds)
}

func TestExpirationSecondsMonthDisabledUsesLegacyDivider(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	// No UnixNow() expectation is set: with the feature flag off, MONTH must
	// not consult the time source at all, matching the legacy UnitToDivider
	// behavior exactly.
	timeSource := mock_utils.NewMockTimeSource(controller)

	seconds := utils.ExpirationSeconds(pb.RateLimitResponse_RateLimit_MONTH, timeSource, false)
	assert.EqualValues(t, 60*60*24*30, seconds)
}

func TestExpirationSecondsNonMonthDoesNotUseTimeSource(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	// No UnixNow() expectation is set: a fixed-length unit must not consult
	// the time source at all, matching the pre-existing UnitToDivider behavior.
	timeSource := mock_utils.NewMockTimeSource(controller)

	seconds := utils.ExpirationSeconds(pb.RateLimitResponse_RateLimit_DAY, timeSource, true)
	assert.EqualValues(t, 60*60*24, seconds)
}

func TestWeekStartUnix(t *testing.T) {
	// Wednesday 2026-01-14 12:00 UTC - verify each weekday as a start day produces the correct anchor.
	wednesday := time.Date(2026, time.January, 14, 12, 0, 0, 0, time.UTC).Unix()
	assert.Equal(t, time.Date(2026, time.January, 12, 0, 0, 0, 0, time.UTC).Unix(), utils.WeekStartUnix(wednesday, time.Monday))
	assert.Equal(t, time.Date(2026, time.January, 13, 0, 0, 0, 0, time.UTC).Unix(), utils.WeekStartUnix(wednesday, time.Tuesday))
	assert.Equal(t, time.Date(2026, time.January, 14, 0, 0, 0, 0, time.UTC).Unix(), utils.WeekStartUnix(wednesday, time.Wednesday))
	assert.Equal(t, time.Date(2026, time.January, 8, 0, 0, 0, 0, time.UTC).Unix(), utils.WeekStartUnix(wednesday, time.Thursday))
	assert.Equal(t, time.Date(2026, time.January, 9, 0, 0, 0, 0, time.UTC).Unix(), utils.WeekStartUnix(wednesday, time.Friday))
	assert.Equal(t, time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC).Unix(), utils.WeekStartUnix(wednesday, time.Saturday))
	assert.Equal(t, time.Date(2026, time.January, 11, 0, 0, 0, 0, time.UTC).Unix(), utils.WeekStartUnix(wednesday, time.Sunday))

	// Exactly at week start - bucket start is now itself.
	mondayMidnight := time.Date(2026, time.January, 12, 0, 0, 0, 0, time.UTC).Unix()
	assert.Equal(t, mondayMidnight, utils.WeekStartUnix(mondayMidnight, time.Monday))

	// 1 second before week start - still the previous week.
	oneSecBefore := mondayMidnight - 1
	prevMonday := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC).Unix()
	assert.Equal(t, prevMonday, utils.WeekStartUnix(oneSecBefore, time.Monday))

	// Non-UTC must still bucket by UTC calendar week.
	inTokyo := time.Date(2026, time.January, 13, 5, 0, 0, 0, time.FixedZone("JST", 9*60*60)).Unix()
	assert.Equal(t, time.Date(2026, time.January, 12, 0, 0, 0, 0, time.UTC).Unix(), utils.WeekStartUnix(inTokyo, time.Monday))
}

func TestWeekExpirationSeconds(t *testing.T) {
	// Exactly at week start: full 7 days remain
	mondayMidnight := time.Date(2026, time.January, 12, 0, 0, 0, 0, time.UTC).Unix()
	assert.EqualValues(t, 7*24*60*60, utils.WeekExpirationSeconds(mondayMidnight, time.Monday))

	// 1 second before week end: 1 seconds remains
	oneSecBeforeEnd := time.Date(2026, time.January, 18, 23, 59, 59, 0, time.UTC).Unix()
	assert.EqualValues(t, 1, utils.WeekExpirationSeconds(oneSecBeforeEnd, time.Monday))
}

func TestCalculateResetWeek(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	// Wednesday 2026-01-14 12:00 UTC. Monday week start: 4.5 days to reset.
	now := time.Date(2026, time.January, 14, 12, 0, 0, 0, time.UTC).Unix()
	timeSource.EXPECT().UnixNow().Return(now)

	unit := pb.RateLimitResponse_RateLimit_WEEK
	reset := utils.CalculateReset(&unit, timeSource, false, time.Monday)

	nextMonday := time.Date(2026, time.January, 19, 0, 0, 0, 0, time.UTC).Unix()
	assert.EqualValues(t, nextMonday-now, reset.Seconds)
}

func TestExpirationSecondsWeekUsesLegacyDivider(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	// No UnixNow() expectation is set: WEEK keeps the fixed 7-day TTL
	// regardless of the configured week start.
	timeSource := mock_utils.NewMockTimeSource(controller)

	seconds := utils.ExpirationSeconds(pb.RateLimitResponse_RateLimit_WEEK, timeSource, false)
	assert.EqualValues(t, 60*60*24*7, seconds)
}

func TestWeekThursdayMatchesLegacyDivider(t *testing.T) {
	// The default Thursday reset day must produce the same bucket and reset time
	// as the legacy epoch-division, so existing behaviour is unchanged.
	const week = int64(60 * 60 * 24 * 7)
	for _, now := range []int64{
		0,
		1234,
		time.Date(2026, time.January, 8, 0, 0, 0, 0, time.UTC).Unix(),    // Thursday midnight
		time.Date(2026, time.January, 7, 23, 59, 59, 0, time.UTC).Unix(), // 1s before
		time.Date(2026, time.March, 3, 6, 30, 0, 0, time.UTC).Unix(),
	} {
		assert.Equal(t, (now/week)*week, utils.WeekStartUnix(now, time.Thursday), "now=%d", now)
		assert.Equal(t, week-now%week, utils.WeekExpirationSeconds(now, time.Thursday), "now=%d", now)
	}
}

func TestParseWeekday(t *testing.T) {
	cases := []struct {
		input    string
		expected time.Weekday
	}{
		{"Monday", time.Monday},
		{"monday", time.Monday},
		{"MONDAY", time.Monday},
		{"Tuesday", time.Tuesday},
		{"Wednesday", time.Wednesday},
		{"Thursday", time.Thursday},
		{"Friday", time.Friday},
		{"Saturday", time.Saturday},
		{"Sunday", time.Sunday},
	}
	for _, c := range cases {
		weekday, err := utils.ParseWeekday(c.input)
		assert.NoError(t, err, c.input)
		assert.Equal(t, c.expected, weekday, c.input)
	}
}

func TestParseWeekdayInvalid(t *testing.T) {
	for _, input := range []string{"", " Monday", "Mon", "Mondey", "1"} {
		weekday, err := utils.ParseWeekday(input)
		assert.Error(t, err, input)
		assert.Equal(t, time.Thursday, weekday, input)
	}
}

func TestSanitizeStatKeyValue(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a.b.c", "a_b_c"},
		{"nodots", "nodots"},
		{"", ""},
		{"10.0.0.1", "10_0_0_1"},
		{"foo.bar", "foo_bar"},
		{".leading", "_leading"},
		{"trailing.", "trailing_"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, utils.SanitizeStatKeyValue(c.in))
	}
}

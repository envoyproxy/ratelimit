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
	reset := utils.CalculateReset(&unit, timeSource, true)

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
	reset := utils.CalculateReset(&unit, timeSource, false)

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
	reset := utils.CalculateReset(&unit, timeSource, true)

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

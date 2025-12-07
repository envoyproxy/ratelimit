package settings

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSettingsTlsConfigUnmodified(t *testing.T) {
	settings := NewSettings()
	assert.NotNil(t, settings.RedisTlsConfig)
	assert.Nil(t, settings.RedisTlsConfig.RootCAs)
}

// Tests for RedisPoolOnEmptyBehavior
func TestRedisPoolOnEmptyBehavior_Default(t *testing.T) {
	os.Unsetenv("REDIS_POOL_ON_EMPTY_BEHAVIOR")
	os.Unsetenv("REDIS_POOL_ON_EMPTY_WAIT_DURATION")

	settings := NewSettings()

	assert.Equal(t, "CREATE", settings.RedisPoolOnEmptyBehavior)
	assert.Equal(t, 1*time.Second, settings.RedisPoolOnEmptyWaitDuration)
}

func TestRedisPoolOnEmptyBehavior_Error(t *testing.T) {
	os.Setenv("REDIS_POOL_ON_EMPTY_BEHAVIOR", "ERROR")
	os.Setenv("REDIS_POOL_ON_EMPTY_WAIT_DURATION", "0")
	defer os.Unsetenv("REDIS_POOL_ON_EMPTY_BEHAVIOR")
	defer os.Unsetenv("REDIS_POOL_ON_EMPTY_WAIT_DURATION")

	settings := NewSettings()

	assert.Equal(t, "ERROR", settings.RedisPoolOnEmptyBehavior)
	assert.Equal(t, time.Duration(0), settings.RedisPoolOnEmptyWaitDuration)
}

func TestRedisPoolOnEmptyBehavior_ErrorWithDuration(t *testing.T) {
	os.Setenv("REDIS_POOL_ON_EMPTY_BEHAVIOR", "ERROR")
	os.Setenv("REDIS_POOL_ON_EMPTY_WAIT_DURATION", "100ms")
	defer os.Unsetenv("REDIS_POOL_ON_EMPTY_BEHAVIOR")
	defer os.Unsetenv("REDIS_POOL_ON_EMPTY_WAIT_DURATION")

	settings := NewSettings()

	assert.Equal(t, "ERROR", settings.RedisPoolOnEmptyBehavior)
	assert.Equal(t, 100*time.Millisecond, settings.RedisPoolOnEmptyWaitDuration)
}

func TestRedisPoolOnEmptyBehavior_Create(t *testing.T) {
	os.Setenv("REDIS_POOL_ON_EMPTY_BEHAVIOR", "CREATE")
	os.Setenv("REDIS_POOL_ON_EMPTY_WAIT_DURATION", "500ms")
	defer os.Unsetenv("REDIS_POOL_ON_EMPTY_BEHAVIOR")
	defer os.Unsetenv("REDIS_POOL_ON_EMPTY_WAIT_DURATION")

	settings := NewSettings()

	assert.Equal(t, "CREATE", settings.RedisPoolOnEmptyBehavior)
	assert.Equal(t, 500*time.Millisecond, settings.RedisPoolOnEmptyWaitDuration)
}

func TestRedisPoolOnEmptyBehavior_Wait(t *testing.T) {
	os.Setenv("REDIS_POOL_ON_EMPTY_BEHAVIOR", "WAIT")
	defer os.Unsetenv("REDIS_POOL_ON_EMPTY_BEHAVIOR")

	settings := NewSettings()

	assert.Equal(t, "WAIT", settings.RedisPoolOnEmptyBehavior)
}

func TestRedisPoolOnEmptyBehavior_CaseInsensitive(t *testing.T) {
	// Test that lowercase values work (processing is done in driver_impl.go)
	os.Setenv("REDIS_POOL_ON_EMPTY_BEHAVIOR", "error")
	defer os.Unsetenv("REDIS_POOL_ON_EMPTY_BEHAVIOR")

	settings := NewSettings()

	// Setting stores as-is, case conversion happens in driver_impl.go
	assert.Equal(t, "error", settings.RedisPoolOnEmptyBehavior)
}

// Tests for RedisPerSecondPoolOnEmptyBehavior
func TestRedisPerSecondPoolOnEmptyBehavior_Default(t *testing.T) {
	os.Unsetenv("REDIS_PERSECOND_POOL_ON_EMPTY_BEHAVIOR")
	os.Unsetenv("REDIS_PERSECOND_POOL_ON_EMPTY_WAIT_DURATION")

	settings := NewSettings()

	assert.Equal(t, "CREATE", settings.RedisPerSecondPoolOnEmptyBehavior)
	assert.Equal(t, 1*time.Second, settings.RedisPerSecondPoolOnEmptyWaitDuration)
}

func TestRedisPerSecondPoolOnEmptyBehavior_Error(t *testing.T) {
	os.Setenv("REDIS_PERSECOND_POOL_ON_EMPTY_BEHAVIOR", "ERROR")
	os.Setenv("REDIS_PERSECOND_POOL_ON_EMPTY_WAIT_DURATION", "50ms")
	defer os.Unsetenv("REDIS_PERSECOND_POOL_ON_EMPTY_BEHAVIOR")
	defer os.Unsetenv("REDIS_PERSECOND_POOL_ON_EMPTY_WAIT_DURATION")

	settings := NewSettings()

	assert.Equal(t, "ERROR", settings.RedisPerSecondPoolOnEmptyBehavior)
	assert.Equal(t, 50*time.Millisecond, settings.RedisPerSecondPoolOnEmptyWaitDuration)
}

// Test both pools can be configured independently
func TestRedisPoolOnEmptyBehavior_IndependentConfiguration(t *testing.T) {
	os.Setenv("REDIS_POOL_ON_EMPTY_BEHAVIOR", "ERROR")
	os.Setenv("REDIS_POOL_ON_EMPTY_WAIT_DURATION", "0")
	os.Setenv("REDIS_PERSECOND_POOL_ON_EMPTY_BEHAVIOR", "CREATE")
	os.Setenv("REDIS_PERSECOND_POOL_ON_EMPTY_WAIT_DURATION", "100ms")
	defer os.Unsetenv("REDIS_POOL_ON_EMPTY_BEHAVIOR")
	defer os.Unsetenv("REDIS_POOL_ON_EMPTY_WAIT_DURATION")
	defer os.Unsetenv("REDIS_PERSECOND_POOL_ON_EMPTY_BEHAVIOR")
	defer os.Unsetenv("REDIS_PERSECOND_POOL_ON_EMPTY_WAIT_DURATION")

	settings := NewSettings()

	// Main pool configured for fail-fast
	assert.Equal(t, "ERROR", settings.RedisPoolOnEmptyBehavior)
	assert.Equal(t, time.Duration(0), settings.RedisPoolOnEmptyWaitDuration)

	// Per-second pool configured differently
	assert.Equal(t, "CREATE", settings.RedisPerSecondPoolOnEmptyBehavior)
	assert.Equal(t, 100*time.Millisecond, settings.RedisPerSecondPoolOnEmptyWaitDuration)
}

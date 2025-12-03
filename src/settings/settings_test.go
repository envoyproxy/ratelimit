package settings

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSettingsTlsConfigUnmodified(t *testing.T) {
	settings := NewSettings()
	assert.NotNil(t, settings.RedisTlsConfig)
	assert.Nil(t, settings.RedisTlsConfig.RootCAs)
}

func TestSentinelAuthSettings(t *testing.T) {
	// Clean up environment variables after test
	defer func() {
		os.Unsetenv("REDIS_SENTINEL_AUTH")
		os.Unsetenv("REDIS_PERSECOND_SENTINEL_AUTH")
		os.Unsetenv("REDIS_AUTH")
	}()

	t.Run("default values are empty", func(t *testing.T) {
		os.Unsetenv("REDIS_SENTINEL_AUTH")
		os.Unsetenv("REDIS_PERSECOND_SENTINEL_AUTH")

		settings := NewSettings()
		assert.Equal(t, "", settings.RedisSentinelAuth)
		assert.Equal(t, "", settings.RedisPerSecondSentinelAuth)
	})

	t.Run("REDIS_SENTINEL_AUTH environment variable", func(t *testing.T) {
		os.Setenv("REDIS_SENTINEL_AUTH", "sentinel-password")

		settings := NewSettings()
		assert.Equal(t, "sentinel-password", settings.RedisSentinelAuth)

		os.Unsetenv("REDIS_SENTINEL_AUTH")
	})

	t.Run("REDIS_PERSECOND_SENTINEL_AUTH environment variable", func(t *testing.T) {
		os.Setenv("REDIS_PERSECOND_SENTINEL_AUTH", "per-second-sentinel-password")

		settings := NewSettings()
		assert.Equal(t, "per-second-sentinel-password", settings.RedisPerSecondSentinelAuth)

		os.Unsetenv("REDIS_PERSECOND_SENTINEL_AUTH")
	})

	t.Run("sentinel auth with username:password format", func(t *testing.T) {
		os.Setenv("REDIS_SENTINEL_AUTH", "sentinel-user:sentinel-pass")

		settings := NewSettings()
		assert.Equal(t, "sentinel-user:sentinel-pass", settings.RedisSentinelAuth)

		os.Unsetenv("REDIS_SENTINEL_AUTH")
	})

	t.Run("sentinel auth is independent from redis auth", func(t *testing.T) {
		os.Setenv("REDIS_AUTH", "redis-password")
		os.Setenv("REDIS_SENTINEL_AUTH", "sentinel-password")

		settings := NewSettings()
		assert.Equal(t, "redis-password", settings.RedisAuth)
		assert.Equal(t, "sentinel-password", settings.RedisSentinelAuth)
		assert.NotEqual(t, settings.RedisAuth, settings.RedisSentinelAuth)

		os.Unsetenv("REDIS_AUTH")
		os.Unsetenv("REDIS_SENTINEL_AUTH")
	})
}

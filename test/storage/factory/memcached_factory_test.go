package factory_test

import (
	"testing"

	"github.com/envoyproxy/ratelimit/src/storage/factory"
	"github.com/envoyproxy/ratelimit/src/storage/strategy"
	"github.com/stretchr/testify/assert"

	stats "github.com/lyft/gostats"
)

func TestNewMemcachedClient(t *testing.T) {
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	mkMemcachedClient := func(addr []string) strategy.StorageStrategy {
		return factory.NewMemcached(statsStore, addr, "", 0, 2)
	}

	t.Run("empty server", func(t *testing.T) {
		storage := mkMemcachedClient([]string{})
		_, err := storage.GetValue("test")
		assert.Error(t, err)
	})
}

func TestNewRateLimitCacheImplFromSettingsWhenSrvCannotBeResolved(t *testing.T) {
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	assert.Panics(t, func() {
		factory.NewMemcached(statsStore, []string{}, "_something._tcp.example.invalid", 0, 2)
	})
}

func TestNewRateLimitCacheImplFromSettingsWhenHostAndPortAndSrvAreBothSet(t *testing.T) {
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	assert.Panics(t, func() {
		factory.NewMemcached(statsStore, []string{"example.org:11211"}, "_something._tcp.example.invalid", 0, 2)
	})
}

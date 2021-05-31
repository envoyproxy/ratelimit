package factory_test

import (
	"testing"

	"github.com/envoyproxy/ratelimit/src/storage/factory"
	"github.com/envoyproxy/ratelimit/src/storage/strategy"
	"github.com/stretchr/testify/assert"
)

func TestNewMemcachedClient(t *testing.T) {
	mkMemcachedClient := func(addr []string) strategy.StorageStrategy {
		return factory.NewMemcached(addr)
	}

	t.Run("empty server", func(t *testing.T) {
		storage := mkMemcachedClient([]string{})
		_, err := storage.GetValue("test")
		assert.Error(t, err)
	})
}

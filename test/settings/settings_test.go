package settings_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/settings"
)

func TestValidateNegativeHitsBackendCombinations(t *testing.T) {
	makeSettings := func(enableNegativeHits bool, backendType string, localCacheSizeInBytes int) settings.Settings {
		var s settings.Settings
		s.EnableNegativeHits = enableNegativeHits
		s.BackendType = backendType
		s.LocalCacheSizeInBytes = localCacheSizeInBytes
		return s
	}

	assert.Error(t, makeSettings(true, "memcache", 1000).Validate())

	assert.NoError(t, makeSettings(true, "memcache", 0).Validate())
	assert.NoError(t, makeSettings(true, "redis", 1000).Validate())
	assert.NoError(t, makeSettings(false, "memcache", 1000).Validate())

	// freecache clamps a negative size up to a real cache, bypassing the guard.
	assert.Error(t, makeSettings(true, "memcache", -1).Validate())
}

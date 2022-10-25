package provider

import (
	"github.com/envoyproxy/ratelimit/src/config"
)

// RateLimitConfigProvider is the interface for configurations providers.
type RateLimitConfigProvider interface {
	// ConfigUpdateEvent returns a receive-only channel for retrieve configuration updates
	// The provider implementer should send config update to this channel when it detect a config update
	// Config receiver waits for configuration updates
	ConfigUpdateEvent() <-chan ConfigUpdateEvent

	// Stop stops the configuration provider watch for configurations.
	Stop()
}

type ConfigUpdateEvent interface {
	GetConfig() (config config.RateLimitConfig, err any)
}

type ConfigUpdateEventImpl struct {
	config config.RateLimitConfig
	err    any
}

func (e *ConfigUpdateEventImpl) GetConfig() (config config.RateLimitConfig, err any) {
	return e.config, e.err
}

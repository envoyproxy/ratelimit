package provider

import (
	"github.com/envoyproxy/ratelimit/src/config"
)

type RateLimitConfigProvider interface {
	ConfigUpdateEvent() <-chan ConfigUpdateEvent
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

package config

type RateLimitConfigProvider interface {
	ConfigUpdateEvent() <-chan ConfigUpdateEvent
}

type ConfigUpdateEvent interface {
	GetConfig() (config RateLimitConfig, err any)
}

type ConfigUpdateEventImpl struct {
	config RateLimitConfig
	err    any
}

func (e *ConfigUpdateEventImpl) GetConfig() (config RateLimitConfig, err any) {
	return e.config, e.err
}

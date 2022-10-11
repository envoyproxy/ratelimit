package config

type RateLimitConfigProvider interface {
	ConfigUpdateEvent() <-chan *ConfigUpdateEvent
}

type ConfigUpdateEvent struct {
	Config RateLimitConfig
	Err    any
}

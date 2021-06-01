package service

import (
	"github.com/mediocregopher/radix/v3"
)

// Client interface for Redis
type RedisClientInterface interface {
	Do(radix.Action) error
}

type RedisClient struct {
	Client             radix.Client
	Stats              RedisStats
	ImplicitPipelining bool
}

func (r RedisClient) Do(cmd radix.Action) error {
	return r.Client.Do(cmd)
}

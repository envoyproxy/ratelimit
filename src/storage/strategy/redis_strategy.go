package strategy

import (
	"github.com/envoyproxy/ratelimit/src/storage/service"
	"github.com/mediocregopher/radix/v3"
)

type RedisStrategy struct {
	Client service.RedisClientInterface
}

func (r RedisStrategy) GetValue(key string) (uint64, error) {
	var value uint64
	err := r.Client.Do(radix.Cmd(&value, "GET", key))
	if err != nil {
		return value, err
	}

	return value, nil
}

func (r RedisStrategy) SetValue(key string, value uint64, expirationSeconds uint64) error {

	err := r.Client.Do(radix.FlatCmd(nil, "SET", key, value))
	if err != nil {
		return err
	}

	err = r.Client.Do(radix.FlatCmd(nil, "EXPIRE", key, expirationSeconds))
	if err != nil {
		return err
	}

	return nil
}

func (r RedisStrategy) IncrementValue(key string, delta uint64) error {
	err := r.Client.Do(radix.FlatCmd(nil, "INCRBY", key, delta))
	if err != nil {
		return err
	}

	return nil
}

func (r RedisStrategy) SetExpire(key string, expirationSeconds uint64) error {
	err := r.Client.Do(radix.FlatCmd(nil, "EXPIRE", key, expirationSeconds))
	if err != nil {
		return err
	}

	return nil
}

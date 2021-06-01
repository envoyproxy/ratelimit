package strategy

import (
	"strconv"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/envoyproxy/ratelimit/src/storage/service"
)

type MemcachedStrategy struct {
	Client service.MemcachedClientInterface
}

func (m MemcachedStrategy) GetValue(key string) (uint64, error) {
	item, err := m.Client.Get(key)
	if err != nil {
		return 0, err
	}

	value, err := strconv.ParseUint(string(item.Value), 10, 32)
	if err != nil {
		return 0, err
	}

	return value, nil
}

func (m MemcachedStrategy) SetValue(key string, value uint64, expirationSeconds uint64) error {
	item := &memcache.Item{
		Key:        key,
		Value:      []byte(strconv.FormatUint(value, 10)),
		Expiration: int32(expirationSeconds),
	}

	err := m.Client.Set(item)
	if err != nil {
		return err
	}

	return nil
}

func (m MemcachedStrategy) IncrementValue(key string, delta uint64) error {
	_, err := m.Client.Increment(key, delta)
	if err != nil {
		return err
	}

	return nil
}

func (m MemcachedStrategy) SetExpire(key string, expirationSeconds uint64) error {
	return nil
}

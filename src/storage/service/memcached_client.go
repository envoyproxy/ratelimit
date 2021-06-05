package service

import (
	"github.com/bradfitz/gomemcache/memcache"
)

// Client interface for memcached
type MemcachedClientInterface interface {
	Get(key string) (*memcache.Item, error)
	Set(item *memcache.Item) error
	Increment(key string, delta uint64) (uint64, error)
}

type MemcachedClient struct {
	Client *memcache.Client
	Stats  MemcachedStats
}

func (m MemcachedClient) Get(key string) (*memcache.Item, error) {
	m.Stats.keysRequested.Inc()
	items, err := m.Client.Get(key)
	if err != nil {
		m.Stats.GetError.Inc()
	} else {
		m.Stats.keysFound.Inc()
		m.Stats.GetSuccess.Inc()
	}

	return items, err
}

func (m MemcachedClient) Set(item *memcache.Item) error {
	err := m.Client.Set(item)
	if err != nil {
		m.Stats.SetError.Inc()
	} else {
		m.Stats.SetSuccess.Inc()
	}

	return err
}

func (m MemcachedClient) Increment(key string, delta uint64) (uint64, error) {
	newValue, err := m.Client.Increment(key, delta)
	switch err {
	case memcache.ErrCacheMiss:
		m.Stats.IncrementMiss.Inc()
	case nil:
		m.Stats.IncrementSuccess.Inc()
	default:
		m.Stats.IncrementError.Inc()
	}

	return newValue, err
}

package service

import (
	"github.com/bradfitz/gomemcache/memcache"
)

type MemcachedClientInterface interface {
	Get(key string) (*memcache.Item, error)
	Set(item *memcache.Item) error
	Increment(key string, delta uint64) (uint64, error)
}

type MemcachedClient struct {
	Client *memcache.Client
}

func (m MemcachedClient) Get(key string) (*memcache.Item, error) {
	return m.Client.Get(key)
}

func (m MemcachedClient) Set(item *memcache.Item) error {
	return m.Client.Set(item)
}

func (m MemcachedClient) Increment(key string, delta uint64) (uint64, error) {
	return m.Client.Increment(key, delta)
}

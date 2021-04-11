package factory

import (
	"github.com/bradfitz/gomemcache/memcache"
	"github.com/envoyproxy/ratelimit/src/storage/service"
	"github.com/envoyproxy/ratelimit/src/storage/strategy"
)

func NewMemcached(servers []string) strategy.StorageStrategy {
	client := newMemcachedClient(servers)
	return strategy.MemcachedStrategy{
		Client: client,
	}
}

func newMemcachedClient(servers []string) service.MemcachedClientInterface {
	client := memcache.New(servers...)
	return &service.MemcachedClient{
		Client: client,
	}
}

package factory

import (
	"github.com/bradfitz/gomemcache/memcache"
	"github.com/envoyproxy/ratelimit/src/storage/service"
	"github.com/envoyproxy/ratelimit/src/storage/strategy"
	stats "github.com/lyft/gostats"
)

func NewMemcached(scope stats.Scope, servers []string) strategy.StorageStrategy {
	client := newMemcachedClient(scope, servers)
	return strategy.MemcachedStrategy{
		Client: client,
	}
}

func newMemcachedClient(scope stats.Scope, servers []string) service.MemcachedClientInterface {
	client := memcache.New(servers...)
	stats := service.NewMemcachedStats(scope)
	return &service.MemcachedClient{
		Client: client,
		Stats:  stats,
	}
}

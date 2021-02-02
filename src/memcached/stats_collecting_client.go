package memcached

import (
	"github.com/bradfitz/gomemcache/memcache"
	stats "github.com/lyft/gostats"
)

type statsCollectingClient struct {
	c Client

	multiGetSuccess  stats.Counter
	multiGetError    stats.Counter
	incrementSuccess stats.Counter
	incrementMiss    stats.Counter
	incrementError   stats.Counter
	addSuccess       stats.Counter
	addError         stats.Counter
	addNotStored     stats.Counter
	keysRequested    stats.Counter
	keysFound        stats.Counter
}

func CollectStats(c Client, scope stats.Scope) Client {
	return statsCollectingClient{
		c:                c,
		multiGetSuccess:  scope.NewCounterWithTags("multiget", map[string]string{"code": "success"}),
		multiGetError:    scope.NewCounterWithTags("multiget", map[string]string{"code": "error"}),
		incrementSuccess: scope.NewCounterWithTags("increment", map[string]string{"code": "success"}),
		incrementMiss:    scope.NewCounterWithTags("increment", map[string]string{"code": "miss"}),
		incrementError:   scope.NewCounterWithTags("increment", map[string]string{"code": "error"}),
		addSuccess:       scope.NewCounterWithTags("add", map[string]string{"code": "success"}),
		addError:         scope.NewCounterWithTags("add", map[string]string{"code": "error"}),
		addNotStored:     scope.NewCounterWithTags("add", map[string]string{"code": "not_stored"}),
		keysRequested:    scope.NewCounter("keys_requested"),
		keysFound:        scope.NewCounter("keys_found"),
	}
}

func (scc statsCollectingClient) GetMulti(keys []string) (map[string]*memcache.Item, error) {
	scc.keysRequested.Add(uint64(len(keys)))

	results, err := scc.c.GetMulti(keys)

	if err != nil {
		scc.multiGetError.Inc()
	} else {
		scc.keysFound.Add(uint64(len(results)))
		scc.multiGetSuccess.Inc()
	}

	return results, err
}

func (scc statsCollectingClient) Increment(key string, delta uint64) (newValue uint64, err error) {
	newValue, err = scc.c.Increment(key, delta)
	switch err {
	case memcache.ErrCacheMiss:
		scc.incrementMiss.Inc()
	case nil:
		scc.incrementSuccess.Inc()
	default:
		scc.incrementError.Inc()
	}
	return
}

func (scc statsCollectingClient) Add(item *memcache.Item) error {
	err := scc.c.Add(item)

	switch err {
	case memcache.ErrNotStored:
		scc.addNotStored.Inc()
	case nil:
		scc.addSuccess.Inc()
	default:
		scc.addError.Inc()
	}

	return err
}

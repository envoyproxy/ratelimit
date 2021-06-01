package service

import stats "github.com/lyft/gostats"

type MemcachedStats struct {
	GetSuccess       stats.Counter
	GetError         stats.Counter
	SetSuccess       stats.Counter
	SetError         stats.Counter
	IncrementSuccess stats.Counter
	IncrementMiss    stats.Counter
	IncrementError   stats.Counter
	keysRequested    stats.Counter
	keysFound        stats.Counter
}

func NewMemcachedStats(scope stats.Scope) MemcachedStats {
	return MemcachedStats{
		GetSuccess:       scope.NewCounterWithTags("get", map[string]string{"code": "success"}),
		GetError:         scope.NewCounterWithTags("get", map[string]string{"code": "error"}),
		SetSuccess:       scope.NewCounterWithTags("set", map[string]string{"code": "success"}),
		SetError:         scope.NewCounterWithTags("set", map[string]string{"code": "error"}),
		IncrementSuccess: scope.NewCounterWithTags("increment", map[string]string{"code": "success"}),
		IncrementMiss:    scope.NewCounterWithTags("increment", map[string]string{"code": "miss"}),
		IncrementError:   scope.NewCounterWithTags("increment", map[string]string{"code": "error"}),
		keysRequested:    scope.NewCounter("keys_requested"),
		keysFound:        scope.NewCounter("keys_found"),
	}
}

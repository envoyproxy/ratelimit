package stats

import (
	stat "github.com/envoyproxy/ratelimit/src/stats"
	stats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
)

type MockStatManager struct {
	scope stats.Scope
}

func (m *MockStatManager) NewShouldRateLimitStats() stat.ShouldRateLimitStats {
	return stat.ShouldRateLimitStats{}
}

func (m *MockStatManager) NewServiceStats() stat.ServiceStats {
	return stat.ServiceStats{}
}

func (m *MockStatManager) NewShouldRateLimitLegacyStats() stat.ShouldRateLimitLegacyStats {
	return stat.ShouldRateLimitLegacyStats{}
}

func (m *MockStatManager) NewStats(key string) stat.RateLimitStats {
	ret := stat.RateLimitStats{}
	logger.Debugf("outputing test stats %s", key)
	ret.Key = key
	ret.TotalHits = m.scope.NewCounter(key + ".detailed_total_hits")
	ret.OverLimit = m.scope.NewCounter(key + ".detailed_over_limit")
	ret.NearLimit = m.scope.NewCounter(key + ".detailed_near_limit")
	ret.OverLimitWithLocalCache = m.scope.NewCounter(key + ".detailed_over_limit_with_local_cache")
	return ret
}

func (m *MockStatManager) AddTotalHits(u uint64, rlStats stat.RateLimitStats, key string) {
	rlStats.TotalHits.Add(u)
}

func (this *MockStatManager) AddOverLimit(u uint64, rlStats stat.RateLimitStats, key string) {
	rlStats.OverLimit.Add(u)
}

func (this *MockStatManager) AddNearLimit(u uint64, rlStats stat.RateLimitStats, key string) {
	rlStats.NearLimit.Add(u)
}

func (this *MockStatManager) AddOverLimitWithLocalCache(u uint64, rlStats stat.RateLimitStats, key string) {
	rlStats.OverLimitWithLocalCache.Add(u)
}

func NewMockStatManager(store stats.Scope) stat.Manager {
	return &MockStatManager{scope: store}
}

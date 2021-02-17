package stats

import (
	stat "github.com/envoyproxy/ratelimit/src/stats"
	stats "github.com/lyft/gostats"
)

type MockStatManager struct {
	store stats.Store
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
	ret := m.NewStats(key)
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

func NewMockStatManager(store stats.Store) stat.Manager {
	return &MockStatManager{store: store}
}

package stats

import (
	stat "github.com/envoyproxy/ratelimit/src/stats"
	stats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
)

type MockStatManager struct {
	store stats.Store
}

func (m *MockStatManager) GetStatsStore() stats.Store {
	return m.store
}

func (m *MockStatManager) NewShouldRateLimitStats() stat.ShouldRateLimitStats {
	s := m.store.Scope("call.should_rate_limit")
	ret := stat.ShouldRateLimitStats{}
	ret.RedisError = s.NewCounter("redis_error")
	ret.ServiceError = s.NewCounter("service_error")
	return ret
}

func (m *MockStatManager) NewServiceStats() stat.ServiceStats {
	ret := stat.ServiceStats{}
	ret.ConfigLoadSuccess = m.store.NewCounter("config_load_success")
	ret.ConfigLoadError = m.store.NewCounter("config_load_error")
	ret.ShouldRateLimit = m.NewShouldRateLimitStats()
	return ret
}

func (m *MockStatManager) NewShouldRateLimitLegacyStats() stat.ShouldRateLimitLegacyStats {
	s := m.store.Scope("call.should_rate_limit_legacy")
	return stat.ShouldRateLimitLegacyStats{
		ReqConversionError:   s.NewCounter("req_conversion_error"),
		RespConversionError:  s.NewCounter("resp_conversion_error"),
		ShouldRateLimitError: s.NewCounter("should_rate_limit_error"),
	}
}

//todo: review mock implementation
func (m *MockStatManager) NewStats(key string) stat.RateLimitStats {
	ret := stat.RateLimitStats{}
	logger.Debugf("outputing test stats %s", key)
	ret.Key = key
	ret.TotalHits = m.store.NewCounter(key + ".total_hits")
	ret.OverLimit = m.store.NewCounter(key + ".over_limit")
	ret.NearLimit = m.store.NewCounter(key + ".near_limit")
	ret.OverLimitWithLocalCache = m.store.NewCounter(key + ".over_limit_with_local_cache")
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

func (this *MockStatManager) NewDetailedStats(key string) stat.RateLimitStats {
	return this.NewStats(key)
}

func NewMockStatManager(store stats.Store) stat.Manager {
	return &MockStatManager{store: store}
}

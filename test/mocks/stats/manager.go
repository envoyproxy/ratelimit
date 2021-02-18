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
	s := m.scope.Scope("call.should_rate_limit")
	ret := stat.ShouldRateLimitStats{}
	ret.RedisError = s.NewCounter("redis_error")
	ret.ServiceError = s.NewCounter("service_error")
	return ret
}

func (m *MockStatManager) NewServiceStats() stat.ServiceStats {
	ret := stat.ServiceStats{}
	ret.ConfigLoadSuccess = m.scope.NewCounter("config_load_success")
	ret.ConfigLoadError = m.scope.NewCounter("config_load_error")
	ret.ShouldRateLimit = m.NewShouldRateLimitStats()
	return ret
}

func (m *MockStatManager) NewShouldRateLimitLegacyStats() stat.ShouldRateLimitLegacyStats {
	s := m.scope.Scope("call.should_rate_limit_legacy")
	return stat.ShouldRateLimitLegacyStats{
		ReqConversionError:   s.NewCounter("req_conversion_error"),
		RespConversionError:  s.NewCounter("resp_conversion_error"),
		ShouldRateLimitError: s.NewCounter("should_rate_limit_error"),
	}
}
//todo: consider not using mock since pretty much all logic is repeated.
func (m *MockStatManager) NewStats(key string) stat.RateLimitStats {
	ret := stat.RateLimitStats{}
	logger.Debugf("outputing test stats %s", key)
	ret.Key = key
	ret.TotalHits = m.scope.NewCounter(key + ".total_hits")
	ret.OverLimit = m.scope.NewCounter(key + ".over_limit")
	ret.NearLimit = m.scope.NewCounter(key + ".near_limit")
	ret.OverLimitWithLocalCache = m.scope.NewCounter(key + ".over_limit_with_local_cache")
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

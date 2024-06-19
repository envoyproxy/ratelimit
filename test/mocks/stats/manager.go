package stats

import (
	gostats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"

	"github.com/envoyproxy/ratelimit/src/stats"
	"github.com/envoyproxy/ratelimit/src/utils"
)

type MockStatManager struct {
	store gostats.Store
}

func (m *MockStatManager) GetStatsStore() gostats.Store {
	return m.store
}

func (m *MockStatManager) NewShouldRateLimitStats() stats.ShouldRateLimitStats {
	s := m.store.Scope("call.should_rate_limit")
	ret := stats.ShouldRateLimitStats{}
	ret.RedisError = s.NewCounter("redis_error")
	ret.ServiceError = s.NewCounter("service_error")
	return ret
}

func (m *MockStatManager) NewServiceStats() stats.ServiceStats {
	ret := stats.ServiceStats{}
	ret.ConfigLoadSuccess = m.store.NewCounter("config_load_success")
	ret.ConfigLoadError = m.store.NewCounter("config_load_error")
	ret.ShouldRateLimit = m.NewShouldRateLimitStats()
	ret.GlobalShadowMode = m.store.NewCounter("global_shadow_mode")
	return ret
}

func (m *MockStatManager) NewStats(key string) stats.RateLimitStats {
	ret := stats.RateLimitStats{}
	logger.Debugf("outputing test gostats %s", key)
	ret.Key = key
	key = utils.SanitizeStatName(key)
	ret.TotalHits = m.store.NewCounter(key + ".total_hits")
	ret.OverLimit = m.store.NewCounter(key + ".over_limit")
	ret.NearLimit = m.store.NewCounter(key + ".near_limit")
	ret.OverLimitWithLocalCache = m.store.NewCounter(key + ".over_limit_with_local_cache")
	ret.WithinLimit = m.store.NewCounter(key + ".within_limit")
	ret.ShadowMode = m.store.NewCounter(key + ".shadow_mode")

	return ret
}

func (m *MockStatManager) NewDomainStats(key string) stats.DomainStats {
	ret := stats.DomainStats{}
	logger.Debugf("outputing test domain stats %s", key)
	ret.Key = key
	ret.NotFound = m.store.NewCounter(key + ".domain_not_found")

	return ret
}

func NewMockStatManager(store gostats.Store) stats.Manager {
	return &MockStatManager{store: store}
}

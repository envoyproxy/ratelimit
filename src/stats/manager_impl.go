package stats

import (
	"github.com/envoyproxy/ratelimit/src/settings"
	gostats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
)

func NewStatManager(store gostats.Store, settings settings.Settings) *ManagerImpl {
	serviceScope := store.ScopeWithTags("ratelimit", settings.ExtraTags).Scope("service")
	return &ManagerImpl{
		store:                store,
		rlStatsScope:         serviceScope.Scope("rate_limit"),
		legacyStatsScope:     serviceScope.Scope("call.should_rate_limit_legacy"),
		serviceStatsScope:    serviceScope,
		shouldRateLimitScope: serviceScope.Scope("call.should_rate_limit"),
	}
}

func (this *ManagerImpl) GetStatsStore() gostats.Store {
	return this.store
}

// Create new rate descriptor stats for a descriptor tuple.
// @param key supplies the fully resolved descriptor tuple.
// @return new stats.
func (this *ManagerImpl) NewStats(key string) RateLimitStats {
	ret := RateLimitStats{}
	logger.Debugf("Creating stats for key: '%s'", key)
	ret.Key = key
	ret.TotalHits = this.rlStatsScope.NewCounter(key + ".total_hits")
	ret.OverLimit = this.rlStatsScope.NewCounter(key + ".over_limit")
	ret.NearLimit = this.rlStatsScope.NewCounter(key + ".near_limit")
	ret.OverLimitWithLocalCache = this.rlStatsScope.NewCounter(key + ".over_limit_with_local_cache")
	ret.WithinLimit = this.rlStatsScope.NewCounter(key + ".within_limit")
	return ret
}

func (this *ManagerImpl) NewShouldRateLimitLegacyStats() ShouldRateLimitLegacyStats {
	return ShouldRateLimitLegacyStats{
		ReqConversionError:   this.legacyStatsScope.NewCounter("req_conversion_error"),
		RespConversionError:  this.legacyStatsScope.NewCounter("resp_conversion_error"),
		ShouldRateLimitError: this.legacyStatsScope.NewCounter("should_rate_limit_error"),
	}
}

func (this *ManagerImpl) NewShouldRateLimitStats() ShouldRateLimitStats {
	ret := ShouldRateLimitStats{}
	ret.RedisError = this.shouldRateLimitScope.NewCounter("redis_error")
	ret.ServiceError = this.shouldRateLimitScope.NewCounter("service_error")
	return ret
}

func (this *ManagerImpl) NewServiceStats() ServiceStats {
	ret := ServiceStats{}
	ret.ConfigLoadSuccess = this.serviceStatsScope.NewCounter("config_load_success")
	ret.ConfigLoadError = this.serviceStatsScope.NewCounter("config_load_error")
	ret.ShouldRateLimit = this.NewShouldRateLimitStats()
	return ret
}

func (this RateLimitStats) GetKey() string {
	return this.Key
}

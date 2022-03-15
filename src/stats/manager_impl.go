package stats

import (
	gostats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"

	"github.com/envoyproxy/ratelimit/src/settings"
)

func NewStatManager(store gostats.Store, settings settings.Settings) *ManagerImpl {
	serviceScope := store.ScopeWithTags("ratelimit", settings.ExtraTags).Scope("service")
	return &ManagerImpl{
		store:                store,
		rlStatsScope:         serviceScope.Scope("rate_limit"),
		legacyStatsScope:     serviceScope.Scope("call.should_rate_limit_legacy"),
		serviceStatsScope:    serviceScope,
		detailedMetricsScope: serviceScope.Scope("rate_limit").Scope("detailed"),
		detailed:             settings.DetailedMetrics,
	}
}

func (this *ManagerImpl) GetStatsStore() gostats.Store {
	return this.store
}

func (this *ManagerImpl) AddTotalHits(u uint64, rlStats RateLimitStats, key string) {
	rlStats.TotalHits.Add(u)
	if this.detailed {
		stat := this.getDescriptorStat(key)
		stat.TotalHits.Add(u)
	}
}

func (this *ManagerImpl) AddOverLimit(u uint64, rlStats RateLimitStats, key string) {
	rlStats.OverLimit.Add(u)
	if this.detailed {
		stat := this.getDescriptorStat(key)
		stat.OverLimit.Add(u)
	}
}

func (this *ManagerImpl) AddNearLimit(u uint64, rlStats RateLimitStats, key string) {
	rlStats.NearLimit.Add(u)
	if this.detailed {
		stat := this.getDescriptorStat(key)
		stat.NearLimit.Add(u)
	}
}

func (this *ManagerImpl) AddOverLimitWithLocalCache(u uint64, rlStats RateLimitStats, key string) {
	rlStats.OverLimitWithLocalCache.Add(u)
	if this.detailed {
		stat := this.getDescriptorStat(key)
		stat.OverLimitWithLocalCache.Add(u)
	}
}

func (this *ManagerImpl) AddWithinLimit(u uint64, rlStats RateLimitStats, key string) {
	rlStats.WithinLimit.Add(u)
	if this.detailed {
		stat := this.getDescriptorStat(key)
		stat.WithinLimit.Add(u)
	}
}

// todo: consider adding a RateLimitStats cache
// todo: consider adding descriptor fields parameter to allow configuration of descriptor entries for which metrics will be emited.
func (this *ManagerImpl) getDescriptorStat(key string) RateLimitStats {
	ret := this.NewDetailedStats(key)
	return ret
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
	ret.ShadowMode = this.rlStatsScope.NewCounter(key + ".shadow_mode")
	return ret
}

func (this *ManagerImpl) NewDetailedStats(key string) RateLimitStats {
	ret := RateLimitStats{}
	logger.Debugf("Creating detailed stats for key: '%s'", key)
	ret.Key = key
	ret.TotalHits = this.detailedMetricsScope.NewCounter(key + ".total_hits")
	ret.OverLimit = this.detailedMetricsScope.NewCounter(key + ".over_limit")
	ret.NearLimit = this.detailedMetricsScope.NewCounter(key + ".near_limit")
	ret.OverLimitWithLocalCache = this.detailedMetricsScope.NewCounter(key + ".over_limit_with_local_cache")
	ret.WithinLimit = this.detailedMetricsScope.NewCounter(key + ".within_limit")
	ret.ShadowMode = this.detailedMetricsScope.NewCounter(key + ".shadow_mode")
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
	s := this.serviceStatsScope.Scope("call.should_rate_limit")
	ret := ShouldRateLimitStats{}
	ret.RedisError = s.NewCounter("redis_error")
	ret.ServiceError = s.NewCounter("service_error")
	return ret
}

func (this *ManagerImpl) NewServiceStats() ServiceStats {
	ret := ServiceStats{}
	ret.ConfigLoadSuccess = this.serviceStatsScope.NewCounter("config_load_success")
	ret.ConfigLoadError = this.serviceStatsScope.NewCounter("config_load_error")
	ret.ShouldRateLimit = this.NewShouldRateLimitStats()
	ret.GlobalShadowMode = this.serviceStatsScope.NewCounter("global_shadow_mode")
	return ret
}

func (this RateLimitStats) GetKey() string {
	return this.Key
}

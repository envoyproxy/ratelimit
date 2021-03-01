package stats

import (
	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/settings"
	gostats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
)

func NewStatManager(store gostats.Store, s settings.Settings) *ManagerImpl {
	logger.Infof("Initializing Stat Manager with detailed metrics %t", s.DetailedMetrics)
	serviceScope := store.ScopeWithTags("ratelimit", s.ExtraTags).Scope("service")
	return &ManagerImpl{
		store:                store,
		rlStatsScope:         serviceScope.Scope("rate_limit"),
		legacyStatsScope:     serviceScope.Scope("call.should_rate_limit_legacy"),
		serviceStatsScope:    serviceScope,
		detailedMetricsScope: serviceScope.Scope("rate_limit").Scope("detailed"),
		detailed:             s.DetailedMetrics,
	}
}

type ManagerImpl struct {
	store                gostats.Store
	rlStatsScope         gostats.Scope
	legacyStatsScope     gostats.Scope
	serviceStatsScope    gostats.Scope
	detailedMetricsScope gostats.Scope
	detailed             bool
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

//todo: consider adding a RateLimitStats cache
//todo: consider adding descriptor fields parameter to allow configuration of descriptor entries for which metrics will be emited.
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
	return ret

}

type ShouldRateLimitLegacyStats struct {
	ReqConversionError   gostats.Counter
	RespConversionError  gostats.Counter
	ShouldRateLimitError gostats.Counter
}

func (this *ManagerImpl) NewShouldRateLimitLegacyStats() ShouldRateLimitLegacyStats {
	return ShouldRateLimitLegacyStats{
		ReqConversionError:   this.legacyStatsScope.NewCounter("req_conversion_error"),
		RespConversionError:  this.legacyStatsScope.NewCounter("resp_conversion_error"),
		ShouldRateLimitError: this.legacyStatsScope.NewCounter("should_rate_limit_error"),
	}
}

type ShouldRateLimitStats struct {
	RedisError   gostats.Counter
	ServiceError gostats.Counter
}

func (this *ManagerImpl) NewShouldRateLimitStats() ShouldRateLimitStats {
	s := this.serviceStatsScope.Scope("call.should_rate_limit")
	ret := ShouldRateLimitStats{}
	ret.RedisError = s.NewCounter("redis_error")
	ret.ServiceError = s.NewCounter("service_error")
	return ret
}

type ServiceStats struct {
	ConfigLoadSuccess gostats.Counter
	ConfigLoadError   gostats.Counter
	ShouldRateLimit   ShouldRateLimitStats
}

func (this *ManagerImpl) NewServiceStats() ServiceStats {
	ret := ServiceStats{}
	ret.ConfigLoadSuccess = this.serviceStatsScope.NewCounter("config_load_success")
	ret.ConfigLoadError = this.serviceStatsScope.NewCounter("config_load_error")
	ret.ShouldRateLimit = this.NewShouldRateLimitStats()
	return ret
}

func DescriptorKey(domain string, descriptor *pb_struct.RateLimitDescriptor) string {
	rateLimitKey := ""
	for _, entry := range descriptor.Entries {
		if rateLimitKey != "" {
			rateLimitKey += "."
		}
		rateLimitKey += entry.Key
		if entry.Value != "" {
			rateLimitKey += "_" + entry.Value
		}
	}
	return domain + "." + rateLimitKey
}

// Stats for an individual rate limit config entry.
//todo: Ideally the gostats package fields should be unexported
//	the inner value could be interacted with via getters such as rlStats.TotalHits() uint64
//	This ensures that setters such as Inc() and Add() can only be managed by ManagerImpl.
type RateLimitStats struct {
	Key                     string
	TotalHits               gostats.Counter
	OverLimit               gostats.Counter
	NearLimit               gostats.Counter
	OverLimitWithLocalCache gostats.Counter
}

func (this RateLimitStats) String() string {
	return this.Key
}

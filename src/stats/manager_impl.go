package stats

import (
	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	gostats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
)

func NewStatManager(scope gostats.Scope, detailedMetrics bool) *ManagerImpl {
	return &ManagerImpl{
		scope:    scope,
		detailed: detailedMetrics,
	}
}

type ManagerImpl struct {
	scope    gostats.Scope
	detailed bool
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

//todo: consider adding a ratelimitstats cache
//todo: add descriptor fields parameter to allow configuration of descriptor entries for which metrics will be emited.
func (this *ManagerImpl) getDescriptorStat(key string) RateLimitStats {
	ret := this.NewStats(key)
	return ret
}

// Create new rate descriptor stats for a descriptor tuple.
// @param statsScope supplies the owning scope.
// @param key supplies the fully resolved descriptor tuple.
// @return new stats.
func (this *ManagerImpl) NewStats(key string) RateLimitStats {
	ret := RateLimitStats{}
	logger.Debugf("outputing test stats %s", key)
	ret.Key = key
	ret.TotalHits = this.scope.NewCounter(key + ".total_hits")
	ret.OverLimit = this.scope.NewCounter(key + ".over_limit")
	ret.NearLimit = this.scope.NewCounter(key + ".near_limit")
	ret.OverLimitWithLocalCache = this.scope.NewCounter(key + ".over_limit_with_local_cache")
	return ret
}


type ShouldRateLimitLegacyStats struct {
	ReqConversionError   gostats.Counter
	RespConversionError  gostats.Counter
	ShouldRateLimitError gostats.Counter
}


func (this *ManagerImpl) NewShouldRateLimitLegacyStats() ShouldRateLimitLegacyStats {
	s := this.scope.Scope("call.should_rate_limit_legacy")
	return ShouldRateLimitLegacyStats{
		ReqConversionError:   s.NewCounter("req_conversion_error"),
		RespConversionError:  s.NewCounter("resp_conversion_error"),
		ShouldRateLimitError: s.NewCounter("should_rate_limit_error"),
	}
}

type ShouldRateLimitStats struct {
	RedisError   gostats.Counter
	ServiceError gostats.Counter
}

func (this *ManagerImpl) NewShouldRateLimitStats() ShouldRateLimitStats {
	s := this.scope.Scope("call.should_rate_limit")
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

func (this *ManagerImpl)  NewServiceStats() ServiceStats {
	ret := ServiceStats{}
	ret.ConfigLoadSuccess = this.scope.NewCounter("config_load_success")
	ret.ConfigLoadError = this.scope.NewCounter("config_load_error")
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
//todo: unexport fields
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
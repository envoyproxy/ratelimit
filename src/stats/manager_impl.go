package stats

import (
	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	st "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
)

func NewStatManager(scope st.Scope, detailedMetrics bool) *ManagerImpl {
	return &ManagerImpl{
		scope:    scope,
		detailed: detailedMetrics,
	}
}

type ManagerImpl struct {
	scope    st.Scope
	detailed bool
}

func (this *ManagerImpl) AddTotalHits(u uint64, rlStats RateLimitStats, domain string, descriptor *pb_struct.RateLimitDescriptor) {
	rlStats.TotalHits.Add(u)
	if this.detailed {
		stat := this.getDescriptorStat(domain, descriptor)
		stat.OverLimit.Add(u)
	}
}

func (this *ManagerImpl) AddOverLimit(u uint64, rlStats RateLimitStats, domain string, descriptor *pb_struct.RateLimitDescriptor) {
	rlStats.OverLimit.Add(u)
	if this.detailed {
		stat := this.getDescriptorStat(domain, descriptor)
		stat.OverLimit.Add(u)
	}
}

func (this *ManagerImpl) AddNearLimit(u uint64, rlStats RateLimitStats, domain string, descriptor *pb_struct.RateLimitDescriptor) {
	rlStats.NearLimit.Add(u)
	if this.detailed {
		stat := this.getDescriptorStat(domain, descriptor)
		stat.OverLimit.Add(u)
	}
}

func (this *ManagerImpl) AddOverLimitWithLocalCache(u uint64, rlStats RateLimitStats, domain string, descriptor *pb_struct.RateLimitDescriptor) {
	rlStats.OverLimitWithLocalCache.Add(u)
	if this.detailed {
		stat := this.getDescriptorStat(domain, descriptor)
		stat.OverLimit.Add(u)
	}
}

//todo: consider adding a ratelimitstats cache
//todo: add descriptor fields parameter to allow configuration of descriptor entries for which metrics will be emited.
func (this *ManagerImpl) getDescriptorStat(domain string, descriptor *pb_struct.RateLimitDescriptor) RateLimitStats {
	key := DescriptorKey(domain, descriptor)
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
	ret.TotalHits = this.scope.NewCounter(key + ".detailed_total_hits")
	ret.OverLimit = this.scope.NewCounter(key + ".detailed_over_limit")
	ret.NearLimit = this.scope.NewCounter(key + ".detailed_near_limit")
	ret.OverLimitWithLocalCache = this.scope.NewCounter(key + ".detailed_over_limit_with_local_cache")
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
	Key						string
	TotalHits               st.Counter
	OverLimit               st.Counter
	NearLimit               st.Counter
	OverLimitWithLocalCache st.Counter
}
func (this RateLimitStats) String() string {
	return this.Key
}
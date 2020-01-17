package redis

import (
	"github.com/coocood/freecache"
	stats "github.com/lyft/gostats"
)

type localCacheStats struct {
	cache             *freecache.Cache
	evacuateCount     stats.Gauge
	expiredCount      stats.Gauge
	entryCount        stats.Gauge
	averageAccessTime stats.Gauge
	hitCount          stats.Gauge
	missCount         stats.Gauge
	lookupCount       stats.Gauge
	overwriteCount    stats.Gauge
}

func NewLocalCacheStats(localCache *freecache.Cache, scope stats.Scope) stats.StatGenerator {
	return localCacheStats{
		cache:             localCache,
		evacuateCount:     scope.NewGauge("evacuateCount"),
		expiredCount:      scope.NewGauge("expiredCount"),
		entryCount:        scope.NewGauge("entryCount"),
		averageAccessTime: scope.NewGauge("averageAccessTime"),
		hitCount:          scope.NewGauge("hitCount"),
		missCount:         scope.NewGauge("missCount"),
		lookupCount:       scope.NewGauge("lookupCount"),
		overwriteCount:    scope.NewGauge("overwriteCount"),
	}
}

func (stats localCacheStats) GenerateStats() {
	stats.evacuateCount.Set(uint64(stats.cache.EvacuateCount()))
	stats.expiredCount.Set(uint64(stats.cache.ExpiredCount()))
	stats.entryCount.Set(uint64(stats.cache.EntryCount()))
	stats.averageAccessTime.Set(uint64(stats.cache.AverageAccessTime()))
	stats.hitCount.Set(uint64(stats.cache.HitCount()))
	stats.missCount.Set(uint64(stats.cache.MissCount()))
	stats.lookupCount.Set(uint64(stats.cache.LookupCount()))
	stats.overwriteCount.Set(uint64(stats.cache.OverwriteCount()))
}

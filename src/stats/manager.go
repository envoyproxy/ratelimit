package stats

import stats "github.com/lyft/gostats"
import gostats "github.com/lyft/gostats"

type Manager interface {
	NewStats(key string) RateLimitStats
	NewShouldRateLimitStats() ShouldRateLimitStats
	NewServiceStats() ServiceStats
	NewShouldRateLimitLegacyStats() ShouldRateLimitLegacyStats
	GetStatsStore() stats.Store
}

type ManagerImpl struct {
	store                gostats.Store
	rlStatsScope         gostats.Scope
	legacyStatsScope     gostats.Scope
	serviceStatsScope    gostats.Scope
	shouldRateLimitScope gostats.Scope
}

type ShouldRateLimitStats struct {
	RedisError   gostats.Counter
	ServiceError gostats.Counter
}

type ServiceStats struct {
	ConfigLoadSuccess gostats.Counter
	ConfigLoadError   gostats.Counter
	ShouldRateLimit   ShouldRateLimitStats
}

type ShouldRateLimitLegacyStats struct {
	ReqConversionError   gostats.Counter
	RespConversionError  gostats.Counter
	ShouldRateLimitError gostats.Counter
}

// Stats for an individual rate limit config entry.
//todo: Ideally the gostats package fields should be unexported
//	the inner value could be interacted with via getters such as rlStats.TotalHits() uint64
//	This ensures that setters such as Inc() and Add() can only be managed by RateLimitStats.
type RateLimitStats struct {
	Key                     string
	TotalHits               gostats.Counter
	OverLimit               gostats.Counter
	NearLimit               gostats.Counter
	OverLimitWithLocalCache gostats.Counter
	WithinLimit             gostats.Counter
}
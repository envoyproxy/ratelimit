package stats

import stats "github.com/lyft/gostats"
import gostats "github.com/lyft/gostats"

// Manager is the interface that wraps initialization of stat structures.
type Manager interface {
	// NewStats provides a RateLimitStats structure associated with a given descriptorKey.
	// Multiple calls with the same descriptorKey argument are guaranteed to be equivalent.
	NewStats(descriptorKey string) RateLimitStats
	// Initializes a ShouldRateLimitStats structure.
	// Multiple calls to this method are idempotent.
	NewShouldRateLimitStats() ShouldRateLimitStats
	// Initializes a ServiceStats structure.
	// Multiple calls to this method are idempotent.
	NewServiceStats() ServiceStats
	// Initializes a ShouldRateLimitLegacyStats structure.
	// Multiple calls to this method are idempotent.
	NewShouldRateLimitLegacyStats() ShouldRateLimitLegacyStats
	// Returns the stats.Store wrapped by the Manager.
	GetStatsStore() stats.Store
}

type ManagerImpl struct {
	store                gostats.Store
	rlStatsScope         gostats.Scope
	legacyStatsScope     gostats.Scope
	serviceStatsScope    gostats.Scope
	shouldRateLimitScope gostats.Scope
}

// Stats for panic recoveries.
// Identifies if a recovered panic is a redis.RedisError or a ServiceError.
type ShouldRateLimitStats struct {
	RedisError   gostats.Counter
	ServiceError gostats.Counter
}

// Stats for server errors.
// Keeps failure and success metrics.
type ServiceStats struct {
	ConfigLoadSuccess gostats.Counter
	ConfigLoadError   gostats.Counter
	ShouldRateLimit   ShouldRateLimitStats
}

// Legacy Stats for ratelimit errors.
type ShouldRateLimitLegacyStats struct {
	ReqConversionError   gostats.Counter
	RespConversionError  gostats.Counter
	ShouldRateLimitError gostats.Counter
}

// Stats for an individual rate limit config entry.
type RateLimitStats struct {
	Key                     string
	TotalHits               gostats.Counter
	OverLimit               gostats.Counter
	NearLimit               gostats.Counter
	OverLimitWithLocalCache gostats.Counter
	WithinLimit             gostats.Counter
}

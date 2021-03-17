package stats

import stats "github.com/lyft/gostats"

type Manager interface {
	AddTotalHits(u uint64, rlStats RateLimitStats)
	AddOverLimit(u uint64, rlStats RateLimitStats)
	AddNearLimit(u uint64, rlStats RateLimitStats)
	AddOverLimitWithLocalCache(u uint64, rlStats RateLimitStats)
	AddWithinLimit(u uint64, rlStats RateLimitStats)
	NewStats(key string) RateLimitStats
	NewShouldRateLimitStats() ShouldRateLimitStats
	NewServiceStats() ServiceStats
	NewShouldRateLimitLegacyStats() ShouldRateLimitLegacyStats
	GetStatsStore() stats.Store
}

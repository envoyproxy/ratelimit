package stats

import stats "github.com/lyft/gostats"

type Manager interface {
	AddTotalHits(u uint64, rlStats RateLimitStats, key string)
	AddOverLimit(u uint64, rlStats RateLimitStats, key string)
	AddNearLimit(u uint64, rlStats RateLimitStats, key string)
	AddOverLimitWithLocalCache(u uint64, rlStats RateLimitStats, key string)
	NewStats(key string) RateLimitStats
	NewShouldRateLimitStats() ShouldRateLimitStats
	NewServiceStats() ServiceStats
	NewShouldRateLimitLegacyStats() ShouldRateLimitLegacyStats
	GetStatsStore() stats.Store
	NewDetailedStats(key string) RateLimitStats
}

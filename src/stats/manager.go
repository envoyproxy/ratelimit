package stats

import stats "github.com/lyft/gostats"

type Manager interface {
	NewStats(key string) RateLimitStats
	NewShouldRateLimitStats() ShouldRateLimitStats
	NewServiceStats() ServiceStats
	NewShouldRateLimitLegacyStats() ShouldRateLimitLegacyStats
	GetStatsStore() stats.Store
}

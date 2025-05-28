package test_stats

import (
	"fmt"
	"testing"

	gostats "github.com/lyft/gostats"
	gostatsMock "github.com/lyft/gostats/mock"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
)

func TestEscapingInvalidChartersInMetricName(t *testing.T) {
	mockSink := gostatsMock.NewSink()
	statsStore := gostats.NewStore(mockSink, false)
	statsManager := stats.NewStatManager(statsStore, settings.Settings{})

	tests := []struct {
		name string
		key  string
		want string
	}{
		{
			name: "use not modified key if it does not contain special characters",
			key:  "path_/foo/bar",
			want: "path_/foo/bar",
		},
		{
			name: "escape colon",
			key:  "path_/foo:*:bar",
			want: "path_/foo_*_bar",
		},
		{
			name: "escape pipe",
			key:  "path_/foo|bar|baz",
			want: "path_/foo_bar_baz",
		},
		{
			name: "escape all special characters",
			key:  "path_/foo:bar|baz",
			want: "path_/foo_bar_baz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := statsManager.NewStats(tt.key)
			assert.Equal(t, tt.key, stats.Key)

			stats.TotalHits.Inc()
			statsManager.GetStatsStore().Flush()
			mockSink.AssertCounterExists(t, fmt.Sprintf("ratelimit.service.rate_limit.%s.total_hits", tt.want))
		})
	}
}

func TestPerKeyStats(t *testing.T) {
	t.Run("WithPerKeyStatsEnabled", func(t *testing.T) {
		mockSink := gostatsMock.NewSink()
		statsStore := gostats.NewStore(mockSink, false)
		statsManager := stats.NewStatManager(statsStore, settings.Settings{EnablePerKeyStats: true})

		// Create stats for different keys
		key1Stats := statsManager.NewStats("domain1.key1")
		key2Stats := statsManager.NewStats("domain1.key2")

		// Increment counters
		key1Stats.TotalHits.Inc()
		key2Stats.TotalHits.Inc()

		// Flush stats
		statsManager.GetStatsStore().Flush()

		// Each key should have its own counter
		mockSink.AssertCounterExists(t, "ratelimit.service.rate_limit.domain1.key1.total_hits")
		mockSink.AssertCounterExists(t, "ratelimit.service.rate_limit.domain1.key2.total_hits")
	})

	t.Run("WithPerKeyStatsDisabled", func(t *testing.T) {
		mockSink := gostatsMock.NewSink()
		statsStore := gostats.NewStore(mockSink, false)
		statsManager := stats.NewStatManager(statsStore, settings.Settings{EnablePerKeyStats: false})

		// Create stats for different keys
		key1Stats := statsManager.NewStats("domain1.key1")
		key2Stats := statsManager.NewStats("domain1.key2")

		// Increment counters
		key1Stats.TotalHits.Inc()
		key2Stats.TotalHits.Inc()

		// Flush stats
		statsManager.GetStatsStore().Flush()

		// All stats should be aggregated under "all"
		mockSink.AssertCounterEquals(t, "ratelimit.service.rate_limit.all.total_hits", 2)
		
		// Original keys should not exist
		mockSink.AssertCounterNotExists(t, "ratelimit.service.rate_limit.domain1.key1.total_hits")
		mockSink.AssertCounterNotExists(t, "ratelimit.service.rate_limit.domain1.key2.total_hits")
	})
}

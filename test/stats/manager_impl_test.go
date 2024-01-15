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

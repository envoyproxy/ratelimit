package prometheusstats

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

var (
	s = NewPrometheusSink()
)

func TestFlushCounter(t *testing.T) {
	s.FlushCounter("ratelimit.service.call.should_rate_limit.service_error", 1)
	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	metrics := make(map[string]*dto.MetricFamily)
	for _, metricFamily := range metricFamilies {
		metrics[*metricFamily.Name] = metricFamily
	}

	m, ok := metrics["ratelimit_service_should_rate_limit_error"]
	require.True(t, ok)
	require.Len(t, m.Metric, 1)
	require.Equal(t, 1.0, *m.Metric[0].Counter.Value)
}

func TestFlushGauge(t *testing.T) {
	s.FlushGauge("ratelimit.service.rate_limit.domain1.key1.test_gauge", 1)
	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	metrics := make(map[string]*dto.MetricFamily)
	for _, metricFamily := range metricFamilies {
		metrics[*metricFamily.Name] = metricFamily
	}

	_, ok := metrics["ratelimit_service_rate_limit_test_gauge"]
	require.False(t, ok)
}

func TestFlushTimer(t *testing.T) {
	s.FlushTimer("ratelimit.service.rate_limit.mongo_cps.database_users.total_hits", 1)
	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	metrics := make(map[string]*dto.MetricFamily)
	for _, metricFamily := range metricFamilies {
		metrics[*metricFamily.Name] = metricFamily
	}

	m, ok := metrics["ratelimit_service_rate_limit_total_hits"]
	require.True(t, ok)
	require.Len(t, m.Metric, 1)
	require.Equal(t, uint64(1), *m.Metric[0].Histogram.SampleCount)
	require.Equal(t, 1.0, *m.Metric[0].Histogram.SampleSum)
}

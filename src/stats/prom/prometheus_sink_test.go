package prom

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var s = NewPrometheusSink()

func TestFlushCounter(t *testing.T) {
	s.FlushCounter("ratelimit_server.ShouldRateLimit.total_requests", 1)
	assert.Eventually(t, func() bool {
		metricFamilies, err := prometheus.DefaultGatherer.Gather()
		require.NoError(t, err)

		metrics := make(map[string]*dto.MetricFamily)
		for _, metricFamily := range metricFamilies {
			metrics[*metricFamily.Name] = metricFamily
		}

		m, ok := metrics["ratelimit_service_total_requests"]
		require.True(t, ok)
		require.Len(t, m.Metric, 1)
		require.Equal(t, map[string]string{
			"grpc_method": "ShouldRateLimit",
		}, toMap(m.Metric[0].Label))
		require.Equal(t, 1.0, *m.Metric[0].Counter.Value)
		return true
	}, time.Second, time.Millisecond)
}

func toMap(labels []*dto.LabelPair) map[string]string {
	m := make(map[string]string)
	for _, l := range labels {
		m[*l.Name] = *l.Value
	}
	return m
}

func TestFlushCounterWithDifferentLabels(t *testing.T) {
	s.FlushCounter("ratelimit.service.rate_limit.domain1.key1_val1.over_limit", 1)
	s.FlushCounter("ratelimit.service.rate_limit.domain1.key1_val1.key2_val2.over_limit", 2)
	s.FlushCounter("ratelimit.service.rate_limit.domain1.key3_val3.key4_val4.over_limit", 1)
	s.FlushCounter("ratelimit.service.rate_limit.domain1.key3_val3.key4_val4.key5_val5.over_limit", 2)
	assert.Eventually(t, func() bool {
		metricFamilies, err := prometheus.DefaultGatherer.Gather()
		require.NoError(t, err)

		metrics := make(map[string]*dto.MetricFamily)
		for _, metricFamily := range metricFamilies {
			metrics[*metricFamily.Name] = metricFamily
		}

		m, ok := metrics["ratelimit_service_rate_limit_over_limit"]
		require.True(t, ok)
		require.Len(t, m.Metric, 3)
		require.Equal(t, 1.0, *m.Metric[0].Counter.Value)
		require.Equal(t, map[string]string{
			"domain": "domain1",
			"key1":   "key1_val1",
		}, toMap(m.Metric[0].Label))
		require.Equal(t, 2.0, *m.Metric[1].Counter.Value)
		require.Equal(t, map[string]string{
			"domain": "domain1",
			"key1":   "key1_val1",
			"key2":   "key2_val2",
		}, toMap(m.Metric[1].Label))
		require.Equal(t, 3.0, *m.Metric[2].Counter.Value)
		require.Equal(t, map[string]string{
			"domain": "domain1",
			"key1":   "key3_val3",
			"key2":   "key4_val4",
		}, toMap(m.Metric[2].Label))
		return true
	}, time.Second, time.Millisecond)
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
	assert.Eventually(t, func() bool {
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
		require.Equal(t, map[string]string{
			"domain": "mongo_cps",
			"key1":   "database_users",
		}, toMap(m.Metric[0].Label))
		require.Equal(t, 1.0, *m.Metric[0].Histogram.SampleSum)
		return true
	}, time.Second, time.Millisecond)
}

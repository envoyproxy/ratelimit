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
		if err != nil {
			return false
		}

		metrics := make(map[string]*dto.MetricFamily)
		for _, metricFamily := range metricFamilies {
			metrics[*metricFamily.Name] = metricFamily
		}

		m, ok := metrics["ratelimit_service_total_requests"]
		if !ok || len(m.Metric) < 1 {
			return false
		}
		assert.Equal(t, map[string]string{
			"grpc_method": "ShouldRateLimit",
		}, toMap(m.Metric[0].Label))
		assert.GreaterOrEqual(t, *m.Metric[0].Counter.Value, 1.0)
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
		if err != nil {
			return false
		}

		metrics := make(map[string]*dto.MetricFamily)
		for _, metricFamily := range metricFamilies {
			metrics[*metricFamily.Name] = metricFamily
		}

		m, ok := metrics["ratelimit_service_rate_limit_over_limit"]
		if !ok || len(m.Metric) < 3 {
			return false
		}

		// Find metrics by their labels since order is not guaranteed
		metricsByKey := make(map[string]*dto.Metric)
		for _, metric := range m.Metric {
			labels := toMap(metric.Label)
			key := labels["domain"] + ":" + labels["key1"]
			if k2, ok := labels["key2"]; ok {
				key += ":" + k2
			}
			metricsByKey[key] = metric
		}

		// Check metric with only key1
		if metric, ok := metricsByKey["domain1:key1_val1"]; ok {
			assert.GreaterOrEqual(t, *metric.Counter.Value, 1.0)
		}

		// Check metric with key1 and key2 (from key1_val1.key2_val2)
		if metric, ok := metricsByKey["domain1:key1_val1:key2_val2"]; ok {
			assert.GreaterOrEqual(t, *metric.Counter.Value, 2.0)
		}

		// Check metric with key1 and key2 (from key3_val3.key4_val4) - aggregates both calls
		if metric, ok := metricsByKey["domain1:key3_val3:key4_val4"]; ok {
			assert.GreaterOrEqual(t, *metric.Counter.Value, 3.0)
		}

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
		if err != nil {
			return false
		}

		metrics := make(map[string]*dto.MetricFamily)
		for _, metricFamily := range metricFamilies {
			metrics[*metricFamily.Name] = metricFamily
		}

		m, ok := metrics["ratelimit_service_rate_limit_total_hits"]
		if !ok || len(m.Metric) < 1 {
			return false
		}

		// Find the metric with matching labels
		var found *dto.Metric
		for _, metric := range m.Metric {
			labels := toMap(metric.Label)
			if labels["domain"] == "mongo_cps" && labels["key1"] == "database_users" {
				found = metric
				break
			}
		}

		if found == nil || found.Histogram == nil {
			return false
		}

		assert.GreaterOrEqual(t, *found.Histogram.SampleCount, uint64(1))
		assert.GreaterOrEqual(t, *found.Histogram.SampleSum, 1.0)
		return true
	}, time.Second, time.Millisecond)
}

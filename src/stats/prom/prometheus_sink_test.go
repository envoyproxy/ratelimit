package prom

import (
	"reflect"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
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
		if !ok || len(m.Metric) != 1 {
			return false
		}
		return toMap(m.Metric[0].Label)["grpc_method"] == "ShouldRateLimit" &&
			*m.Metric[0].Counter.Value == 1.0
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
		if !ok || len(m.Metric) != 3 {
			return false
		}
		return *m.Metric[0].Counter.Value == 1.0 &&
			reflect.DeepEqual(toMap(m.Metric[0].Label), map[string]string{
				"domain": "domain1",
				"key1":   "key1_val1",
			}) &&
			*m.Metric[1].Counter.Value == 2.0 &&
			reflect.DeepEqual(toMap(m.Metric[1].Label), map[string]string{
				"domain": "domain1",
				"key1":   "key1_val1",
				"key2":   "key2_val2",
			}) &&
			*m.Metric[2].Counter.Value == 3.0 &&
			reflect.DeepEqual(toMap(m.Metric[2].Label), map[string]string{
				"domain": "domain1",
				"key1":   "key3_val3",
				"key2":   "key4_val4",
			})
	}, time.Second, time.Millisecond)
}

func TestFlushGauge(t *testing.T) {
	s.FlushGauge("ratelimit.service.rate_limit.domain1.key1.test_gauge", 1)
	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	assert.NoError(t, err)

	metrics := make(map[string]*dto.MetricFamily)
	for _, metricFamily := range metricFamilies {
		metrics[*metricFamily.Name] = metricFamily
	}

	_, ok := metrics["ratelimit_service_rate_limit_test_gauge"]
	assert.False(t, ok)
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
		if !ok || len(m.Metric) != 1 {
			return false
		}
		return *m.Metric[0].Histogram.SampleCount == uint64(1) &&
			reflect.DeepEqual(toMap(m.Metric[0].Label), map[string]string{
				"domain": "mongo_cps",
				"key1":   "database_users",
			}) &&
			*m.Metric[0].Histogram.SampleSum == 1.0
	}, time.Second, time.Millisecond)
}

func TestFlushResponseTimeConvertsMillisecondsToSeconds(t *testing.T) {
	s.FlushTimer("ratelimit_server.ShouldRateLimit.response_time", 1000)
	assert.Eventually(t, func() bool {
		metricFamilies, err := prometheus.DefaultGatherer.Gather()
		if err != nil {
			return false
		}

		metrics := make(map[string]*dto.MetricFamily)
		for _, metricFamily := range metricFamilies {
			metrics[*metricFamily.Name] = metricFamily
		}

		m, ok := metrics["ratelimit_service_response_time_seconds"]
		if !ok || len(m.Metric) != 1 {
			return false
		}

		return *m.Metric[0].Histogram.SampleCount == uint64(1) &&
			reflect.DeepEqual(toMap(m.Metric[0].Label), map[string]string{
				"grpc_method": "ShouldRateLimit",
			}) &&
			*m.Metric[0].Histogram.SampleSum == 1.0
	}, time.Second, time.Millisecond)
}

func TestFlushResponseTimeCanUseLegacyMilliseconds(t *testing.T) {
	oldRegisterer := prometheus.DefaultRegisterer
	oldGatherer := prometheus.DefaultGatherer
	reg := prometheus.NewRegistry()
	prometheus.DefaultRegisterer = reg
	prometheus.DefaultGatherer = reg
	defer func() {
		prometheus.DefaultRegisterer = oldRegisterer
		prometheus.DefaultGatherer = oldGatherer
	}()

	legacySink := NewPrometheusSink(WithAddr(":0"), WithPath("/metrics-legacy"), WithResponseTimeAsMilliseconds(true))
	legacySink.FlushTimer("ratelimit_server.ShouldRateLimit.response_time", 1000)

	assert.Eventually(t, func() bool {
		metricFamilies, err := reg.Gather()
		if err != nil {
			return false
		}

		metrics := make(map[string]*dto.MetricFamily)
		for _, metricFamily := range metricFamilies {
			metrics[*metricFamily.Name] = metricFamily
		}

		m, ok := metrics["ratelimit_service_response_time_seconds"]
		if !ok || len(m.Metric) != 1 {
			return false
		}

		return *m.Metric[0].Histogram.SampleCount == uint64(1) &&
			reflect.DeepEqual(toMap(m.Metric[0].Label), map[string]string{
				"grpc_method": "ShouldRateLimit",
			}) &&
			*m.Metric[0].Histogram.SampleSum == 1000.0
	}, time.Second, time.Millisecond)
}

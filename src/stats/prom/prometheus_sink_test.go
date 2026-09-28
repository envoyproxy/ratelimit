package prom

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/go-kit/log"
	stats "github.com/lyft/gostats"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/statsd_exporter/pkg/event"
	"github.com/prometheus/statsd_exporter/pkg/exporter"
	"github.com/prometheus/statsd_exporter/pkg/mapper"
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

func TestRedisClientMetricsNamesTypesAndUnits(t *testing.T) {
	for _, legacyMilliseconds := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_response_time=%t", legacyMilliseconds), func(t *testing.T) {
			// Exercise the production sink, mapper, and exporter without starting
			// another HTTP listener or changing the process-global registry.
			reg := prometheus.NewRegistry()
			sink := &prometheusSink{
				events: make(chan event.Events),
				mapper: &mapper.MetricMapper{Registerer: reg},
			}
			WithResponseTimeAsMilliseconds(legacyMilliseconds)(sink)
			require.NoError(t, sink.mapper.InitFromYAMLString(sink.mapperConfig()))
			sink.exp = exporter.NewExporter(reg, sink.mapper, log.NewNopLogger(),
				eventsActions, eventsUnmapped, errorEventStats, eventStats,
				conflictingEventStats, metricsCount)
			done := make(chan struct{})
			go func() {
				sink.exp.Listen(sink.events)
				close(done)
			}()
			t.Cleanup(func() {
				close(sink.events)
				<-done
			})

			store := stats.NewStore(sink, false)
			pools := []string{"redis_pool", "redis_per_second_pool"}
			operations := []string{"command", "pipeline", "startup_ping"}
			outcomes := []string{"success", "canceled", "deadline", "redis_error", "network_error", "other_error"}
			for _, pool := range pools {
				for _, operation := range operations {
					// Redis receives srv.Scope().Scope(pool), rooted at ratelimit,
					// separately from the statistics manager's service scope.
					scope := store.Scope("ratelimit").Scope(pool).Scope("client").Scope(operation)
					scope.NewCounter("calls_started").Add(6)
					scope.NewCounter("actions_attempted").Add(9)
					scope.NewGauge("calls_in_flight").Set(2)
					for _, outcome := range outcomes {
						scope.NewCounter("calls_completed." + outcome).Inc()
					}
					scope.NewMilliTimer("call_duration").AddValue(1250.5)
				}
			}
			poolScope := store.Scope("ratelimit").Scope("redis_pool")
			poolScope.NewGauge("cx_active").Set(3)
			poolScope.NewCounter("cx_total").Add(5)
			poolScope.NewCounter("cx_local_close").Add(4)
			poolScope.NewCounter("cx_connect_fail").Add(2)
			store.Flush()

			var metrics map[string]*dto.MetricFamily
			require.Eventually(t, func() bool {
				families, err := reg.Gather()
				if err != nil {
					return false
				}
				metrics = make(map[string]*dto.MetricFamily)
				for _, family := range families {
					metrics[family.GetName()] = family
				}
				for _, name := range []string{"calls_started_total", "actions_attempted_total", "calls_in_flight", "call_duration_seconds"} {
					if len(metrics["ratelimit_redis_client_"+name].GetMetric()) != 6 {
						return false
					}
				}
				return len(metrics["ratelimit_redis_client_calls_completed_total"].GetMetric()) == 36 &&
					metrics["ratelimit_redis_pool_cx_active"] != nil &&
					metrics["ratelimit_redis_pool_cx_total"] != nil &&
					metrics["ratelimit_redis_pool_cx_local_close"] != nil &&
					metrics["ratelimit_redis_pool_cx_connect_fail"] != nil
			}, time.Second, time.Millisecond)

			for _, expected := range []struct {
				name       string
				metricType dto.MetricType
				value      float64
			}{
				{"calls_started_total", dto.MetricType_COUNTER, 6},
				{"calls_completed_total", dto.MetricType_COUNTER, 1},
				{"actions_attempted_total", dto.MetricType_COUNTER, 9},
				{"calls_in_flight", dto.MetricType_GAUGE, 2},
				{"call_duration_seconds", dto.MetricType_HISTOGRAM, 1.2505},
			} {
				family := metrics["ratelimit_redis_client_"+expected.name]
				require.Equal(t, expected.metricType, family.GetType(), expected.name)
				for _, metric := range family.GetMetric() {
					labels := toMap(metric.Label)
					assert.Contains(t, pools, labels["pool"])
					assert.Contains(t, operations, labels["operation"])
					if expected.name == "calls_completed_total" {
						assert.Contains(t, outcomes, labels["outcome"])
						assert.Len(t, labels, 3)
					} else {
						assert.Len(t, labels, 2)
					}
					switch expected.metricType {
					case dto.MetricType_COUNTER:
						assert.Equal(t, expected.value, metric.GetCounter().GetValue())
					case dto.MetricType_GAUGE:
						assert.Equal(t, expected.value, metric.GetGauge().GetValue())
					case dto.MetricType_HISTOGRAM:
						assert.Equal(t, uint64(1), metric.GetHistogram().GetSampleCount())
						assert.InDelta(t, expected.value, metric.GetHistogram().GetSampleSum(), 1e-9)
					}
				}
			}
			for _, expected := range []struct {
				name       string
				metricType dto.MetricType
				value      float64
			}{
				{"ratelimit_redis_pool_cx_active", dto.MetricType_GAUGE, 3},
				{"ratelimit_redis_pool_cx_total", dto.MetricType_COUNTER, 5},
				{"ratelimit_redis_pool_cx_local_close", dto.MetricType_COUNTER, 4},
				{"ratelimit_redis_pool_cx_connect_fail", dto.MetricType_COUNTER, 2},
			} {
				family := metrics[expected.name]
				require.Equal(t, expected.metricType, family.GetType(), expected.name)
				require.Len(t, family.GetMetric(), 1)
				metric := family.GetMetric()[0]
				assert.Empty(t, metric.GetLabel())
				switch expected.metricType {
				case dto.MetricType_COUNTER:
					assert.Equal(t, expected.value, metric.GetCounter().GetValue())
				case dto.MetricType_GAUGE:
					assert.Equal(t, expected.value, metric.GetGauge().GetValue())
				}
			}
		})
	}
}

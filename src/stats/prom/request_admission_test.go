package prom

import (
	"fmt"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	gostats "github.com/lyft/gostats"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
)

func TestRequestAdmissionMetricNamesTypesAndUnits(t *testing.T) {
	for _, legacyResponseTime := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_response_time_%t", legacyResponseTime), func(t *testing.T) {
			oldRegisterer, oldGatherer := prometheus.DefaultRegisterer, prometheus.DefaultGatherer
			reg := prometheus.NewRegistry()
			prometheus.DefaultRegisterer, prometheus.DefaultGatherer = reg, reg
			t.Cleanup(func() {
				prometheus.DefaultRegisterer, prometheus.DefaultGatherer = oldRegisterer, oldGatherer
			})
			sink := NewPrometheusSink(WithAddr("127.0.0.1:0"),
				WithPath(fmt.Sprintf("/metrics-admission-%t", legacyResponseTime)),
				WithResponseTimeAsMilliseconds(legacyResponseTime))
			store := gostats.NewStore(sink, false)
			admission := stats.NewStatManager(store, settings.Settings{}).NewServiceStats().RequestAdmission
			admission.Admitted.Add(2)
			admission.Rejected.Add(3)
			admission.InFlight.Set(1)
			admission.CompletedDuration.AllocateSpan().CompleteWithDuration(1250 * time.Millisecond)
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
				return metrics["ratelimit_service_request_admission_admitted_total"] != nil &&
					metrics["ratelimit_service_request_admission_rejected_total"] != nil &&
					metrics["ratelimit_service_request_admission_in_flight"] != nil &&
					metrics["ratelimit_service_request_admission_completed_duration_seconds"] != nil
			}, time.Second, time.Millisecond)

			admitted := metrics["ratelimit_service_request_admission_admitted_total"]
			require.Equal(t, dto.MetricType_COUNTER, admitted.GetType())
			require.Len(t, admitted.Metric, 1)
			require.Empty(t, admitted.Metric[0].Label)
			require.Equal(t, float64(2), admitted.Metric[0].Counter.GetValue())
			rejected := metrics["ratelimit_service_request_admission_rejected_total"]
			require.Equal(t, dto.MetricType_COUNTER, rejected.GetType())
			require.Equal(t, float64(3), rejected.Metric[0].Counter.GetValue())
			inFlight := metrics["ratelimit_service_request_admission_in_flight"]
			require.Equal(t, dto.MetricType_GAUGE, inFlight.GetType())
			require.Equal(t, float64(1), inFlight.Metric[0].Gauge.GetValue())
			duration := metrics["ratelimit_service_request_admission_completed_duration_seconds"]
			require.Equal(t, dto.MetricType_HISTOGRAM, duration.GetType())
			require.Equal(t, uint64(1), duration.Metric[0].Histogram.GetSampleCount())
			require.Equal(t, 1.25, duration.Metric[0].Histogram.GetSampleSum())

			endpoint := httptest.NewServer(promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
			defer endpoint.Close()
			response, err := endpoint.Client().Get(endpoint.URL + "/metrics")
			require.NoError(t, err)
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Contains(t, string(body), "ratelimit_service_request_admission_admitted_total 2\n")
			require.Contains(t, string(body), "ratelimit_service_request_admission_rejected_total 3\n")
			require.Contains(t, string(body), "ratelimit_service_request_admission_in_flight 1\n")
			require.Contains(t, string(body), "ratelimit_service_request_admission_completed_duration_seconds_sum 1.25\n")
			require.Contains(t, string(body), "ratelimit_service_request_admission_completed_duration_seconds_count 1\n")
		})
	}
}

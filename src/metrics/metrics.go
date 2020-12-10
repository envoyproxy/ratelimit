package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ShadowRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rate_limiting_shadow_requests",
		Help: "The total number of requests that would of been rate limited not in shadow mode",
	}, []string{"descriptor_key", "descriptor_value", "limit", "unit"})
	LimitedRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rate_limiting_limited_requests",
		Help: "The total number of requests that have been rate limited",
	}, []string{"descriptor_key", "descriptor_value", "limit", "unit"})
	RateLimitRequestSummary = promauto.NewSummary(prometheus.SummaryOpts{
		Name:       "rate_limiting_request_time_sec",
		Help:       "Summary of rate limiting request times",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	})
	RateLimitErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rate_limiting_service_errors",
		Help: "Count of different rate limiting errors",
	}, []string{"type"})
)

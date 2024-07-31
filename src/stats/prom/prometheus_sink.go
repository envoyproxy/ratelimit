package prom

import (
	_ "embed"
	"net/http"

	"github.com/go-kit/log"
	gostats "github.com/lyft/gostats"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/statsd_exporter/pkg/event"
	"github.com/prometheus/statsd_exporter/pkg/exporter"
	"github.com/prometheus/statsd_exporter/pkg/mapper"
	"github.com/sirupsen/logrus"
)

var (
	//go:embed default_mapper.yaml
	defaultMapper string
	_             gostats.Sink = &prometheusSink{}

	eventsActions = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "statsd_exporter_events_actions_total",
			Help: "The total number of StatsD events by action.",
		},
		[]string{"action"},
	)
	eventsUnmapped = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "statsd_exporter_events_unmapped_total",
			Help: "The total number of StatsD events no mapping was found for.",
		})
	metricsCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "statsd_exporter_metrics_total",
			Help: "The total number of metrics.",
		},
		[]string{"type"},
	)
	conflictingEventStats = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "statsd_exporter_events_conflict_total",
			Help: "The total number of StatsD events with conflicting names.",
		},
		[]string{"type"},
	)
	eventStats = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "statsd_exporter_events_total",
			Help: "The total number of StatsD events seen.",
		},
		[]string{"type"},
	)
	errorEventStats = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "statsd_exporter_events_error_total",
			Help: "The total number of StatsD events discarded due to errors.",
		},
		[]string{"reason"},
	)
)

type prometheusSink struct {
	config struct {
		addr           string
		path           string
		mapperYamlPath string
	}
	mapper *mapper.MetricMapper
	events chan event.Events
	exp    *exporter.Exporter
}

type prometheusSinkOption func(sink *prometheusSink)

func WithAddr(addr string) prometheusSinkOption {
	return func(sink *prometheusSink) {
		sink.config.addr = addr
	}
}

func WithPath(path string) prometheusSinkOption {
	return func(sink *prometheusSink) {
		sink.config.path = path
	}
}

func WithMapperYamlPath(mapperYamlPath string) prometheusSinkOption {
	return func(sink *prometheusSink) {
		sink.config.mapperYamlPath = mapperYamlPath
	}
}

// NewPrometheusSink returns a Sink that flushes stats to os.StdErr.
func NewPrometheusSink(opts ...prometheusSinkOption) gostats.Sink {
	promRegistry := prometheus.DefaultRegisterer
	sink := &prometheusSink{
		events: make(chan event.Events),
		mapper: &mapper.MetricMapper{
			Registerer: promRegistry,
		},
	}
	for _, opt := range opts {
		opt(sink)
	}
	if sink.config.addr == "" {
		sink.config.addr = ":9090"
	}
	if sink.config.path == "" {
		sink.config.path = "/metrics"
	}
	http.Handle(sink.config.path, promhttp.Handler())
	go func() {
		logrus.Infof("Starting prometheus sink on %s%s", sink.config.addr, sink.config.path)
		_ = http.ListenAndServe(sink.config.addr, nil)
	}()
	if sink.config.mapperYamlPath != "" {
		_ = sink.mapper.InitFromFile(sink.config.mapperYamlPath)
	} else {
		_ = sink.mapper.InitFromYAMLString(defaultMapper)
	}

	sink.exp = exporter.NewExporter(promRegistry,
		sink.mapper, log.NewNopLogger(),
		eventsActions, eventsUnmapped,
		errorEventStats, eventStats,
		conflictingEventStats, metricsCount)

	go func() {
		sink.exp.Listen(sink.events)
	}()

	return sink
}

func (s *prometheusSink) FlushCounter(name string, value uint64) {
	s.events <- event.Events{&event.CounterEvent{
		CMetricName: name,
		CValue:      float64(value),
		CLabels:     make(map[string]string),
	}}
}

func (s *prometheusSink) FlushGauge(name string, value uint64) {
	s.events <- event.Events{&event.GaugeEvent{
		GMetricName: name,
		GValue:      float64(value),
		GLabels:     make(map[string]string),
	}}
}

func (s *prometheusSink) FlushTimer(name string, value float64) {
	s.events <- event.Events{&event.ObserverEvent{
		OMetricName: name,
		OValue:      value,
		OLabels:     make(map[string]string),
	}}
}

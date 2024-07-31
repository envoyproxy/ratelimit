package prometheusstats

import (
	_ "embed"
	"net/http"

	gostats "github.com/lyft/gostats"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/statsd_exporter/pkg/mapper"
	"github.com/sirupsen/logrus"
)

var (
	//go:embed default_mapper.yaml
	defaultMapper string
	_             gostats.Sink = &prometheusSink{}
)

type prometheusSink struct {
	config struct {
		addr           string
		path           string
		mapperYamlPath string
	}
	counters   map[string]*prometheus.CounterVec
	gauges     map[string]*prometheus.GaugeVec
	histograms map[string]*prometheus.HistogramVec
	mapper     *mapper.MetricMapper
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
	sink := &prometheusSink{
		counters:   make(map[string]*prometheus.CounterVec),
		gauges:     make(map[string]*prometheus.GaugeVec),
		histograms: make(map[string]*prometheus.HistogramVec),
		mapper: &mapper.MetricMapper{
			Registerer: prometheus.DefaultRegisterer,
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
	return sink
}

func (s *prometheusSink) FlushCounter(name string, value uint64) {
	m, labels, present := s.mapper.GetMapping(name, mapper.MetricTypeCounter)
	if present {
		labelNames := make([]string, 0, len(labels))
		labelValues := make([]string, 0, len(labels))
		for k, v := range labels {
			labelNames = append(labelNames, k)
			labelValues = append(labelValues, v)
		}

		metricName := mapper.EscapeMetricName(m.Name)
		if _, ok := s.counters[metricName]; !ok {
			s.counters[metricName] = promauto.NewCounterVec(prometheus.CounterOpts{Name: metricName}, labelNames)
		}
		counter := s.counters[metricName]
		counter.WithLabelValues(labelValues...).Add(float64(value))
	}
}

func (s *prometheusSink) FlushGauge(name string, value uint64) {
	m, labels, present := s.mapper.GetMapping(name, mapper.MetricTypeCounter)
	if present {
		labelNames := make([]string, 0, len(labels))
		labelValues := make([]string, 0, len(labels))
		for k, v := range labels {
			labelNames = append(labelNames, k)
			labelValues = append(labelValues, v)
		}

		metricName := mapper.EscapeMetricName(m.Name)
		if _, ok := s.gauges[metricName]; !ok {
			s.gauges[metricName] = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: metricName}, labelNames)
		}

		gauge := s.gauges[metricName]
		gauge.WithLabelValues(labelValues...).Set(float64(value))
	}
}

func (s *prometheusSink) FlushTimer(name string, value float64) {
	m, labels, present := s.mapper.GetMapping(name, mapper.MetricTypeCounter)
	if present {
		labelNames := make([]string, 0, len(labels))
		labelValues := make([]string, 0, len(labels))
		for k, v := range labels {
			labelNames = append(labelNames, k)
			labelValues = append(labelValues, v)
		}

		metricName := mapper.EscapeMetricName(m.Name)
		if _, ok := s.histograms[metricName]; !ok {
			s.histograms[metricName] = promauto.NewHistogramVec(prometheus.HistogramOpts{Name: metricName}, labelNames)
		}

		histogram := s.histograms[metricName]
		histogram.WithLabelValues(labelValues...).Observe(value)
	}
}

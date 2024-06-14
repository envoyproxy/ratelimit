package godogstats

import (
	"regexp"
	"strconv"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
	gostats "github.com/lyft/gostats"
)

type godogStatsSink struct {
	client *statsd.Client
	config struct {
		host string
		port int
	}

	mogrifier mogrifierMap
}

// ensure that godogStatsSink implements gostats.Sink
var _ gostats.Sink = (*godogStatsSink)(nil)

type goDogStatsSinkOption func(*godogStatsSink)

func WithStatsdHost(host string) goDogStatsSinkOption {
	return func(g *godogStatsSink) {
		g.config.host = host
	}
}

func WithStatsdPort(port int) goDogStatsSinkOption {
	return func(g *godogStatsSink) {
		g.config.port = port
	}
}

func WithMogrifier(mogrifiers map[*regexp.Regexp]func([]string) (string, []string)) goDogStatsSinkOption {
	return func(g *godogStatsSink) {
		g.mogrifier = mogrifiers
	}
}

func WithMogrifierFromEnv(keys []string) goDogStatsSinkOption {
	return func(g *godogStatsSink) {
		mogrifier, err := newMogrifierMapFromEnv(keys)
		if err != nil {
			panic(err)
		}
		g.mogrifier = mogrifier
	}
}

func NewSink(opts ...goDogStatsSinkOption) (*godogStatsSink, error) {
	sink := &godogStatsSink{}
	for _, opt := range opts {
		opt(sink)
	}
	client, err := statsd.New(sink.config.host+":"+strconv.Itoa(sink.config.port), statsd.WithoutClientSideAggregation())
	if err != nil {
		return nil, err
	}
	sink.client = client
	return sink, nil
}

func (g *godogStatsSink) FlushCounter(name string, value uint64) {
	name, tags := g.mogrifier.mogrify(name)
	g.client.Count(name, int64(value), tags, 1.0)
}

func (g *godogStatsSink) FlushGauge(name string, value uint64) {
	name, tags := g.mogrifier.mogrify(name)
	g.client.Gauge(name, float64(value), tags, 1.0)
}

func (g *godogStatsSink) FlushTimer(name string, milliseconds float64) {
	name, tags := g.mogrifier.mogrify(name)
	duration := time.Duration(milliseconds) * time.Millisecond
	g.client.Timing(name, duration, tags, 1.0)
}

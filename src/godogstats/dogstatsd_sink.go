package godogstats

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
	gostats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
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

// WithMogrifier adds a mogrifier to the sink. Map iteration order is randomized, to control order call multiple times.
func WithMogrifier(mogrifiers map[*regexp.Regexp]func([]string) (string, []string)) goDogStatsSinkOption {
	return func(g *godogStatsSink) {
		for m, h := range mogrifiers {
			g.mogrifier = append(g.mogrifier, mogrifierEntry{
				matcher: m,
				handler: h,
			})
		}
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

// separateTags separates the metric name and tags from the combined serialized metric name.
// e.g. given input: "ratelimit.service.rate_limit.mongo_cps.database_users.total_hits.__COMMIT=12345.__DEPLOY=67890"
// this should produce output: "ratelimit.service.rate_limit.mongo_cps.database_users.total_hits", ["COMMIT:12345", "DEPLOY:67890"]
// Aligns to how tags are serialized here https://github.com/lyft/gostats/blob/49e70f1b7932d146fecd991be04f8e1ad235452c/internal/tags/tags.go#L335
func separateTags(name string) (string, []string) {
	const (
		prefix = ".__"
		sep    = "="
	)

	// split the name and tags about the first prefix for extra tags
	shortName, tagString, hasTags := strings.Cut(name, prefix)
	if !hasTags {
		return name, nil
	}

	// split the tags at every instance of prefix
	tagPairs := strings.Split(tagString, prefix)
	tags := make([]string, 0, len(tagPairs))
	for _, tagPair := range tagPairs {
		// split the name + value by the seperator
		tagName, tagValue, isValid := strings.Cut(tagPair, sep)
		if !isValid {
			logger.Debugf("godogstats sink found malformed extra tag: %v, string: %v", tagPair, name)
			continue
		}
		tags = append(tags, tagName+":"+tagValue)
	}

	return shortName, tags
}

// mogrify takes a serialized metric name as input (internal gostats format)
// and returns a metric name and list of tags (dogstatsd output format)
// the output list of tags includes any "tags" that are serialized into the metric name,
// as well as any other tags emitted by the mogrifier config
func (g *godogStatsSink) mogrify(name string) (string, []string) {
	name, extraTags := separateTags(name)
	name, tags := g.mogrifier.mogrify(name)
	return name, append(extraTags, tags...)
}

func (g *godogStatsSink) FlushCounter(name string, value uint64) {
	name, tags := g.mogrify(name)
	g.client.Count(name, int64(value), tags, 1.0)
}

func (g *godogStatsSink) FlushGauge(name string, value uint64) {
	name, tags := g.mogrify(name)
	g.client.Gauge(name, float64(value), tags, 1.0)
}

func (g *godogStatsSink) FlushTimer(name string, milliseconds float64) {
	name, tags := g.mogrify(name)
	duration := time.Duration(milliseconds) * time.Millisecond
	g.client.Timing(name, duration, tags, 1.0)
}

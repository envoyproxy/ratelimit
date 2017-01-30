package config_test

import (
	"io/ioutil"
	"testing"

	"github.com/lyft/gostats"
	pb "github.com/lyft/ratelimit/proto/ratelimit"
	"github.com/lyft/ratelimit/src/config"

	"github.com/stretchr/testify/assert"
)

func loadFile(path string) []config.RateLimitConfigToLoad {
	contents, err := ioutil.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return []config.RateLimitConfigToLoad{{path, string(contents)}}
}

func TestBasicConfig(t *testing.T) {
	assert := assert.New(t)
	stats := stats.NewStore(stats.NewNullSink(), false)
	rlConfig := config.NewRateLimitConfigImpl(loadFile("basic_config.yaml"), stats)
	rlConfig.Dump()
	assert.Nil(rlConfig.GetLimit(nil, "foo_domain", &pb.RateLimitDescriptor{}))
	assert.Nil(rlConfig.GetLimit(nil, "test-domain", &pb.RateLimitDescriptor{}))

	rl := rlConfig.GetLimit(
		nil, "test-domain",
		&pb.RateLimitDescriptor{[]*pb.RateLimitDescriptor_Entry{{"key1", "something"}}})
	assert.Nil(rl)

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb.RateLimitDescriptor{[]*pb.RateLimitDescriptor_Entry{{"key1", "value1"}}})
	assert.Nil(rl)

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb.RateLimitDescriptor{[]*pb.RateLimitDescriptor_Entry{{"key2", "value2"}, {"subkey", "subvalue"}}})
	assert.Nil(rl)

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb.RateLimitDescriptor{[]*pb.RateLimitDescriptor_Entry{{"key5", "value5"}, {"subkey5", "subvalue"}}})
	assert.Nil(rl)

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb.RateLimitDescriptor{
			[]*pb.RateLimitDescriptor_Entry{{"key1", "value1"}, {"subkey1", "something"}}})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	assert.EqualValues(5, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimit_SECOND, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1.near_limit").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb.RateLimitDescriptor{
			[]*pb.RateLimitDescriptor_Entry{{"key1", "value1"}, {"subkey1", "subvalue1"}}})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	assert.EqualValues(10, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimit_SECOND, rl.Limit.Unit)
	assert.EqualValues(
		1, stats.NewCounter("test-domain.key1_value1.subkey1_subvalue1.total_hits").Value())
	assert.EqualValues(
		1, stats.NewCounter("test-domain.key1_value1.subkey1_subvalue1.over_limit").Value())
	assert.EqualValues(
		1, stats.NewCounter("test-domain.key1_value1.subkey1_subvalue1.near_limit").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb.RateLimitDescriptor{[]*pb.RateLimitDescriptor_Entry{{"key2", "something"}}})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	assert.EqualValues(20, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimit_MINUTE, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key2.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2.near_limit").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb.RateLimitDescriptor{[]*pb.RateLimitDescriptor_Entry{{"key2", "value2"}}})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	assert.EqualValues(30, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimit_MINUTE, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key2_value2.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2_value2.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2_value2.near_limit").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb.RateLimitDescriptor{[]*pb.RateLimitDescriptor_Entry{{"key3", "foo"}}})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	assert.EqualValues(1, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimit_HOUR, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key3.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key3.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key3.near_limit").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb.RateLimitDescriptor{[]*pb.RateLimitDescriptor_Entry{{"key4", "foo"}}})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	assert.EqualValues(1, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimit_DAY, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key4.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key4.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key4.near_limit").Value())
}

func expectConfigPanic(t *testing.T, call func(), expectedError string) {
	assert := assert.New(t)
	defer func() {
		e := recover()
		assert.NotNil(e)
		assert.Equal(expectedError, e.(error).Error())
	}()

	call()
}

func TestEmptyDomain(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("empty_domain.yaml"), stats.NewStore(stats.NewNullSink(), false))
		},
		"empty_domain.yaml: config file cannot have empty domain")
}

func TestDuplicateDomain(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			files := loadFile("basic_config.yaml")
			files = append(files, loadFile("duplicate_domain.yaml")...)
			config.NewRateLimitConfigImpl(files, stats.NewStore(stats.NewNullSink(), false))
		},
		"duplicate_domain.yaml: duplicate domain 'test-domain' in config file")
}

func TestEmptyKey(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("empty_key.yaml"),
				stats.NewStore(stats.NewNullSink(), false))
		},
		"empty_key.yaml: descriptor has empty key")
}

func TestDuplicateKey(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("duplicate_key.yaml"),
				stats.NewStore(stats.NewNullSink(), false))
		},
		"duplicate_key.yaml: duplicate descriptor composite key 'test-domain.key1_value1'")
}

func TestBadLimitUnit(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("bad_limit_unit.yaml"),
				stats.NewStore(stats.NewNullSink(), false))
		},
		"bad_limit_unit.yaml: invalid rate limit unit 'foo'")
}

func TestBadYaml(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("bad_yaml.yaml"),
				stats.NewStore(stats.NewNullSink(), false))
		},
		"bad_yaml.yaml: error loading config file: yaml: line 1: found unexpected end of stream")
}

func TestMisspelledKey(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("misspelled_key.yaml"),
				stats.NewStore(stats.NewNullSink(), false))
		},
		"misspelled_key.yaml: config error, unknown key 'ratelimit'")

	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("misspelled_key2.yaml"),
				stats.NewStore(stats.NewNullSink(), false))
		},
		"misspelled_key2.yaml: config error, unknown key 'requestsperunit'")
}

func TestNonStringKey(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("non_string_key.yaml"),
				stats.NewStore(stats.NewNullSink(), false))
		},
		"non_string_key.yaml: config error, key is not of type string: 0.25")
}

func TestNonMapList(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("non_map_list.yaml"),
				stats.NewStore(stats.NewNullSink(), false))
		},
		"non_map_list.yaml: config error, yaml file contains list of type other than map: a")
}

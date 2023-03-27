package config_test

import (
	"io/ioutil"
	"testing"

	"github.com/envoyproxy/ratelimit/test/common"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	pb_type "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/config"
	mockstats "github.com/envoyproxy/ratelimit/test/mocks/stats"
)

func loadFile(path string) []config.RateLimitConfigToLoad {
	contents, err := ioutil.ReadFile(path)
	if err != nil {
		panic(err)
	}
	configYaml := config.ConfigFileContentToYaml(path, string(contents))
	return []config.RateLimitConfigToLoad{{Name: path, ConfigYaml: configYaml}}
}

func TestBasicConfig(t *testing.T) {
	assert := assert.New(t)
	stats := stats.NewStore(stats.NewNullSink(), false)
	rlConfig := config.NewRateLimitConfigImpl(loadFile("basic_config.yaml"), mockstats.NewMockStatManager(stats), false)
	rlConfig.Dump()
	assert.Equal(rlConfig.IsEmptyDomains(), false)
	assert.Nil(rlConfig.GetLimit(nil, "foo_domain", &pb_struct.RateLimitDescriptor{}))
	assert.Nil(rlConfig.GetLimit(nil, "test-domain", &pb_struct.RateLimitDescriptor{}))

	rl := rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key1", Value: "something"}},
		})
	assert.Nil(rl)

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key1", Value: "value1"}},
		})
	assert.Nil(rl)

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key2", Value: "value2"}, {Key: "subkey", Value: "subvalue"}},
		})
	assert.Nil(rl)

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key5", Value: "value5"}, {Key: "subkey5", Value: "subvalue"}},
		})
	assert.Nil(rl)

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key1", Value: "value1"}, {Key: "subkey1", Value: "something"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.WithinLimit.Inc()
	assert.EqualValues(5, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_SECOND, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1.near_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1.within_limit").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key1", Value: "value1"}, {Key: "subkey1", Value: "subvalue1"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.WithinLimit.Inc()
	assert.EqualValues(10, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_SECOND, rl.Limit.Unit)
	assert.EqualValues(
		1, stats.NewCounter("test-domain.key1_value1.subkey1_subvalue1.total_hits").Value())
	assert.EqualValues(
		1, stats.NewCounter("test-domain.key1_value1.subkey1_subvalue1.over_limit").Value())
	assert.EqualValues(
		1, stats.NewCounter("test-domain.key1_value1.subkey1_subvalue1.near_limit").Value())
	assert.EqualValues(
		1, stats.NewCounter("test-domain.key1_value1.subkey1_subvalue1.within_limit").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key2", Value: "something"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.WithinLimit.Inc()
	assert.EqualValues(20, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_MINUTE, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key2.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2.near_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2.within_limit").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key2", Value: "value2"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.WithinLimit.Inc()
	assert.EqualValues(30, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_MINUTE, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key2_value2.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2_value2.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2_value2.near_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2_value2.within_limit").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key2", Value: "value3"}},
		})
	assert.Nil(rl)

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key3", Value: "foo"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.WithinLimit.Inc()
	assert.EqualValues(1, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_HOUR, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key3.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key3.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key3.near_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key3.within_limit").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key4", Value: "foo"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.WithinLimit.Inc()
	assert.EqualValues(1, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_DAY, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key4.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key4.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key4.near_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key4.within_limit").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key6", Value: "foo"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.WithinLimit.Inc()
	assert.True(rl.Unlimited)
	assert.EqualValues(1, stats.NewCounter("test-domain.key6.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key6.within_limit").Value())

	// A value for the key with detailed_metric: true
	// should also generate a stat with the value included
	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key7", Value: "unspecified_value"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.WithinLimit.Inc()
	assert.EqualValues(70, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_MINUTE, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key7_unspecified_value.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key7_unspecified_value.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key7_unspecified_value.near_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key7_unspecified_value.within_limit").Value())

	// Another value for the key with detailed_metric: true
	// should also generate a stat with the value included
	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key7", Value: "another_value"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.WithinLimit.Inc()
	assert.EqualValues(70, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_MINUTE, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key7_another_value.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key7_another_value.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key7_another_value.near_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key7_another_value.within_limit").Value())
}

func TestDomainMerge(t *testing.T) {
	assert := assert.New(t)
	stats := stats.NewStore(stats.NewNullSink(), false)

	files := loadFile("merge_domain_key1.yaml")
	files = append(files, loadFile("merge_domain_key2.yaml")...)

	rlConfig := config.NewRateLimitConfigImpl(files, mockstats.NewMockStatManager(stats), true)
	rlConfig.Dump()
	assert.Nil(rlConfig.GetLimit(nil, "foo_domain", &pb_struct.RateLimitDescriptor{}))
	assert.Nil(rlConfig.GetLimit(nil, "test-domain", &pb_struct.RateLimitDescriptor{}))

	rl := rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key1", Value: "value1"}},
		})
	assert.NotNil(rl)
	assert.EqualValues(10, rl.Limit.RequestsPerUnit)

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key2", Value: "value2"}},
		})
	assert.NotNil(rl)
	assert.EqualValues(20, rl.Limit.RequestsPerUnit)
}

func TestConfigLimitOverride(t *testing.T) {
	assert := assert.New(t)
	stats := stats.NewStore(stats.NewNullSink(), false)
	rlConfig := config.NewRateLimitConfigImpl(loadFile("basic_config.yaml"), mockstats.NewMockStatManager(stats), false)
	rlConfig.Dump()
	// No matching domain
	assert.Nil(rlConfig.GetLimit(nil, "foo_domain", &pb_struct.RateLimitDescriptor{
		Limit: &pb_struct.RateLimitDescriptor_RateLimitOverride{
			RequestsPerUnit: 10, Unit: pb_type.RateLimitUnit_DAY,
		},
	}))
	rl := rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key1", Value: "value1"}, {Key: "subkey1", Value: "something"}},
			Limit: &pb_struct.RateLimitDescriptor_RateLimitOverride{
				RequestsPerUnit: 10, Unit: pb_type.RateLimitUnit_DAY,
			},
		})
	assert.Equal("test-domain.key1_value1.subkey1_something", rl.FullKey)
	common.AssertProtoEqual(assert, &pb.RateLimitResponse_RateLimit{
		RequestsPerUnit: 10,
		Unit:            pb.RateLimitResponse_RateLimit_DAY,
	}, rl.Limit)
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.WithinLimit.Inc()
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1_something.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1_something.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1_something.near_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1_something.within_limit").Value())

	// Change in override value doesn't erase stats
	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key1", Value: "value1"}, {Key: "subkey1", Value: "something"}},
			Limit: &pb_struct.RateLimitDescriptor_RateLimitOverride{
				RequestsPerUnit: 42, Unit: pb_type.RateLimitUnit_HOUR,
			},
		})
	assert.Equal("test-domain.key1_value1.subkey1_something", rl.FullKey)
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.WithinLimit.Inc()
	common.AssertProtoEqual(assert, &pb.RateLimitResponse_RateLimit{
		RequestsPerUnit: 42,
		Unit:            pb.RateLimitResponse_RateLimit_HOUR,
	}, rl.Limit)
	assert.EqualValues(2, stats.NewCounter("test-domain.key1_value1.subkey1_something.total_hits").Value())
	assert.EqualValues(2, stats.NewCounter("test-domain.key1_value1.subkey1_something.over_limit").Value())
	assert.EqualValues(2, stats.NewCounter("test-domain.key1_value1.subkey1_something.near_limit").Value())
	assert.EqualValues(2, stats.NewCounter("test-domain.key1_value1.subkey1_something.within_limit").Value())

	// Different value creates a different counter
	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key1", Value: "value1"}, {Key: "subkey1", Value: "something_else"}},
			Limit: &pb_struct.RateLimitDescriptor_RateLimitOverride{
				RequestsPerUnit: 42, Unit: pb_type.RateLimitUnit_HOUR,
			},
		})
	assert.Equal("test-domain.key1_value1.subkey1_something_else", rl.FullKey)
	common.AssertProtoEqual(assert, &pb.RateLimitResponse_RateLimit{
		RequestsPerUnit: 42,
		Unit:            pb.RateLimitResponse_RateLimit_HOUR,
	}, rl.Limit)
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.WithinLimit.Inc()
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1_something_else.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1_something_else.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1_something_else.near_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1_something_else.within_limit").Value())
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
				loadFile("empty_domain.yaml"), mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"empty_domain.yaml: config file cannot have empty domain")
}

func TestDuplicateDomain(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			files := loadFile("basic_config.yaml")
			files = append(files, loadFile("duplicate_domain.yaml")...)
			config.NewRateLimitConfigImpl(files, mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"duplicate_domain.yaml: duplicate domain 'test-domain' in config file")
}

func TestEmptyKey(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("empty_key.yaml"),
				mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"empty_key.yaml: descriptor has empty key")
}

func TestDuplicateKey(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("duplicate_key.yaml"),
				mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"duplicate_key.yaml: duplicate descriptor composite key 'test-domain.key1_value1'")
}

func TestDuplicateKeyDomainMerge(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			files := loadFile("merge_domain_key1.yaml")
			files = append(files, loadFile("merge_domain_key1.yaml")...)
			config.NewRateLimitConfigImpl(
				files,
				mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), true)
		},
		"merge_domain_key1.yaml: duplicate descriptor composite key 'test-domain.key1_value1'")
}

func TestBadLimitUnit(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("bad_limit_unit.yaml"),
				mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"bad_limit_unit.yaml: invalid rate limit unit 'foo'")
}

func TestReplacesSelf(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("replaces_self.yaml"),
				mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"replaces_self.yaml: replaces should not contain name of same descriptor")
}

func TestReplacesEmpty(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("replaces_empty.yaml"),
				mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"replaces_empty.yaml: should not have an empty replaces entry")
}

func TestBadYaml(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("bad_yaml.yaml"),
				mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"bad_yaml.yaml: error loading config file: yaml: line 2: found unexpected end of stream")
}

func TestMisspelledKey(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("misspelled_key.yaml"),
				mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"misspelled_key.yaml: config error, unknown key 'ratelimit'")

	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("misspelled_key2.yaml"),
				mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"misspelled_key2.yaml: config error, unknown key 'requestsperunit'")
}

func TestNonStringKey(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("non_string_key.yaml"),
				mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"non_string_key.yaml: config error, key is not of type string: 0.25")
}

func TestNonMapList(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("non_map_list.yaml"),
				mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"non_map_list.yaml: config error, yaml file contains list of type other than map: a")
}

func TestUnlimitedWithRateLimitUnit(t *testing.T) {
	expectConfigPanic(
		t,
		func() {
			config.NewRateLimitConfigImpl(
				loadFile("unlimited_with_unit.yaml"),
				mockstats.NewMockStatManager(stats.NewStore(stats.NewNullSink(), false)), false)
		},
		"unlimited_with_unit.yaml: should not specify rate limit unit when unlimited")
}

func TestShadowModeConfig(t *testing.T) {
	assert := assert.New(t)
	stats := stats.NewStore(stats.NewNullSink(), false)

	rlConfig := config.NewRateLimitConfigImpl(loadFile("shadowmode_config.yaml"), mockstats.NewMockStatManager(stats), false)
	rlConfig.Dump()

	rl := rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key1", Value: "value1"}, {Key: "subkey1", Value: "something"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()

	assert.Equal(rl.ShadowMode, false)
	assert.EqualValues(5, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_SECOND, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1.near_limit").Value())
	assert.EqualValues(0, stats.NewCounter("test-domain.key1_value1.subkey1.shadow_mode").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key1", Value: "value1"}, {Key: "subkey1", Value: "subvalue1"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.ShadowMode.Inc()

	assert.Equal(rl.ShadowMode, true)
	assert.EqualValues(10, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_SECOND, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1_subvalue1.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1_subvalue1.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1_subvalue1.near_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key1_value1.subkey1_subvalue1.shadow_mode").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key2", Value: "something"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	rl.Stats.ShadowMode.Inc()
	assert.Equal(rl.ShadowMode, true)
	assert.EqualValues(20, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_MINUTE, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key2.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2.near_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2.shadow_mode").Value())

	rl = rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "key2", Value: "value2"}},
		})
	rl.Stats.TotalHits.Inc()
	rl.Stats.OverLimit.Inc()
	rl.Stats.NearLimit.Inc()
	assert.Equal(rl.ShadowMode, false)
	assert.EqualValues(30, rl.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_MINUTE, rl.Limit.Unit)
	assert.EqualValues(1, stats.NewCounter("test-domain.key2_value2.total_hits").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2_value2.over_limit").Value())
	assert.EqualValues(1, stats.NewCounter("test-domain.key2_value2.near_limit").Value())
	assert.EqualValues(0, stats.NewCounter("test-domain.key2_value2.shadow_mode").Value())
}

func TestWildcardConfig(t *testing.T) {
	assert := assert.New(t)
	stats := stats.NewStore(stats.NewNullSink(), false)
	rlConfig := config.NewRateLimitConfigImpl(loadFile("wildcard.yaml"), mockstats.NewMockStatManager(stats), false)
	rlConfig.Dump()

	// Baseline to show wildcard works like no value
	withoutVal1 := rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "noVal", Value: "foo1"}},
		})
	withoutVal2 := rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "noVal", Value: "foo2"}},
		})
	assert.NotNil(withoutVal1)
	assert.Equal(withoutVal1, withoutVal2)

	// Matches multiple wildcard values and results are equal
	wildcard1 := rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "wild", Value: "foo1"}},
		})
	wildcard2 := rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "wild", Value: "foo2"}},
		})
	assert.NotNil(wildcard1)
	assert.Equal(wildcard1, wildcard2)

	// Doesn't match non-matching values
	noMatch := rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "wild", Value: "bar"}},
		})
	assert.Nil(noMatch)

	// Non-wildcard values don't eager match
	eager := rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "noWild", Value: "foo1"}},
		})
	assert.Nil(eager)

	// Wildcard in the middle of value is not supported.
	midWildcard := rlConfig.GetLimit(
		nil, "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "midWildcard", Value: "barab"}},
		})
	assert.Nil(midWildcard)
}

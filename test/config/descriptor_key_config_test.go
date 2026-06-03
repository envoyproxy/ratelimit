package config_test

import (
	"context"
	"testing"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb_type "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	gostats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/config"
	mockstats "github.com/envoyproxy/ratelimit/test/mocks/stats"
)

func TestDescriptorKeyConfigSelectiveValues(t *testing.T) {
	assert := assert.New(t)
	yaml := []byte(`
default:
  include_entry_value_for_keys: []
domains:
  test-domain:
    include_entry_value_for_keys:
      - subkey1
`)
	descriptorKeyConfig, err := config.ParseDescriptorKeyConfig(yaml)
	assert.NoError(err)

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	rlConfig := config.NewRateLimitConfigImpl(
		loadFile("basic_config.yaml"),
		mockstats.NewMockStatManager(statsStore),
		false,
		descriptorKeyConfig,
	)

	rl := rlConfig.GetLimit(
		context.TODO(), "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{
				{Key: "key1", Value: "value1"},
				{Key: "subkey1", Value: "something"},
			},
			Limit: &pb_struct.RateLimitDescriptor_RateLimitOverride{
				RequestsPerUnit: 10, Unit: pb_type.RateLimitUnit_DAY,
			},
		})
	assert.NotNil(rl)
	assert.Equal("test-domain.key1.subkey1_something", rl.FullKey)
}

func TestDescriptorKeyConfigDefault(t *testing.T) {
	cfg, err := config.ParseDescriptorKeyConfig([]byte(`
default:
  include_entry_value_for_keys:
    - foo
`))
	assert.NoError(t, err)
	assert.True(t, cfg.IncludeEntryValueForKey("unknown-domain", "foo"))
	assert.False(t, cfg.IncludeEntryValueForKey("unknown-domain", "bar"))
}

func TestDescriptorKeyConfigMultipleDomains(t *testing.T) {
	assert := assert.New(t)
	yaml := []byte(`
default:
  include_entry_value_for_keys: [default_key]
domains:
  test-domain-1:
    include_entry_value_for_keys:
      - subkey1
  test-domain-2:
    include_entry_value_for_keys:
      - subkey2
  test-domain-3:
    include_entry_value_for_keys:
      - subkey3
`)
	descriptorKeyConfig, err := config.ParseDescriptorKeyConfig(yaml)
	assert.NoError(err)

	files := loadFile("multiple_default.yaml")
	files = append(files, loadFile("multiple_1.yaml")...)
	files = append(files, loadFile("multiple_2.yaml")...)
	files = append(files, loadFile("multiple_3.yaml")...)

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	rlConfig := config.NewRateLimitConfigImpl(
		files,
		mockstats.NewMockStatManager(statsStore),
		false,
		descriptorKeyConfig,
	)

	default_key := "default_key"
	subkey1 := "subkey1"
	subkey2 := "subkey2"
	subkey3 := "subkey3"

	default_value := "default_value"
	value1 := "value1"
	value2 := "value2"
	value3 := "value3"

	rl := rlConfig.GetLimit(
		context.TODO(), "default",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{
				{Key: default_key, Value: default_value},
				{Key: subkey1, Value: value1},
				{Key: subkey2, Value: value2},
				{Key: subkey3, Value: value3},
			},
			Limit: &pb_struct.RateLimitDescriptor_RateLimitOverride{
				RequestsPerUnit: 10, Unit: pb_type.RateLimitUnit_DAY,
			},
		})
	assert.NotNil(rl)
	assert.Equal("default."+default_key+"_"+default_value+"."+subkey1+"."+subkey2+"."+subkey3, rl.FullKey)

	rl = rlConfig.GetLimit(
		context.TODO(), "test-domain-1",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{
				{Key: default_key, Value: default_value},
				{Key: subkey1, Value: value1},
				{Key: subkey2, Value: value2},
				{Key: subkey3, Value: value3},
			},
			Limit: &pb_struct.RateLimitDescriptor_RateLimitOverride{
				RequestsPerUnit: 10, Unit: pb_type.RateLimitUnit_DAY,
			},
		})
	assert.NotNil(rl)
	assert.Equal("test-domain-1."+default_key+"."+subkey1+"_"+value1+"."+subkey2+"."+subkey3, rl.FullKey)

	rl = rlConfig.GetLimit(
		context.TODO(), "test-domain-2",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{
				{Key: default_key, Value: default_value},
				{Key: subkey1, Value: value1},
				{Key: subkey2, Value: value2},
				{Key: subkey3, Value: value3},
			},
			Limit: &pb_struct.RateLimitDescriptor_RateLimitOverride{
				RequestsPerUnit: 10, Unit: pb_type.RateLimitUnit_DAY,
			},
		})
	assert.NotNil(rl)
	assert.Equal("test-domain-2."+default_key+"."+subkey1+"."+subkey2+"_"+value2+"."+subkey3, rl.FullKey)

	rl = rlConfig.GetLimit(
		context.TODO(), "test-domain-3",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{
				{Key: default_key, Value: default_value},
				{Key: subkey1, Value: value1},
				{Key: subkey2, Value: value2},
				{Key: subkey3, Value: value3},
			},
			Limit: &pb_struct.RateLimitDescriptor_RateLimitOverride{
				RequestsPerUnit: 10, Unit: pb_type.RateLimitUnit_DAY,
			},
		})
	assert.NotNil(rl)
	assert.Equal("test-domain-3."+default_key+"."+subkey1+"."+subkey2+"."+subkey3+"_"+value3, rl.FullKey)
}

func TestDescriptorKeyConfigMultipleEntries(t *testing.T) {
	assert := assert.New(t)
	yaml := []byte(`
default:
  include_entry_value_for_keys: []
domains:
  test-domain:
    include_entry_value_for_keys:
      - subkey1
      - subkey2
      - subkey3
`)
	descriptorKeyConfig, err := config.ParseDescriptorKeyConfig(yaml)
	assert.NoError(err)

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	rlConfig := config.NewRateLimitConfigImpl(
		loadFile("basic_config.yaml"),
		mockstats.NewMockStatManager(statsStore),
		false,
		descriptorKeyConfig,
	)

	subkey1 := "subkey1"
	subkey2 := "subkey2"
	subkey4 := "subkey4"

	value1 := "value1"
	value2 := "value2"
	value4 := "value4"

	rl := rlConfig.GetLimit(
		context.TODO(), "test-domain",
		&pb_struct.RateLimitDescriptor{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{
				{Key: subkey1, Value: value1},
				{Key: subkey4, Value: value4},
				{Key: subkey2, Value: value2},
			},
			Limit: &pb_struct.RateLimitDescriptor_RateLimitOverride{
				RequestsPerUnit: 10, Unit: pb_type.RateLimitUnit_DAY,
			},
		})
	assert.NotNil(rl)
	assert.Equal("test-domain."+subkey1+"_"+value1+"."+subkey4+"."+subkey2+"_"+value2, rl.FullKey)
}

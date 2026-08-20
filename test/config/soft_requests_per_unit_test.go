package config_test

import (
	"testing"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"

	"github.com/envoyproxy/ratelimit/src/config"
	mockstats "github.com/envoyproxy/ratelimit/test/mocks/stats"
)

// soft_requests_per_unit must survive real YAML parsing. Building a RateLimit
// struct directly does not exercise validateYamlKeys, which keeps its own
// allowlist of permitted keys — a field added to the struct but not to that
// list parses fine in unit tests and then panics on a live config load.
func TestSoftRequestsPerUnitParsesFromYaml(t *testing.T) {
	assert := assert.New(t)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	sm := mockstats.NewMockStatManager(statsStore)

	rlConfig := config.NewRateLimitConfigImpl([]config.RateLimitConfigToLoad{
		{
			Name: "soft.yaml",
			ConfigYaml: config.ConfigFileContentToYaml("soft.yaml", `
domain: soft_test
descriptors:
  - key: subject
    rate_limit:
      unit: minute
      requests_per_unit: 20
      soft_requests_per_unit: 14
  - key: plain
    rate_limit:
      unit: minute
      requests_per_unit: 30
`),
		},
	}, sm, true)

	withSoft := rlConfig.GetLimit(context.Background(), "soft_test",
		&pb_struct.RateLimitDescriptor{Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "subject", Value: "a"}}})
	assert.NotNil(withSoft)
	assert.Equal(uint32(14), withSoft.SoftRequestsPerUnit)
	assert.Equal(uint32(20), withSoft.Limit.RequestsPerUnit)
	assert.Equal(pb.RateLimitResponse_RateLimit_MINUTE, withSoft.Limit.Unit)

	// A descriptor that omits it defaults to 0 (feature off), matching the three
	// depop-routing limiters that pass nil for soft_count.
	withoutSoft := rlConfig.GetLimit(context.Background(), "soft_test",
		&pb_struct.RateLimitDescriptor{Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "plain", Value: "b"}}})
	assert.NotNil(withoutSoft)
	assert.Equal(uint32(0), withoutSoft.SoftRequestsPerUnit)
}

// A soft threshold at or above the hard limit is meaningless and must be rejected
// at load time rather than silently never firing.
func TestSoftRequestsPerUnitAboveHardLimitRejected(t *testing.T) {
	assert := assert.New(t)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	sm := mockstats.NewMockStatManager(statsStore)

	assert.Panics(func() {
		config.NewRateLimitConfigImpl([]config.RateLimitConfigToLoad{
			{
				Name: "bad.yaml",
				ConfigYaml: config.ConfigFileContentToYaml("bad.yaml", `
domain: soft_bad
descriptors:
  - key: subject
    rate_limit:
      unit: minute
      requests_per_unit: 20
      soft_requests_per_unit: 20
`),
			},
		}, sm, true)
	})
}

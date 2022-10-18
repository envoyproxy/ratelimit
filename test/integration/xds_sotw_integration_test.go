//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"

	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/test/common"

	rls_config "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
)

func testXdsProviderBasicConfig(perSecond bool, local_cache_size int) func(*testing.T) {
	s := makeSimpleRedisSettings(6383, 6380, perSecond, local_cache_size)
	configXdsProvider(&s)

	return testBasicBaseConfig(s)
}

func configXdsProvider(s *settings.Settings) {
	s.ConfigType = "GRPC_XDS_SOTW"
	s.ConfigGrpcXdsNodeId = "init-test-node"
	s.ConfigGrpcXdsServerUrl = "localhost:18000"
}

func startXdsSotwServer(t *testing.T) (cache.SnapshotCache, context.CancelFunc) {
	conf := &common.XdsServerConfig{Port: 18000, NodeId: "init-test-node"}
	return common.StartXdsSotwServer(t, conf, initialXdsBasicConfig())
}

func initialXdsBasicConfig() *cache.Snapshot {
	intSnapshot, _ := cache.NewSnapshot("1",
		map[resource.Type][]types.Resource{
			resource.RateLimitConfigType: {
				&rls_config.RateLimitConfig{
					Domain: "basic",
					Descriptors: []*rls_config.RateLimitDescriptor{
						{
							Key: "key1",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            "second",
								RequestsPerUnit: 50,
							},
						},
						{
							Key: "key1_local",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            "second",
								RequestsPerUnit: 50,
							},
						},
						{
							Key: "one_per_minute",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            "minute",
								RequestsPerUnit: 1,
							},
						},
					},
				},
				&rls_config.RateLimitConfig{
					Domain: "another",
					Descriptors: []*rls_config.RateLimitDescriptor{
						{
							Key: "key2",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            "minute",
								RequestsPerUnit: 20,
							},
						},
						{
							Key: "key3",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            "hour",
								RequestsPerUnit: 10,
							},
						},
						{
							Key: "key2_local",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            "minute",
								RequestsPerUnit: 20,
							},
						},
						{
							Key: "key3_local",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            "hour",
								RequestsPerUnit: 10,
							},
						},
						{
							Key: "key4",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            "day",
								RequestsPerUnit: 20,
							},
						},
						{
							Key: "key4_local",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            "day",
								RequestsPerUnit: 20,
							},
						},
					},
				},
			},
		},
	)
	return intSnapshot
}

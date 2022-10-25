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

func testXdsProviderBasicConfigReload(setSnapshotFunc common.SetSnapshotFunc, perSecond bool, local_cache_size int) func(*testing.T) {
	s := makeSimpleRedisSettings(6383, 6380, perSecond, local_cache_size)
	configXdsProvider(&s)
	return testConfigReload(s, newConfigWithXdsConfigProvider(setSnapshotFunc), restoreConfigWithXdsConfigProvider(setSnapshotFunc))
}

func configXdsProvider(s *settings.Settings) {
	s.ConfigType = "GRPC_XDS_SOTW"
	s.ConfigGrpcXdsNodeId = "init-test-node"
	s.ConfigGrpcXdsServerUrl = "localhost:18000"
}

func startXdsSotwServer(t *testing.T) (common.SetSnapshotFunc, context.CancelFunc) {
	conf := &common.XdsServerConfig{Port: 18000, NodeId: "init-test-node"}
	intSnapshot, err := cache.NewSnapshot("1", initialXdsBasicConfig())
	if err != nil {
		panic(err)
	}
	return common.StartXdsSotwServer(t, conf, intSnapshot)
}

func initialXdsBasicConfig() map[resource.Type][]types.Resource {
	return map[resource.Type][]types.Resource{
		resource.RateLimitConfigType: {
			&rls_config.RateLimitConfig{
				Name:   "basic",
				Domain: "basic",
				Descriptors: []*rls_config.RateLimitDescriptor{
					{
						Key: "key1",
						RateLimit: &rls_config.RateLimitPolicy{
							Unit:            rls_config.RateLimitUnit_SECOND,
							RequestsPerUnit: 50,
						},
					},
					{
						Key: "key1_local",
						RateLimit: &rls_config.RateLimitPolicy{
							Unit:            rls_config.RateLimitUnit_SECOND,
							RequestsPerUnit: 50,
						},
					},
					{
						Key: "one_per_minute",
						RateLimit: &rls_config.RateLimitPolicy{
							Unit:            rls_config.RateLimitUnit_MINUTE,
							RequestsPerUnit: 1,
						},
					},
				},
			},
			&rls_config.RateLimitConfig{
				Name:   "another",
				Domain: "another",
				Descriptors: []*rls_config.RateLimitDescriptor{
					{
						Key: "key2",
						RateLimit: &rls_config.RateLimitPolicy{
							Unit:            rls_config.RateLimitUnit_MINUTE,
							RequestsPerUnit: 20,
						},
					},
					{
						Key: "key3",
						RateLimit: &rls_config.RateLimitPolicy{
							Unit:            rls_config.RateLimitUnit_HOUR,
							RequestsPerUnit: 10,
						},
					},
					{
						Key: "key2_local",
						RateLimit: &rls_config.RateLimitPolicy{
							Unit:            rls_config.RateLimitUnit_MINUTE,
							RequestsPerUnit: 20,
						},
					},
					{
						Key: "key3_local",
						RateLimit: &rls_config.RateLimitPolicy{
							Unit:            rls_config.RateLimitUnit_HOUR,
							RequestsPerUnit: 10,
						},
					},
					{
						Key: "key4",
						RateLimit: &rls_config.RateLimitPolicy{
							Unit:            rls_config.RateLimitUnit_DAY,
							RequestsPerUnit: 20,
						},
					},
					{
						Key: "key4_local",
						RateLimit: &rls_config.RateLimitPolicy{
							Unit:            rls_config.RateLimitUnit_DAY,
							RequestsPerUnit: 20,
						},
					},
				},
			},
		},
	}
}

func newConfigWithXdsConfigProvider(setSnapshotFunc common.SetSnapshotFunc) func() {
	initConfig := initialXdsBasicConfig()
	rlsConf := initConfig[resource.RateLimitConfigType]
	newRlsConf := append(rlsConf, &rls_config.RateLimitConfig{
		Name:   "reload",
		Domain: "reload",
		Descriptors: []*rls_config.RateLimitDescriptor{
			{
				Key: "key1",
				RateLimit: &rls_config.RateLimitPolicy{
					Unit:            rls_config.RateLimitUnit_SECOND,
					RequestsPerUnit: 50,
				},
			},
			{
				Key: "block",
				RateLimit: &rls_config.RateLimitPolicy{
					Unit:            rls_config.RateLimitUnit_SECOND,
					RequestsPerUnit: 0,
				},
			},
			{
				Key: "one_per_minute",
				RateLimit: &rls_config.RateLimitPolicy{
					Unit:            rls_config.RateLimitUnit_MINUTE,
					RequestsPerUnit: 1,
				},
			},
		},
	})

	newConfig := map[resource.Type][]types.Resource{
		resource.RateLimitConfigType: newRlsConf,
	}
	newSnapshot, err := cache.NewSnapshot("2", newConfig)
	if err != nil {
		panic(err)
	}

	return func() {
		setSnapshotFunc(newSnapshot)
	}
}

func restoreConfigWithXdsConfigProvider(setSnapshotFunc common.SetSnapshotFunc) func() {
	newSnapshot, err := cache.NewSnapshot("3", initialXdsBasicConfig())
	if err != nil {
		panic(err)
	}

	return func() {
		setSnapshotFunc(newSnapshot)
	}
}

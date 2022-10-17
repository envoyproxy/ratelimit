package provider_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	gostats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"

	"github.com/envoyproxy/ratelimit/src/provider"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/test/common"
	"github.com/envoyproxy/ratelimit/test/mocks/stats"

	rls_config "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
	// ratelimitservice "github.com/envoyproxy/go-control-plane/ratelimit/service/ratelimit/v3"
)

const (
	xdsNodeId = "test-node"
	xdsPort   = 18001
)

func TestXdsProvider(t *testing.T) {
	intSnapshot, _ := cache.NewSnapshot("1",
		map[resource.Type][]types.Resource{
			resource.RateLimitConfigType: {
				&rls_config.RateLimitConfig{
					Domain: "foo",
					Descriptors: []*rls_config.RateLimitDescriptor{
						{
							Key:   "k1",
							Value: "v1",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            "minute",
								RequestsPerUnit: 3,
							},
						},
					},
				},
			},
		},
	)
	snapCache, cancel := common.StartXdsSotwServer(t, &common.XdsServerConfig{Port: xdsPort, NodeId: xdsNodeId}, intSnapshot)
	defer cancel()

	s := settings.Settings{
		ConfigType:             "GRPC_XDS_SOTW",
		ConfigGrpcXdsNodeId:    xdsNodeId,
		ConfigGrpcXdsServerUrl: fmt.Sprintf("localhost:%d", xdsPort),
	}

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	statsManager := stats.NewMockStatManager(statsStore)
	p := provider.NewXdsGrpcSotwProvider(s, statsManager)
	defer p.Stop()
	providerEventChan := p.ConfigUpdateEvent()

	t.Run("Test initial xDS config", testInitialXdsConfig(snapCache, providerEventChan))
	t.Run("Test new (after initial) xDS config update", testNewXdsConfigUpdate(snapCache, providerEventChan))
	t.Run("Test multi domain xDS config update", testMultiDomainXdsConfigUpdate(snapCache, providerEventChan))
}

func testInitialXdsConfig(snapCache cache.SnapshotCache, providerEventChan <-chan provider.ConfigUpdateEvent) func(t *testing.T) {
	return func(t *testing.T) {
		assert := assert.New(t)

		configEvent := <-providerEventChan
		assert.NotNil(configEvent)

		config, err := configEvent.GetConfig()
		assert.Nil(err)
		assert.Equal("foo.k1_v1: unit=MINUTE requests_per_unit=3, shadow_mode: false\n", config.Dump())
	}
}

func testNewXdsConfigUpdate(snapCache cache.SnapshotCache, providerEventChan <-chan provider.ConfigUpdateEvent) func(t *testing.T) {
	return func(t *testing.T) {
		assert := assert.New(t)

		snapshot, _ := cache.NewSnapshot("2",
			map[resource.Type][]types.Resource{
				resource.RateLimitConfigType: {
					&rls_config.RateLimitConfig{
						Domain: "foo",
						Descriptors: []*rls_config.RateLimitDescriptor{
							{
								Key:   "k2",
								Value: "v2",
								RateLimit: &rls_config.RateLimitPolicy{
									Unit:            "minute",
									RequestsPerUnit: 5,
								},
							},
						},
					},
				},
			},
		)
		setSnapshot(t, snapCache, snapshot)

		configEvent := <-providerEventChan
		assert.NotNil(configEvent)

		config, err := configEvent.GetConfig()
		assert.Nil(err)
		assert.Equal("foo.k2_v2: unit=MINUTE requests_per_unit=5, shadow_mode: false\n", config.Dump())
	}
}

func testMultiDomainXdsConfigUpdate(snapCache cache.SnapshotCache, providerEventChan <-chan provider.ConfigUpdateEvent) func(t *testing.T) {
	return func(t *testing.T) {
		assert := assert.New(t)

		snapshot, _ := cache.NewSnapshot("3",
			map[resource.Type][]types.Resource{
				resource.RateLimitConfigType: {
					&rls_config.RateLimitConfig{
						Domain: "foo",
						Descriptors: []*rls_config.RateLimitDescriptor{
							{
								Key:   "k1",
								Value: "k2",
								RateLimit: &rls_config.RateLimitPolicy{
									Unit:            "minute",
									RequestsPerUnit: 10,
								},
							},
						},
					},
					&rls_config.RateLimitConfig{
						Domain: "bar",
						Descriptors: []*rls_config.RateLimitDescriptor{
							{
								Key:   "k1",
								Value: "k2",
								RateLimit: &rls_config.RateLimitPolicy{
									Unit:            "minute",
									RequestsPerUnit: 100,
								},
							},
						},
					},
				},
			},
		)
		setSnapshot(t, snapCache, snapshot)

		configEvent := <-providerEventChan
		assert.NotNil(configEvent)

		config, err := configEvent.GetConfig()
		assert.Nil(err)
		assert.Equal([]string{
			"foo.k1_k2: unit=MINUTE requests_per_unit=10, shadow_mode: false",
			"bar.k1_k2: unit=MINUTE requests_per_unit=100, shadow_mode: false",
		}, strings.Split(strings.TrimSuffix(config.Dump(), "\n"), "\n"))
	}
}

func setSnapshot(t *testing.T, snapCache cache.SnapshotCache, snapshot *cache.Snapshot) {
	t.Helper()
	if err := snapshot.Consistent(); err != nil {
		t.Errorf("snapshot inconsistency: %+v\n%+v", snapshot, err)
	}
	if err := snapCache.SetSnapshot(context.Background(), "test-node", snapshot); err != nil {
		t.Errorf("snapshot error %q for %+v", err, snapshot)
	}
}

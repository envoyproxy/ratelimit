package provider_test

import (
	"fmt"
	"os"
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
					Name:   "foo",
					Domain: "foo",
					Descriptors: []*rls_config.RateLimitDescriptor{
						{
							Key:   "k1",
							Value: "v1",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            rls_config.RateLimitUnit_MINUTE,
								RequestsPerUnit: 3,
							},
						},
					},
				},
			},
		},
	)
	setSnapshotFunc, cancel := common.StartXdsSotwServer(t, &common.XdsServerConfig{Port: xdsPort, NodeId: xdsNodeId}, intSnapshot)
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

	snapVersion := 1
	t.Run("Test initial xDS config", testInitialXdsConfig(&snapVersion, setSnapshotFunc, providerEventChan))
	t.Run("Test new (after initial) xDS config update", testNewXdsConfigUpdate(&snapVersion, setSnapshotFunc, providerEventChan))
	t.Run("Test multi domain xDS config update", testMultiDomainXdsConfigUpdate(&snapVersion, setSnapshotFunc, providerEventChan))
	t.Run("Test limits with deeper xDS config update", testDeeperLimitsXdsConfigUpdate(&snapVersion, setSnapshotFunc, providerEventChan))

	err := os.Setenv("MERGE_DOMAIN_CONFIG", "true")
	defer os.Unsetenv("MERGE_DOMAIN_CONFIG")
	if err != nil {
		t.Error("Error setting 'MERGE_DOMAIN_CONFIG' environment variable", err)
	}
	t.Run("Test same domain multiple times xDS config update", testSameDomainMultipleXdsConfigUpdate(setSnapshotFunc, providerEventChan))
}

func testInitialXdsConfig(snapVersion *int, setSnapshotFunc common.SetSnapshotFunc, providerEventChan <-chan provider.ConfigUpdateEvent) func(t *testing.T) {
	*snapVersion += 1
	return func(t *testing.T) {
		assert := assert.New(t)

		configEvent := <-providerEventChan
		assert.NotNil(configEvent)

		config, err := configEvent.GetConfig()
		assert.Nil(err)
		assert.Equal("foo.k1_v1: unit=MINUTE requests_per_unit=3, shadow_mode: false\n", config.Dump())
	}
}

func testNewXdsConfigUpdate(snapVersion *int, setSnapshotFunc common.SetSnapshotFunc, providerEventChan <-chan provider.ConfigUpdateEvent) func(t *testing.T) {
	*snapVersion += 1
	return func(t *testing.T) {
		assert := assert.New(t)

		snapshot, _ := cache.NewSnapshot(fmt.Sprint(*snapVersion),
			map[resource.Type][]types.Resource{
				resource.RateLimitConfigType: {
					&rls_config.RateLimitConfig{
						Name:   "foo",
						Domain: "foo",
						Descriptors: []*rls_config.RateLimitDescriptor{
							{
								Key:   "k2",
								Value: "v2",
								RateLimit: &rls_config.RateLimitPolicy{
									Unit:            rls_config.RateLimitUnit_MINUTE,
									RequestsPerUnit: 5,
								},
							},
						},
					},
				},
			},
		)
		setSnapshotFunc(snapshot)

		configEvent := <-providerEventChan
		assert.NotNil(configEvent)

		config, err := configEvent.GetConfig()
		assert.Nil(err)
		assert.Equal("foo.k2_v2: unit=MINUTE requests_per_unit=5, shadow_mode: false\n", config.Dump())
	}
}

func testMultiDomainXdsConfigUpdate(snapVersion *int, setSnapshotFunc common.SetSnapshotFunc, providerEventChan <-chan provider.ConfigUpdateEvent) func(t *testing.T) {
	*snapVersion += 1
	return func(t *testing.T) {
		assert := assert.New(t)

		snapshot, _ := cache.NewSnapshot(fmt.Sprint(*snapVersion),
			map[resource.Type][]types.Resource{
				resource.RateLimitConfigType: {
					&rls_config.RateLimitConfig{
						Name:   "foo",
						Domain: "foo",
						Descriptors: []*rls_config.RateLimitDescriptor{
							{
								Key:   "k1",
								Value: "v1",
								RateLimit: &rls_config.RateLimitPolicy{
									Unit:            rls_config.RateLimitUnit_MINUTE,
									RequestsPerUnit: 10,
								},
							},
						},
					},
					&rls_config.RateLimitConfig{
						Name:   "bar",
						Domain: "bar",
						Descriptors: []*rls_config.RateLimitDescriptor{
							{
								Key:   "k1",
								Value: "v1",
								RateLimit: &rls_config.RateLimitPolicy{
									Unit:            rls_config.RateLimitUnit_MINUTE,
									RequestsPerUnit: 100,
								},
							},
						},
					},
				},
			},
		)
		setSnapshotFunc(snapshot)

		configEvent := <-providerEventChan
		assert.NotNil(configEvent)

		config, err := configEvent.GetConfig()
		assert.Nil(err)
		assert.ElementsMatch([]string{
			"foo.k1_v1: unit=MINUTE requests_per_unit=10, shadow_mode: false",
			"bar.k1_v1: unit=MINUTE requests_per_unit=100, shadow_mode: false",
		}, strings.Split(strings.TrimSuffix(config.Dump(), "\n"), "\n"))
	}
}

func testDeeperLimitsXdsConfigUpdate(snapVersion *int, setSnapshotFunc common.SetSnapshotFunc, providerEventChan <-chan provider.ConfigUpdateEvent) func(t *testing.T) {
	*snapVersion += 1
	return func(t *testing.T) {
		assert := assert.New(t)

		snapshot, _ := cache.NewSnapshot(fmt.Sprint(*snapVersion),
			map[resource.Type][]types.Resource{
				resource.RateLimitConfigType: {
					&rls_config.RateLimitConfig{
						Name:   "foo",
						Domain: "foo",
						Descriptors: []*rls_config.RateLimitDescriptor{
							{
								Key:   "k1",
								Value: "v1",
								RateLimit: &rls_config.RateLimitPolicy{
									Unit:            rls_config.RateLimitUnit_MINUTE,
									RequestsPerUnit: 10,
								},
								Descriptors: []*rls_config.RateLimitDescriptor{
									{
										Key: "k2",
										RateLimit: &rls_config.RateLimitPolicy{
											Unlimited: true,
										},
									},
									{
										Key:   "k2",
										Value: "v2",
										RateLimit: &rls_config.RateLimitPolicy{
											Unit:            rls_config.RateLimitUnit_HOUR,
											RequestsPerUnit: 15,
										},
									},
								},
							},
							{
								Key:   "j1",
								Value: "v2",
								RateLimit: &rls_config.RateLimitPolicy{
									Unlimited: true,
								},
								Descriptors: []*rls_config.RateLimitDescriptor{
									{
										Key: "j2",
										RateLimit: &rls_config.RateLimitPolicy{
											Unlimited: true,
										},
									},
									{
										Key:   "j2",
										Value: "v2",
										RateLimit: &rls_config.RateLimitPolicy{
											Unit:            rls_config.RateLimitUnit_DAY,
											RequestsPerUnit: 15,
										},
										ShadowMode: true,
									},
								},
							},
						},
					},
					&rls_config.RateLimitConfig{
						Name:   "bar",
						Domain: "bar",
						Descriptors: []*rls_config.RateLimitDescriptor{
							{
								Key:   "k1",
								Value: "v1",
								RateLimit: &rls_config.RateLimitPolicy{
									Unit:            rls_config.RateLimitUnit_MINUTE,
									RequestsPerUnit: 100,
								},
							},
						},
					},
				},
			},
		)
		setSnapshotFunc(snapshot)

		configEvent := <-providerEventChan
		assert.NotNil(configEvent)

		config, err := configEvent.GetConfig()
		assert.Nil(err)
		assert.ElementsMatch([]string{
			"foo.k1_v1: unit=MINUTE requests_per_unit=10, shadow_mode: false",
			"foo.k1_v1.k2: unit=UNKNOWN requests_per_unit=0, shadow_mode: false",
			"foo.k1_v1.k2_v2: unit=HOUR requests_per_unit=15, shadow_mode: false",
			"foo.j1_v2: unit=UNKNOWN requests_per_unit=0, shadow_mode: false",
			"foo.j1_v2.j2: unit=UNKNOWN requests_per_unit=0, shadow_mode: false",
			"foo.j1_v2.j2_v2: unit=DAY requests_per_unit=15, shadow_mode: true",
			"bar.k1_v1: unit=MINUTE requests_per_unit=100, shadow_mode: false",
		}, strings.Split(strings.TrimSuffix(config.Dump(), "\n"), "\n"))
	}
}

func testSameDomainMultipleXdsConfigUpdate(setSnapshotFunc common.SetSnapshotFunc, providerEventChan <-chan provider.ConfigUpdateEvent) func(t *testing.T) {
	return func(t *testing.T) {
		assert := assert.New(t)

		snapshot, _ := cache.NewSnapshot("3",
			map[resource.Type][]types.Resource{
				resource.RateLimitConfigType: {
					&rls_config.RateLimitConfig{
						Name:   "foo-1",
						Domain: "foo",
						Descriptors: []*rls_config.RateLimitDescriptor{
							{
								Key:   "k1",
								Value: "v1",
								RateLimit: &rls_config.RateLimitPolicy{
									Unit:            rls_config.RateLimitUnit_MINUTE,
									RequestsPerUnit: 10,
								},
							},
						},
					},
					&rls_config.RateLimitConfig{
						Name:   "foo-2",
						Domain: "foo",
						Descriptors: []*rls_config.RateLimitDescriptor{
							{
								Key:   "k1",
								Value: "v2",
								RateLimit: &rls_config.RateLimitPolicy{
									Unit:            rls_config.RateLimitUnit_MINUTE,
									RequestsPerUnit: 100,
								},
							},
						},
					},
				},
			},
		)
		setSnapshotFunc(snapshot)

		configEvent := <-providerEventChan
		assert.NotNil(configEvent)

		config, err := configEvent.GetConfig()
		assert.Nil(err)
		assert.ElementsMatch([]string{
			"foo.k1_v2: unit=MINUTE requests_per_unit=100, shadow_mode: false",
			"foo.k1_v1: unit=MINUTE requests_per_unit=10, shadow_mode: false",
		}, strings.Split(strings.TrimSuffix(config.Dump(), "\n"), "\n"))
	}
}

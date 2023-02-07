package example

import (
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	rls_config "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
)

func makeRlsConfig() []types.Resource {
	return []types.Resource{
		&rls_config.RateLimitConfig{
			Name:   "mongo_cps",
			Domain: "mongo_cps",
			Descriptors: []*rls_config.RateLimitDescriptor{
				{
					Key:   "database",
					Value: "users",
					RateLimit: &rls_config.RateLimitPolicy{
						Unit:            rls_config.RateLimitUnit_SECOND,
						RequestsPerUnit: 500,
					},
				},
				{
					Key:   "database",
					Value: "default",
					RateLimit: &rls_config.RateLimitPolicy{
						Unit:            rls_config.RateLimitUnit_SECOND,
						RequestsPerUnit: 500,
					},
				},
			},
		},
		&rls_config.RateLimitConfig{
			Name:   "rl",
			Domain: "rl",
			Descriptors: []*rls_config.RateLimitDescriptor{
				{
					Key:   "category",
					Value: "account",
					RateLimit: &rls_config.RateLimitPolicy{
						Replaces:        []*rls_config.RateLimitReplace{{Name: "bkthomps"}, {Name: "fake_name"}},
						Unit:            rls_config.RateLimitUnit_MINUTE,
						RequestsPerUnit: 4,
					},
				},
				{
					Key:   "source_cluster",
					Value: "proxy",
					Descriptors: []*rls_config.RateLimitDescriptor{
						{
							Key:   "destination_cluster",
							Value: "bkthomps",
							RateLimit: &rls_config.RateLimitPolicy{
								Replaces:        []*rls_config.RateLimitReplace{{Name: "bkthomps"}},
								Unit:            rls_config.RateLimitUnit_MINUTE,
								RequestsPerUnit: 2,
							},
						},
						{
							Key:   "destination_cluster",
							Value: "mock",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            rls_config.RateLimitUnit_MINUTE,
								RequestsPerUnit: 1,
							},
						},
						{
							Key:   "destination_cluster",
							Value: "override",
							RateLimit: &rls_config.RateLimitPolicy{
								Replaces:        []*rls_config.RateLimitReplace{{Name: "banned_limit"}},
								Unit:            rls_config.RateLimitUnit_MINUTE,
								RequestsPerUnit: 2,
							},
						},
						{
							Key:   "destination_cluster",
							Value: "fake",
							RateLimit: &rls_config.RateLimitPolicy{
								Name:            "fake_name",
								Unit:            rls_config.RateLimitUnit_MINUTE,
								RequestsPerUnit: 2,
							},
						},
					},
				},
				{
					Key: "foo",
					RateLimit: &rls_config.RateLimitPolicy{
						Unit:            rls_config.RateLimitUnit_MINUTE,
						RequestsPerUnit: 2,
					},
					Descriptors: []*rls_config.RateLimitDescriptor{
						{
							Key: "bar",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            rls_config.RateLimitUnit_MINUTE,
								RequestsPerUnit: 3,
							},
						},
						{
							Key:   "bar",
							Value: "bkthomps",
							RateLimit: &rls_config.RateLimitPolicy{
								Name:            "bkthomps",
								Unit:            rls_config.RateLimitUnit_MINUTE,
								RequestsPerUnit: 1,
							},
						},
						{
							Key:   "bar",
							Value: "banned",
							RateLimit: &rls_config.RateLimitPolicy{
								Name:            "banned_limit",
								Unit:            rls_config.RateLimitUnit_MINUTE,
								RequestsPerUnit: 0,
							},
						},
						{
							Key: "baz",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            rls_config.RateLimitUnit_SECOND,
								RequestsPerUnit: 1,
							},
						},
						{
							Key:   "baz",
							Value: "not-so-shady",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            rls_config.RateLimitUnit_MINUTE,
								RequestsPerUnit: 3,
							},
						},
						{
							Key:   "baz",
							Value: "shady",
							RateLimit: &rls_config.RateLimitPolicy{
								Unit:            rls_config.RateLimitUnit_MINUTE,
								RequestsPerUnit: 3,
							},
							ShadowMode: true,
						},
						{
							Key: "bay",
							RateLimit: &rls_config.RateLimitPolicy{
								Unlimited: true,
							},
						},
					},
				},
				{
					Key: "qux",
					RateLimit: &rls_config.RateLimitPolicy{
						Unlimited: true,
					},
				},
			},
		},
	}
}

func GenerateSnapshot() *cache.Snapshot {
	snap, _ := cache.NewSnapshot("1",
		map[resource.Type][]types.Resource{
			resource.RateLimitConfigType: makeRlsConfig(),
		},
	)
	return snap
}

package mocks

//go:generate go run github.com/golang/mock/mockgen -destination ./runtime/snapshot/snapshot.go github.com/lyft/goruntime/snapshot IFace
//go:generate go run github.com/golang/mock/mockgen -destination ./runtime/loader/loader.go github.com/lyft/goruntime/loader IFace
//go:generate go run github.com/golang/mock/mockgen -destination ./config/config.go github.com/zackzhangverkada/ratelimit/src/config RateLimitConfig,RateLimitConfigLoader
//go:generate go run github.com/golang/mock/mockgen -destination ./redis/redis.go github.com/zackzhangverkada/ratelimit/src/redis Client
//go:generate go run github.com/golang/mock/mockgen -destination ./limiter/limiter.go github.com/zackzhangverkada/ratelimit/src/limiter RateLimitCache
//go:generate go run github.com/golang/mock/mockgen -destination ./utils/utils.go github.com/zackzhangverkada/ratelimit/src/utils TimeSource,JitterRandSource
//go:generate go run github.com/golang/mock/mockgen -destination ./memcached/client.go github.com/zackzhangverkada/ratelimit/src/memcached Client
//go:generate go run github.com/golang/mock/mockgen -destination ./rls/rls.go github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3 RateLimitServiceServer
//go:generate go run github.com/golang/mock/mockgen -destination ./srv/srv.go github.com/zackzhangverkada/ratelimit/src/srv SrvResolver

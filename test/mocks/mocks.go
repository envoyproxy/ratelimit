package mocks

//go:generate go run github.com/golang/mock/mockgen -destination ./runtime/snapshot/snapshot.go github.com/lyft/goruntime/snapshot IFace
//go:generate go run github.com/golang/mock/mockgen -destination ./runtime/loader/loader.go github.com/lyft/goruntime/loader IFace
//go:generate go run github.com/golang/mock/mockgen -destination ./config/config.go github.com/envoyproxy/ratelimit/src/config RateLimitConfig,RateLimitConfigLoader
//go:generate go run github.com/golang/mock/mockgen -destination ./redis/redis.go github.com/envoyproxy/ratelimit/src/redis Client
//go:generate go run github.com/golang/mock/mockgen -destination ./limiter/limiter.go github.com/envoyproxy/ratelimit/src/limiter RateLimitCache,TimeSource,JitterRandSource

package redis_test

import (
	"context"
	"math/rand"
	"runtime"
	"testing"
	"time"

	"github.com/envoyproxy/ratelimit/test/mocks/stats"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	gostats "github.com/lyft/gostats"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/utils"

	"github.com/envoyproxy/ratelimit/test/common"
)

func BenchmarkParallelDoLimit(b *testing.B) {
	b.Skip("Skip benchmark")

	b.ReportAllocs()

	// See https://github.com/mediocregopher/radix/blob/v3.5.1/bench/bench_test.go#L176
	parallel := runtime.GOMAXPROCS(0)
	poolSize := parallel * runtime.GOMAXPROCS(0)

	do := func(b *testing.B, fn func() error) {
		b.ResetTimer()
		b.SetParallelism(parallel)
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if err := fn(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}

	mkDoLimitBench := func(implicitPipeline bool) func(*testing.B) {
		return func(b *testing.B) {
			statsStore := gostats.NewStore(gostats.NewNullSink(), false)
			sm := stats.NewMockStatManager(statsStore)
			client := redis.NewClientImpl(context.Background(), statsStore, false, "", "tcp", "single", "127.0.0.1:6379", poolSize, implicitPipeline, nil, false, nil)
			defer client.Close()

			cache := redis.NewFixedRateLimitCacheImpl(client, nil, utils.NewTimeSourceImpl(), rand.New(utils.NewLockedSource(time.Now().Unix())), 10, nil, 0.8, "", sm, true)
			request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
			limits := []*config.RateLimit{config.NewRateLimit(1000000000, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key_value"), false, false, "", nil, false)}

			// wait for the pool to fill up
			for {
				time.Sleep(50 * time.Millisecond)
				if client.NumActiveConns() >= poolSize {
					break
				}
			}

			b.ResetTimer()

			do(b, func() error {
				cache.DoLimit(context.Background(), request, limits)
				return nil
			})
		}
	}

	b.Run("no pipeline", mkDoLimitBench(false))
	b.Run("pipeline enabled", mkDoLimitBench(true))
}

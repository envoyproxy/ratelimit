package factory_test

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis"
	"github.com/envoyproxy/ratelimit/src/storage/factory"
	"github.com/envoyproxy/ratelimit/src/storage/strategy"
	"github.com/stretchr/testify/assert"

	stats "github.com/lyft/gostats"
)

func mustNewRedisServer() *miniredis.Miniredis {
	srv, err := miniredis.Run()
	if err != nil {
		panic(err)
	}

	return srv
}

func expectPanicError(t *testing.T, f assert.PanicTestFunc) (result error) {
	t.Helper()
	defer func() {
		panicResult := recover()
		assert.NotNil(t, panicResult, "Expected a panic")
		result = panicResult.(error)
	}()
	f()
	return
}

func TestNewRedisClient(t *testing.T) {
	t.Run("ImplicitPipeliningEnabled", testNewRedisClient(t, 2*time.Millisecond, 2))
	t.Run("ImplicitPipeliningDisabled", testNewRedisClient(t, 0, 0))
}

func testNewRedisClient(t *testing.T, pipelineWindow time.Duration, pipelineLimit int) func(t *testing.T) {
	return func(t *testing.T) {
		redisAuth := "123"
		statsStore := stats.NewStore(stats.NewNullSink(), false)

		mkRedisClient := func(auth, addr string) strategy.StorageStrategy {
			return factory.NewRedis(statsStore, false, auth, "single", addr, 1, pipelineWindow, pipelineLimit)
		}

		t.Run("connection refused", func(t *testing.T) {
			// It's possible there is a redis server listening on 6379 in ci environment, so
			// use a random port.
			panicErr := expectPanicError(t, func() { mkRedisClient("", "localhost:12345") })
			assert.Contains(t, panicErr.Error(), "connection refused")
		})

		t.Run("ok", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			var client strategy.StorageStrategy
			assert.NotPanics(t, func() {
				client = mkRedisClient("", redisSrv.Addr())
			})
			assert.NotNil(t, client)
		})

		t.Run("auth fail", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			redisSrv.RequireAuth(redisAuth)

			assert.PanicsWithError(t, "NOAUTH Authentication required.", func() {
				mkRedisClient("", redisSrv.Addr())
			})
		})

		t.Run("auth pass", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			redisSrv.RequireAuth(redisAuth)

			assert.NotPanics(t, func() {
				mkRedisClient(redisAuth, redisSrv.Addr())
			})
		})
	}
}

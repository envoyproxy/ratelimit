package redis_test

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
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

func testNewClientImpl(t *testing.T, pipelineWindow time.Duration, pipelineLimit int) func(t *testing.T) {
	return func(t *testing.T) {
		redisAuth := "123"
		statsStore := stats.NewStore(stats.NewNullSink(), false)

		mkRedisClient := func(auth, addr string) redis.Client {
			return redis.NewClientImpl(statsStore, false, auth, "single", addr, 1, pipelineWindow, pipelineLimit)
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

			var client redis.Client
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

		t.Run("ImplicitPipeliningEnabled() return expected value", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			client := mkRedisClient("", redisSrv.Addr())

			if pipelineWindow == 0 && pipelineLimit == 0 {
				assert.False(t, client.ImplicitPipeliningEnabled())
			} else {
				assert.True(t, client.ImplicitPipeliningEnabled())
			}
		})
	}
}

func TestNewClientImpl(t *testing.T) {
	t.Run("ImplicitPipeliningEnabled", testNewClientImpl(t, 2*time.Millisecond, 2))
	t.Run("ImplicitPipeliningDisabled", testNewClientImpl(t, 0, 0))
}

func TestDoCmd(t *testing.T) {
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	mkRedisClient := func(addr string) redis.Client {
		return redis.NewClientImpl(statsStore, false, "", "single", addr, 1, 0, 0)
	}

	t.Run("SETGET ok", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		client := mkRedisClient(redisSrv.Addr())
		var res string

		assert.Nil(t, client.DoCmd(nil, "SET", "foo", "bar"))
		assert.Nil(t, client.DoCmd(&res, "GET", "foo"))
		assert.Equal(t, "bar", res)
	})

	t.Run("INCRBY ok", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		client := mkRedisClient(redisSrv.Addr())
		var res uint32
		hits := uint32(1)

		assert.Nil(t, client.DoCmd(&res, "INCRBY", "a", hits))
		assert.Equal(t, hits, res)
		assert.Nil(t, client.DoCmd(&res, "INCRBY", "a", hits))
		assert.Equal(t, uint32(2), res)
	})

	t.Run("connection broken", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		client := mkRedisClient(redisSrv.Addr())

		assert.Nil(t, client.DoCmd(nil, "SET", "foo", "bar"))

		redisSrv.Close()
		assert.EqualError(t, client.DoCmd(nil, "GET", "foo"), "EOF")
	})
}

func testPipeDo(t *testing.T, pipelineWindow time.Duration, pipelineLimit int) func(t *testing.T) {
	return func(t *testing.T) {
		statsStore := stats.NewStore(stats.NewNullSink(), false)

		mkRedisClient := func(addr string) redis.Client {
			return redis.NewClientImpl(statsStore, false, "", "single", addr, 1, pipelineWindow, pipelineLimit)
		}

		t.Run("SETGET ok", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			client := mkRedisClient(redisSrv.Addr())
			var res string

			pipeline := redis.Pipeline{}
			pipeline = client.PipeAppend(pipeline, nil, "SET", "foo", "bar")
			pipeline = client.PipeAppend(pipeline, &res, "GET", "foo")

			assert.Nil(t, client.PipeDo(pipeline))
			assert.Equal(t, "bar", res)
		})

		t.Run("INCRBY ok", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			client := mkRedisClient(redisSrv.Addr())
			var res uint32
			hits := uint32(1)

			assert.Nil(t, client.PipeDo(client.PipeAppend(redis.Pipeline{}, &res, "INCRBY", "a", hits)))
			assert.Equal(t, hits, res)

			assert.Nil(t, client.PipeDo(client.PipeAppend(redis.Pipeline{}, &res, "INCRBY", "a", hits)))
			assert.Equal(t, uint32(2), res)
		})

		t.Run("connection broken", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			client := mkRedisClient(redisSrv.Addr())

			assert.Nil(t, nil, client.PipeDo(client.PipeAppend(redis.Pipeline{}, nil, "SET", "foo", "bar")))

			redisSrv.Close()

			expectErrContainEOF := func(t *testing.T, err error) {
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), "EOF")
			}

			expectErrContainEOF(t, client.PipeDo(client.PipeAppend(redis.Pipeline{}, nil, "GET", "foo")))
		})
	}
}

func TestPipeDo(t *testing.T) {
	t.Run("ImplicitPipeliningEnabled", testPipeDo(t, 10*time.Millisecond, 2))
	t.Run("ImplicitPipeliningDisabled", testPipeDo(t, 0, 0))
}

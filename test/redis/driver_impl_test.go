package redis_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/redis"
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
			return redis.NewClientImpl(statsStore, false, auth, "tcp", "single", addr, 1, pipelineWindow, pipelineLimit, nil, false, nil, 10*time.Second, "", 0, "")
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

		t.Run("auth user pass", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			user, pass := "test-user", "test-pass"
			redisSrv.RequireUserAuth(user, pass)

			redisAuth := fmt.Sprintf("%s:%s", user, pass)
			assert.NotPanics(t, func() {
				mkRedisClient(redisAuth, redisSrv.Addr())
			})
		})

		t.Run("auth user pass fail", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			user, pass := "test-user", "test-pass"
			redisSrv.RequireUserAuth(user, pass)

			redisAuth := fmt.Sprintf("%s:invalid-password", user)
			assert.PanicsWithError(t, "WRONGPASS invalid username-password pair", func() {
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
		return redis.NewClientImpl(statsStore, false, "", "tcp", "single", addr, 1, 0, 0, nil, false, nil, 10*time.Second, "", 0, "")
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
			return redis.NewClientImpl(statsStore, false, "", "tcp", "single", addr, 1, pipelineWindow, pipelineLimit, nil, false, nil, 10*time.Second, "", 0, "")
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

// Tests for pool on-empty behavior
func TestPoolOnEmptyBehavior(t *testing.T) {
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	// Helper to create client with specific on-empty behavior
	mkRedisClientWithBehavior := func(addr, behavior string, waitDuration time.Duration) redis.Client {
		return redis.NewClientImpl(statsStore, false, "", "tcp", "single", addr, 1, 0, 0, nil, false, nil, 10*time.Second, behavior, waitDuration, "")
	}

	t.Run("default behavior (empty string)", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		var client redis.Client
		assert.NotPanics(t, func() {
			client = mkRedisClientWithBehavior(redisSrv.Addr(), "", 0)
		})
		assert.NotNil(t, client)

		// Verify client works
		var res string
		assert.Nil(t, client.DoCmd(nil, "SET", "foo", "bar"))
		assert.Nil(t, client.DoCmd(&res, "GET", "foo"))
		assert.Equal(t, "bar", res)
	})

	t.Run("ERROR behavior", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		var client redis.Client
		assert.NotPanics(t, func() {
			client = mkRedisClientWithBehavior(redisSrv.Addr(), "ERROR", 0)
		})
		assert.NotNil(t, client)

		// Verify client works
		var res string
		assert.Nil(t, client.DoCmd(nil, "SET", "test", "value"))
		assert.Nil(t, client.DoCmd(&res, "GET", "test"))
		assert.Equal(t, "value", res)
	})

	t.Run("ERROR behavior with wait duration", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		var client redis.Client
		assert.NotPanics(t, func() {
			client = mkRedisClientWithBehavior(redisSrv.Addr(), "ERROR", 100*time.Millisecond)
		})
		assert.NotNil(t, client)

		// Verify client works
		var res string
		assert.Nil(t, client.DoCmd(nil, "SET", "test2", "value2"))
		assert.Nil(t, client.DoCmd(&res, "GET", "test2"))
		assert.Equal(t, "value2", res)
	})

	t.Run("CREATE behavior", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		var client redis.Client
		assert.NotPanics(t, func() {
			client = mkRedisClientWithBehavior(redisSrv.Addr(), "CREATE", 0)
		})
		assert.NotNil(t, client)

		// Verify client works
		var res string
		assert.Nil(t, client.DoCmd(nil, "SET", "test3", "value3"))
		assert.Nil(t, client.DoCmd(&res, "GET", "test3"))
		assert.Equal(t, "value3", res)
	})

	t.Run("CREATE behavior with wait duration", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		var client redis.Client
		assert.NotPanics(t, func() {
			client = mkRedisClientWithBehavior(redisSrv.Addr(), "CREATE", 500*time.Millisecond)
		})
		assert.NotNil(t, client)

		// Verify client works
		var res string
		assert.Nil(t, client.DoCmd(nil, "SET", "test4", "value4"))
		assert.Nil(t, client.DoCmd(&res, "GET", "test4"))
		assert.Equal(t, "value4", res)
	})

	t.Run("WAIT behavior", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		var client redis.Client
		assert.NotPanics(t, func() {
			client = mkRedisClientWithBehavior(redisSrv.Addr(), "WAIT", 0)
		})
		assert.NotNil(t, client)

		// Verify client works
		var res string
		assert.Nil(t, client.DoCmd(nil, "SET", "test5", "value5"))
		assert.Nil(t, client.DoCmd(&res, "GET", "test5"))
		assert.Equal(t, "value5", res)
	})

	t.Run("case insensitive behavior", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		// Test lowercase
		var client redis.Client
		assert.NotPanics(t, func() {
			client = mkRedisClientWithBehavior(redisSrv.Addr(), "error", 0)
		})
		assert.NotNil(t, client)

		// Verify client works
		var res string
		assert.Nil(t, client.DoCmd(nil, "SET", "test6", "value6"))
		assert.Nil(t, client.DoCmd(&res, "GET", "test6"))
		assert.Equal(t, "value6", res)
	})

	t.Run("unknown behavior falls back to default", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		// Unknown behavior should not panic, just log warning and use default
		var client redis.Client
		assert.NotPanics(t, func() {
			client = mkRedisClientWithBehavior(redisSrv.Addr(), "UNKNOWN_BEHAVIOR", 0)
		})
		assert.NotNil(t, client)

		// Verify client works
		var res string
		assert.Nil(t, client.DoCmd(nil, "SET", "test7", "value7"))
		assert.Nil(t, client.DoCmd(&res, "GET", "test7"))
		assert.Equal(t, "value7", res)
	})
}

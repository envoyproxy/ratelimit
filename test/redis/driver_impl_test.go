package redis_test

import (
	"context"
	"fmt"
	"github.com/alicebob/miniredis/v2"
	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
	"testing"

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

func testNewClientImpl(t *testing.T, implicitPipeline bool) func(t *testing.T) {
	return func(t *testing.T) {
		redisAuth := "123"
		statsStore := stats.NewStore(stats.NewNullSink(), false)

		mkRedisClient := func(auth, addr string) redis.Client {
			return redis.NewClientImpl(context.Background(), statsStore, false, auth, "tcp", "single", addr, 1, implicitPipeline, nil, false, nil)
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

			assert.PanicsWithError(t, "response returned from Conn: NOAUTH Authentication required.", func() {
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
			assert.PanicsWithError(t, "response returned from Conn: WRONGPASS invalid username-password pair", func() {
				mkRedisClient(redisAuth, redisSrv.Addr())
			})
		})

		t.Run("ImplicitPipeliningEnabled() return expected value", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			client := mkRedisClient("", redisSrv.Addr())

			if implicitPipeline {
				assert.True(t, client.ImplicitPipeliningEnabled())
			} else {
				assert.False(t, client.ImplicitPipeliningEnabled())
			}
		})
	}
}

func TestNewClientImpl(t *testing.T) {
	t.Run("ImplicitPipeliningEnabled", testNewClientImpl(t, true))
	t.Run("ImplicitPipeliningDisabled", testNewClientImpl(t, false))
}

func TestDoCmd(t *testing.T) {
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	mkRedisClient := func(addr string) redis.Client {
		return redis.NewClientImpl(context.Background(), statsStore, false, "", "tcp", "single", addr, 1, false, nil, false, nil)
	}

	t.Run("SETGET ok", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		client := mkRedisClient(redisSrv.Addr())
		var res string

		assert.Nil(t, client.DoCmd(context.Background(), nil, "SET", "foo", "bar"))
		assert.Nil(t, client.DoCmd(context.Background(), &res, "GET", "foo"))
		assert.Equal(t, "bar", res)
	})

	t.Run("INCRBY ok", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		client := mkRedisClient(redisSrv.Addr())
		var res uint32
		hits := uint32(1)

		assert.Nil(t, client.DoCmd(context.Background(), &res, "INCRBY", "a", hits))
		assert.Equal(t, hits, res)
		assert.Nil(t, client.DoCmd(context.Background(), &res, "INCRBY", "a", hits))
		assert.Equal(t, uint32(2), res)
	})

	t.Run("connection broken", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		client := mkRedisClient(redisSrv.Addr())

		assert.Nil(t, client.DoCmd(context.Background(), nil, "SET", "foo", "bar"))

		redisSrv.Close()
		assert.EqualError(t, client.DoCmd(context.Background(), nil, "GET", "foo"), "response returned from Conn: EOF")
	})
}

func testPipeDo(t *testing.T, implicitPipeline bool) func(t *testing.T) {
	return func(t *testing.T) {
		statsStore := stats.NewStore(stats.NewNullSink(), false)

		mkRedisClient := func(addr string) redis.Client {
			return redis.NewClientImpl(context.Background(), statsStore, false, "", "tcp", "single", addr, 1, implicitPipeline, nil, false, nil)
		}

		t.Run("SETGET ok", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			client := mkRedisClient(redisSrv.Addr())
			var res string

			pipeline := redis.Pipeline{}
			pipeline = client.PipeAppend(pipeline, nil, "SET", "foo", "bar")
			pipeline = client.PipeAppend(pipeline, &res, "GET", "foo")

			assert.Nil(t, client.PipeDo(context.Background(), pipeline))
			assert.Equal(t, "bar", res)
		})

		t.Run("INCRBY ok", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			client := mkRedisClient(redisSrv.Addr())
			var res uint32
			hits := uint32(1)

			assert.Nil(t, client.PipeDo(context.Background(), client.PipeAppend(redis.Pipeline{}, &res, "INCRBY", "a", hits)))
			assert.Equal(t, hits, res)

			assert.Nil(t, client.PipeDo(context.Background(), client.PipeAppend(redis.Pipeline{}, &res, "INCRBY", "a", hits)))
			assert.Equal(t, uint32(2), res)
		})

		t.Run("connection broken", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			client := mkRedisClient(redisSrv.Addr())

			assert.Nil(t, nil, client.PipeDo(context.Background(), client.PipeAppend(redis.Pipeline{}, nil, "SET", "foo", "bar")))

			redisSrv.Close()

			expectErrContainEOF := func(t *testing.T, err error) {
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), "EOF")
			}

			expectErrContainEOF(t, client.PipeDo(context.Background(), client.PipeAppend(redis.Pipeline{}, nil, "GET", "foo")))
		})
	}
}

func TestPipeDo(t *testing.T) {
	t.Run("ImplicitPipeliningEnabled", testPipeDo(t, true))
	t.Run("ImplicitPipeliningDisabled", testPipeDo(t, false))
}

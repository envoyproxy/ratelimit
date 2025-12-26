package redis_test

import (
	"fmt"
	"strings"
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

func testNewClientImpl(t *testing.T, pipelineWindow time.Duration, pipelineLimit int, useExplicitPipeline bool) func(t *testing.T) {
	return func(t *testing.T) {
		redisAuth := "123"
		statsStore := stats.NewStore(stats.NewNullSink(), false)

		mkRedisClient := func(auth, addr string) redis.Client {
			return redis.NewClientImpl(statsStore, false, auth, "tcp", "single", addr, 1, pipelineWindow, pipelineLimit, nil, false, nil, 10*time.Second, "", 0, "", useExplicitPipeline)
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

		t.Run("UseExplicitPipeline() return expected value", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			client := mkRedisClient("", redisSrv.Addr())

			assert.Equal(t, useExplicitPipeline, client.UseExplicitPipeline())
		})
	}
}

func TestNewClientImpl(t *testing.T) {
	t.Run("AutoBuffering", testNewClientImpl(t, 2*time.Millisecond, 2, false))
	t.Run("ExplicitPipeline", testNewClientImpl(t, 0, 0, true))
}

func TestDoCmd(t *testing.T) {
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	mkRedisClient := func(addr string) redis.Client {
		return redis.NewClientImpl(statsStore, false, "", "tcp", "single", addr, 1, 0, 0, nil, false, nil, 10*time.Second, "", 0, "", false)
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
		assert.EqualError(t, client.DoCmd(nil, "GET", "foo"), "response returned from Conn: EOF")
	})
}

func testPipeDo(t *testing.T, pipelineWindow time.Duration, pipelineLimit int, useExplicitPipeline bool) func(t *testing.T) {
	return func(t *testing.T) {
		statsStore := stats.NewStore(stats.NewNullSink(), false)

		mkRedisClient := func(addr string) redis.Client {
			return redis.NewClientImpl(statsStore, false, "", "tcp", "single", addr, 1, pipelineWindow, pipelineLimit, nil, false, nil, 10*time.Second, "", 0, "", useExplicitPipeline)
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
				// radix v4 wraps errors with "response returned from Conn:"
				// and may return different connection errors (EOF, connection reset, etc)
				errMsg := err.Error()
				hasConnectionError := strings.Contains(errMsg, "EOF") ||
					strings.Contains(errMsg, "connection reset") ||
					strings.Contains(errMsg, "broken pipe")
				assert.True(t, hasConnectionError, "expected connection error, got: %s", errMsg)
			}

			expectErrContainEOF(t, client.PipeDo(client.PipeAppend(redis.Pipeline{}, nil, "GET", "foo")))
		})
	}
}

func TestPipeDo(t *testing.T) {
	t.Run("AutoBuffering", testPipeDo(t, 10*time.Millisecond, 2, false))
	t.Run("ExplicitPipeline", testPipeDo(t, 0, 0, true))
}

// Tests for pool on-empty behavior
func TestPoolOnEmptyBehavior(t *testing.T) {
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	// Helper to create client with specific on-empty behavior
	mkRedisClientWithBehavior := func(addr, behavior string, waitDuration time.Duration) redis.Client {
		return redis.NewClientImpl(statsStore, false, "", "tcp", "single", addr, 1, 0, 0, nil, false, nil, 10*time.Second, behavior, waitDuration, "", false)
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

	t.Run("ERROR behavior should panic", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		// radix v4 does not support ERROR behavior - should panic at startup
		panicErr := expectPanicError(t, func() {
			mkRedisClientWithBehavior(redisSrv.Addr(), "ERROR", 0)
		})
		assert.Contains(t, panicErr.Error(), "REDIS_POOL_ON_EMPTY_BEHAVIOR=ERROR is not supported in radix v4")
	})

	t.Run("ERROR behavior with wait duration should panic", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		// radix v4 does not support ERROR behavior - should panic at startup
		panicErr := expectPanicError(t, func() {
			mkRedisClientWithBehavior(redisSrv.Addr(), "ERROR", 100*time.Millisecond)
		})
		assert.Contains(t, panicErr.Error(), "REDIS_POOL_ON_EMPTY_BEHAVIOR=ERROR is not supported in radix v4")
	})

	t.Run("CREATE behavior should panic", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		// radix v4 does not support CREATE behavior - should panic at startup
		panicErr := expectPanicError(t, func() {
			mkRedisClientWithBehavior(redisSrv.Addr(), "CREATE", 0)
		})
		assert.Contains(t, panicErr.Error(), "REDIS_POOL_ON_EMPTY_BEHAVIOR=CREATE is not supported in radix v4")
	})

	t.Run("CREATE behavior with wait duration should panic", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		// radix v4 does not support CREATE behavior - should panic at startup
		panicErr := expectPanicError(t, func() {
			mkRedisClientWithBehavior(redisSrv.Addr(), "CREATE", 500*time.Millisecond)
		})
		assert.Contains(t, panicErr.Error(), "REDIS_POOL_ON_EMPTY_BEHAVIOR=CREATE is not supported in radix v4")
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

	t.Run("case insensitive behavior - lowercase 'error' panics", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		// Test that lowercase 'error' is treated same as 'ERROR' (case insensitive)
		panicErr := expectPanicError(t, func() {
			mkRedisClientWithBehavior(redisSrv.Addr(), "error", 0)
		})
		assert.Contains(t, panicErr.Error(), "REDIS_POOL_ON_EMPTY_BEHAVIOR=ERROR is not supported in radix v4")
	})

	t.Run("case insensitive behavior - lowercase 'create' panics", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		// Test that lowercase 'create' is treated same as 'CREATE' (case insensitive)
		panicErr := expectPanicError(t, func() {
			mkRedisClientWithBehavior(redisSrv.Addr(), "create", 0)
		})
		assert.Contains(t, panicErr.Error(), "REDIS_POOL_ON_EMPTY_BEHAVIOR=CREATE is not supported in radix v4")
	})

	t.Run("case insensitive behavior - lowercase 'wait' works", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		// Test that lowercase 'wait' is treated same as 'WAIT' (case insensitive)
		var client redis.Client
		assert.NotPanics(t, func() {
			client = mkRedisClientWithBehavior(redisSrv.Addr(), "wait", 0)
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

func TestNewClientImplSentinel(t *testing.T) {
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	mkSentinelClient := func(auth, sentinelAuth, url string, useTls bool, timeout time.Duration) redis.Client {
		// Pass nil for tlsConfig - we can't test TLS without a real TLS server,
		// but we can verify the code path is executed (logs will show TLS is enabled)
		return redis.NewClientImpl(statsStore, useTls, auth, "tcp", "sentinel", url, 1, 0, 0, nil, false, nil, timeout, "", 0, sentinelAuth, false)
	}

	t.Run("invalid url format - missing sentinel addresses", func(t *testing.T) {
		panicErr := expectPanicError(t, func() {
			mkSentinelClient("", "", "mymaster", false, 10*time.Second)
		})
		assert.Contains(t, panicErr.Error(), "Expected master name and a list of urls for the sentinels")
	})

	t.Run("invalid url format - only master name", func(t *testing.T) {
		panicErr := expectPanicError(t, func() {
			mkSentinelClient("", "", "mymaster,", false, 10*time.Second)
		})
		// Empty sentinel address causes "missing address" error from radix
		assert.True(t,
			containsAny(panicErr.Error(), []string{"Expected master name", "missing address"}),
			"Expected format validation error, got: %s", panicErr.Error())
	})

	t.Run("connection refused - sentinel not available", func(t *testing.T) {
		// Use a port that's unlikely to have a sentinel running
		url := "mymaster,localhost:12345"
		panicErr := expectPanicError(t, func() {
			mkSentinelClient("", "", url, false, 1*time.Second)
		})
		// Should fail with connection error or timeout
		assert.NotNil(t, panicErr)
		assert.True(t,
			containsAny(panicErr.Error(), []string{"connection refused", "timeout", "no such host", "connect"}),
			"Expected connection error, got: %s", panicErr.Error())
	})

	t.Run("sentinel auth password only", func(t *testing.T) {
		// This will fail to connect, but we're testing that sentinelAuth parameter is accepted
		// The log output will show "enabling authentication to redis sentinel" which confirms the code path
		url := "mymaster,localhost:12345"
		panicErr := expectPanicError(t, func() {
			mkSentinelClient("", "sentinel-password", url, false, 1*time.Second)
		})
		// Should fail with connection error, not auth error (since we can't connect)
		assert.NotNil(t, panicErr)
		assert.True(t,
			containsAny(panicErr.Error(), []string{"connection refused", "timeout", "no such host", "connect"}),
			"Expected connection error, got: %s", panicErr.Error())
	})

	t.Run("sentinel auth user:password", func(t *testing.T) {
		// This will fail to connect, but we're testing that sentinelAuth parameter with user:password format is accepted
		// The log output will show "enabling authentication to redis sentinel on ... with user sentinel-user"
		url := "mymaster,localhost:12345"
		panicErr := expectPanicError(t, func() {
			mkSentinelClient("", "sentinel-user:sentinel-pass", url, false, 1*time.Second)
		})
		// Should fail with connection error, not auth error (since we can't connect)
		assert.NotNil(t, panicErr)
		assert.True(t,
			containsAny(panicErr.Error(), []string{"connection refused", "timeout", "no such host", "connect"}),
			"Expected connection error, got: %s", panicErr.Error())
	})

	t.Run("sentinel with timeout", func(t *testing.T) {
		// Test that timeout parameter is used
		url := "mymaster,localhost:12345"
		start := time.Now()
		panicErr := expectPanicError(t, func() {
			mkSentinelClient("", "", url, false, 500*time.Millisecond)
		})
		duration := time.Since(start)
		assert.NotNil(t, panicErr)
		// Timeout should be respected (with some tolerance)
		assert.True(t, duration < 2*time.Second, "Timeout should be respected, took %v", duration)
	})

	t.Run("sentinel with multiple addresses", func(t *testing.T) {
		// Test that multiple sentinel addresses are accepted in URL format
		url := "mymaster,localhost:12345,localhost:12346,localhost:12347"
		panicErr := expectPanicError(t, func() {
			mkSentinelClient("", "", url, false, 1*time.Second)
		})
		// Should fail with connection error, not format error
		assert.NotNil(t, panicErr)
		assert.NotContains(t, panicErr.Error(), "Expected master name")
		assert.True(t,
			containsAny(panicErr.Error(), []string{"connection refused", "timeout", "no such host", "connect"}),
			"Expected connection error, got: %s", panicErr.Error())
	})

	t.Run("sentinel with redis auth but no sentinel auth", func(t *testing.T) {
		// Test that redis auth and sentinel auth are separate
		// redisAuth is for master/replica, sentinelAuth is for sentinel nodes
		url := "mymaster,localhost:12345"
		panicErr := expectPanicError(t, func() {
			mkSentinelClient("redis-password", "", url, false, 1*time.Second)
		})
		// Should fail with connection error (can't test auth without real sentinel)
		assert.NotNil(t, panicErr)
		assert.True(t,
			containsAny(panicErr.Error(), []string{"connection refused", "timeout", "no such host", "connect"}),
			"Expected connection error, got: %s", panicErr.Error())
	})

	t.Run("sentinel with TLS enabled", func(t *testing.T) {
		// Test that TLS configuration is accepted (will fail to connect without real TLS server)
		// The log output will show "enabling TLS to redis sentinel" which confirms the code path
		url := "mymaster,localhost:12345"
		panicErr := expectPanicError(t, func() {
			mkSentinelClient("", "", url, true, 1*time.Second)
		})
		// Should fail with connection/TLS error (can't test TLS without real TLS server)
		assert.NotNil(t, panicErr)
		// Error could be connection refused, TLS handshake failure, or timeout
		assert.True(t,
			containsAny(panicErr.Error(), []string{"connection refused", "timeout", "no such host", "connect", "tls", "handshake"}),
			"Expected connection/TLS error, got: %s", panicErr.Error())
	})

	t.Run("sentinel with TLS and sentinel auth", func(t *testing.T) {
		// Test that both TLS and sentinel auth can be configured together
		// The log output will show both TLS and auth messages
		url := "mymaster,localhost:12345"
		panicErr := expectPanicError(t, func() {
			mkSentinelClient("redis-password", "sentinel-password", url, true, 1*time.Second)
		})
		// Should fail with connection/TLS error (can't test without real servers)
		assert.NotNil(t, panicErr)
		assert.True(t,
			containsAny(panicErr.Error(), []string{"connection refused", "timeout", "no such host", "connect", "tls", "handshake"}),
			"Expected connection/TLS error, got: %s", panicErr.Error())
	})
}

// Helper function to check if error message contains any of the given strings
func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if strings.Contains(strings.ToLower(s), strings.ToLower(substr)) {
			return true
		}
	}
	return false
}

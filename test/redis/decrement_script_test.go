package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	gostats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/redis"
)

func mkSingleRedisClient(addr string) redis.Client {
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	return redis.NewClientImpl(context.Background(), statsStore, false, "", "tcp", "single", addr, 1, 0, 0, nil, false, nil,
		10*time.Second, "", "", time.Second, 30*time.Second, 100*time.Millisecond, false)
}

func evalDecrementScript(t *testing.T, client redis.Client, key string, hitsAddend uint64, expirationSeconds int64,
	publishFlag string, requestsPerUnit uint32,
) int64 {
	t.Helper()
	var result int64
	err := client.DoCmd(&result, "EVAL", redis.DecrementScript, 1, key, hitsAddend, expirationSeconds, publishFlag,
		requestsPerUnit, redis.LocalCacheInvalidationChannel)
	assert.NoError(t, err)
	return result
}

// Buffers the subscriber channel: publishing inside miniredis blocks the
// publishing command until the message is consumed.
func pumpMessages(sub *miniredis.Subscriber) <-chan string {
	messages := make(chan string, 100)
	go func() {
		for message := range sub.Messages() {
			messages <- message.Message
		}
		close(messages)
	}()
	return messages
}

func drainMessages(messages <-chan string) []string {
	var drained []string
	for {
		select {
		case message := <-messages:
			drained = append(drained, message)
		case <-time.After(100 * time.Millisecond):
			return drained
		}
	}
}

func TestDecrementScript(t *testing.T) {
	redisSrv := mustNewRedisServer()
	defer redisSrv.Close()

	client := mkSingleRedisClient(redisSrv.Addr())
	defer client.Close()

	sub := redisSrv.NewSubscriber()
	defer sub.Close()
	sub.Subscribe(redis.LocalCacheInvalidationChannel)
	messages := pumpMessages(sub)

	t.Run("absent key returns 0 and does not publish", func(t *testing.T) {
		assert.EqualValues(t, 0, evalDecrementScript(t, client, "absent_key", 3, 60, "1", 10))
		assert.False(t, redisSrv.Exists("absent_key"))
		assert.Empty(t, drainMessages(messages))
	})

	t.Run("crossing refund publishes the key and resets the TTL", func(t *testing.T) {
		assert.NoError(t, redisSrv.Set("crossing_key", "12"))
		redisSrv.SetTTL("crossing_key", 5*time.Second)

		assert.EqualValues(t, 9, evalDecrementScript(t, client, "crossing_key", 3, 60, "1", 10))
		assert.Equal(t, []string{"crossing_key"}, drainMessages(messages))
		assert.Equal(t, 60*time.Second, redisSrv.TTL("crossing_key"))
	})

	t.Run("refund leaving the counter above the limit does not publish", func(t *testing.T) {
		assert.NoError(t, redisSrv.Set("above_key", "20"))

		assert.EqualValues(t, 17, evalDecrementScript(t, client, "above_key", 3, 60, "1", 10))
		assert.Empty(t, drainMessages(messages))

		// One message per poisoning cycle: only the refund that finally crosses
		// back under the limit publishes.
		assert.EqualValues(t, 10, evalDecrementScript(t, client, "above_key", 7, 60, "1", 10))
		assert.Equal(t, []string{"above_key"}, drainMessages(messages))
		assert.EqualValues(t, 8, evalDecrementScript(t, client, "above_key", 2, 60, "1", 10))
		assert.Empty(t, drainMessages(messages))
	})

	t.Run("counter floors at zero", func(t *testing.T) {
		assert.NoError(t, redisSrv.Set("floor_key", "12"))

		assert.EqualValues(t, 0, evalDecrementScript(t, client, "floor_key", 20, 60, "1", 10))
		assert.Equal(t, []string{"floor_key"}, drainMessages(messages))
	})
}

// Radix classifies bare EVAL as keyless; the cache key must reach
// ActionProperties.Keys or cluster routing breaks.
func TestDecrementEvalRoutesByCacheKey(t *testing.T) {
	redisSrv := mustNewRedisServer()
	defer redisSrv.Close()
	client := mkSingleRedisClient(redisSrv.Addr())
	defer client.Close()

	p := client.PipeAppendWithRoutingKey(redis.Pipeline{}, "cache_key", nil, "EVAL",
		redis.DecrementScript, 1, "cache_key", 3, 60, "1", 10, redis.LocalCacheInvalidationChannel)
	assert.Equal(t, []string{"cache_key"}, p[0].Action.Properties().Keys)
	assert.True(t, p[0].Action.Properties().CanRetry)
}

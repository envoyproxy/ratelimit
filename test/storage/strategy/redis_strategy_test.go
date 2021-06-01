package strategy_test

import (
	"testing"

	"github.com/alicebob/miniredis"
	"github.com/envoyproxy/ratelimit/src/storage/strategy"
	"github.com/golang/mock/gomock"
	"github.com/mediocregopher/radix/v3"
	"github.com/stretchr/testify/assert"

	mock_service "github.com/envoyproxy/ratelimit/test/mocks/storage/service"
)

func mustNewRedisServer() *miniredis.Miniredis {
	srv, err := miniredis.Run()
	if err != nil {
		panic(err)
	}

	return srv
}
func TestRedisStrategyGetValue(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	mockRedisClient := mock_service.NewMockRedisClientInterface(controller)
	redisStrategy := strategy.RedisStrategy{
		Client: mockRedisClient,
	}

	var value uint64
	mockRedisClient.EXPECT().Do(radix.Cmd(&value, "GET", "key")).Return(nil)
	value, err := redisStrategy.GetValue("key")

	assert.Equal(value, uint64(0))
	assert.Nil(err)
}

func TestRedisStrategySetValue(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	mockRedisClient := mock_service.NewMockRedisClientInterface(controller)
	redisStrategy := strategy.RedisStrategy{
		Client: mockRedisClient,
	}

	mockRedisClient.EXPECT().Do(radix.FlatCmd(nil, "SET", "key", uint64(5))).Return(nil)
	mockRedisClient.EXPECT().Do(radix.FlatCmd(nil, "EXPIRE", "key", uint64(5))).Return(nil)

	err := redisStrategy.SetValue("key", uint64(5), uint64(5))
	assert.Nil(err)
}

func TestRedisStrategyIncrementValue(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	mockRedisClient := mock_service.NewMockRedisClientInterface(controller)
	redisStrategy := strategy.RedisStrategy{
		Client: mockRedisClient,
	}

	mockRedisClient.EXPECT().Do(radix.FlatCmd(nil, "INCRBY", "key", uint64(1))).Return(nil)

	err := redisStrategy.IncrementValue("key", uint64(1))
	assert.Nil(err)
}

package strategy_test

import (
	"strconv"
	"testing"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/envoyproxy/ratelimit/src/storage/strategy"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	mock_service "github.com/envoyproxy/ratelimit/test/mocks/storage/service"
)

func TestMemcachedStrategyGetValue(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	mockMemcachedClient := mock_service.NewMockMemcachedClientInterface(controller)
	memcachedStrategy := strategy.MemcachedStrategy{
		Client: mockMemcachedClient,
	}

	mockMemcachedClient.EXPECT().Get("key").Return(&memcache.Item{Key: "key", Value: []byte("5")}, nil)
	value, err := memcachedStrategy.GetValue("key")

	assert.Equal(value, uint64(5))
	assert.Nil(err)
}

func TestMemcachedStrategySetValue(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	mockMemcachedClient := mock_service.NewMockMemcachedClientInterface(controller)
	memcachedStrategy := strategy.MemcachedStrategy{
		Client: mockMemcachedClient,
	}

	mockMemcachedClient.EXPECT().Set(&memcache.Item{
		Key:        "key",
		Value:      []byte(strconv.FormatUint(uint64(5), 10)),
		Expiration: int32(5),
	}).Return(nil)

	err := memcachedStrategy.SetValue("key", uint64(5), uint64(5))
	assert.Nil(err)
}

func TestMemcachedStrategyIncrementValue(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	mockMemcachedClient := mock_service.NewMockMemcachedClientInterface(controller)
	memcachedStrategy := strategy.MemcachedStrategy{
		Client: mockMemcachedClient,
	}

	mockMemcachedClient.EXPECT().Increment("key", uint64(1)).Return(uint64(1), nil)

	err := memcachedStrategy.IncrementValue("key", uint64(1))
	assert.Nil(err)
}

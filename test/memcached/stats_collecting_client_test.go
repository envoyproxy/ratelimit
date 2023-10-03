package memcached_test

import (
	"errors"
	"testing"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/golang/mock/gomock"
	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/memcached"
	mock_memcached "github.com/envoyproxy/ratelimit/test/mocks/memcached"
)

type fakeSink struct {
	values map[string]uint64
}

func (fs *fakeSink) FlushCounter(name string, value uint64) {
	if _, ok := fs.values[name]; ok {
		panic(errors.New("fakeSink wasn't cleared before flushing again"))
	}

	fs.values[name] = value
}

func (fs *fakeSink) FlushGauge(name string, value uint64) {}

func (fs *fakeSink) FlushTimer(name string, value float64) {}

func (fs *fakeSink) Flush() {}

func (fs *fakeSink) Reset() {
	fs.values = make(map[string]uint64)
}

func TestStats_MultiGet(t *testing.T) {
	fakeSink := &fakeSink{}
	fakeSink.Reset()

	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()
	client := mock_memcached.NewMockClient(controller)
	statsStore := stats.NewStore(fakeSink, false)

	sc := memcached.CollectStats(client, statsStore)

	returnValue := map[string]*memcache.Item{"foo": nil}
	arg := []string{"foo"}

	client.EXPECT().GetMulti(arg).Return(returnValue, nil)
	actualReturnValue, err := sc.GetMulti(arg)
	statsStore.Flush()

	assert.Equal(returnValue, actualReturnValue)
	assert.Nil(err)
	assert.Equal(map[string]uint64{
		"keys_found":              1,
		"keys_requested":          1,
		"multiget.__code=success": 1,
	}, fakeSink.values)

	fakeSink.Reset()
	returnValue = map[string]*memcache.Item{"foo": nil, "bar": nil}
	client.EXPECT().GetMulti(arg).Return(returnValue, nil)
	actualReturnValue, err = sc.GetMulti(arg)
	statsStore.Flush()

	assert.Equal(returnValue, actualReturnValue)
	assert.Nil(err)
	assert.Equal(map[string]uint64{
		"keys_found":              2,
		"keys_requested":          1,
		"multiget.__code=success": 1,
	}, fakeSink.values)

	fakeSink.Reset()
	returnValue = map[string]*memcache.Item{}
	arg = []string{"foo", "bar"}

	client.EXPECT().GetMulti(arg).Return(returnValue, nil)
	actualReturnValue, err = sc.GetMulti(arg)

	statsStore.Flush()
	assert.Equal(returnValue, actualReturnValue)
	assert.Nil(err)

	assert.Equal(map[string]uint64{
		"keys_requested":          2,
		"multiget.__code=success": 1,
	}, fakeSink.values)

	fakeSink.Reset()
	returnValue = map[string]*memcache.Item{"ignored": nil}
	arg = []string{"foo"}
	returnedErr := errors.New("Random error")

	client.EXPECT().GetMulti(arg).Return(returnValue, returnedErr)
	actualReturnValue, err = sc.GetMulti(arg)

	statsStore.Flush()
	assert.Equal(returnValue, actualReturnValue)
	assert.Equal(returnedErr, err)

	assert.Equal(map[string]uint64{
		"keys_requested":        1,
		"multiget.__code=error": 1,
	}, fakeSink.values)
}

func TestStats_Increment(t *testing.T) {
	fakeSink := &fakeSink{}

	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()
	client := mock_memcached.NewMockClient(controller)
	statsStore := stats.NewStore(fakeSink, false)

	sc := memcached.CollectStats(client, statsStore)

	fakeSink.Reset()
	client.EXPECT().Increment("foo", uint64(5)).Return(uint64(6), nil)
	newValue, err := sc.Increment("foo", 5)
	statsStore.Flush()

	assert.Equal(uint64(6), newValue)
	assert.Nil(err)
	assert.Equal(map[string]uint64{
		"increment.__code=success": 1,
	}, fakeSink.values)

	expectedErr := errors.New("expectedError")
	fakeSink.Reset()
	client.EXPECT().Increment("foo", uint64(5)).Return(uint64(0), expectedErr)
	_, err = sc.Increment("foo", 5)
	statsStore.Flush()

	assert.Equal(expectedErr, err)
	assert.Equal(map[string]uint64{
		"increment.__code=error": 1,
	}, fakeSink.values)

	fakeSink.Reset()
	client.EXPECT().Increment("foo", uint64(5)).Return(uint64(0), memcache.ErrCacheMiss)
	_, err = sc.Increment("foo", 5)
	statsStore.Flush()

	assert.Equal(memcache.ErrCacheMiss, err)
	assert.Equal(map[string]uint64{
		"increment.__code=miss": 1,
	}, fakeSink.values)
}

func TestStats_Add(t *testing.T) {
	fakeSink := &fakeSink{}

	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()
	client := mock_memcached.NewMockClient(controller)
	statsStore := stats.NewStore(fakeSink, false)

	sc := memcached.CollectStats(client, statsStore)
	item := &memcache.Item{}

	fakeSink.Reset()
	client.EXPECT().Add(item).Return(nil)
	err := sc.Add(item)
	statsStore.Flush()

	assert.Nil(err)
	assert.Equal(map[string]uint64{
		"add.__code=success": 1,
	}, fakeSink.values)

	expectedErr := errors.New("expected err")

	fakeSink.Reset()
	client.EXPECT().Add(item).Return(expectedErr)
	err = sc.Add(item)
	statsStore.Flush()

	assert.Equal(expectedErr, err)
	assert.Equal(map[string]uint64{
		"add.__code=error": 1,
	}, fakeSink.values)

	fakeSink.Reset()
	client.EXPECT().Add(item).Return(memcache.ErrNotStored)
	err = sc.Add(item)
	statsStore.Flush()

	assert.Equal(memcache.ErrNotStored, err)
	assert.Equal(map[string]uint64{
		"add.__code=not_stored": 1,
	}, fakeSink.values)
}

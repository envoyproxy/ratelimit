package stats

import (
	"github.com/envoyproxy/ratelimit/src/config"
	ratelimit "github.com/envoyproxy/ratelimit/src/service"
	settings "github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
	mock_config "github.com/envoyproxy/ratelimit/test/mocks/config"
	mock_limiter "github.com/envoyproxy/ratelimit/test/mocks/limiter"
	mock_loader "github.com/envoyproxy/ratelimit/test/mocks/runtime/loader"
	mock_snapshot "github.com/envoyproxy/ratelimit/test/mocks/runtime/snapshot"
	"github.com/golang/mock/gomock"
	gostats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
	"testing"
)

func commonSetup(t *testing.T, detailedMetrics bool) rateLimitServiceTestSuite {
	ret := rateLimitServiceTestSuite{}
	ret.assert = assert.New(t)
	ret.controller = gomock.NewController(t)
	ret.runtime = mock_loader.NewMockIFace(ret.controller)
	ret.snapshot = mock_snapshot.NewMockIFace(ret.controller)
	ret.cache = mock_limiter.NewMockRateLimitCache(ret.controller)
	ret.configLoader = mock_config.NewMockRateLimitConfigLoader(ret.controller)
	ret.config = mock_config.NewMockRateLimitConfig(ret.controller)
	ret.store = gostats.NewStore(gostats.NewNullSink(), false)
	sett := settings.NewSettings()
	sett.DetailedMetrics = detailedMetrics
	ret.sm = stats.NewStatManager(ret.store, sett)
	return ret
}

type rateLimitServiceTestSuite struct {
	assert                *assert.Assertions
	controller            *gomock.Controller
	runtime               *mock_loader.MockIFace
	snapshot              *mock_snapshot.MockIFace
	cache                 *mock_limiter.MockRateLimitCache
	configLoader          *mock_config.MockRateLimitConfigLoader
	config                *mock_config.MockRateLimitConfig
	runtimeUpdateCallback chan<- int
	sm                    stats.Manager
	store                 gostats.Store
}

func (this *rateLimitServiceTestSuite) setupBasicService() ratelimit.RateLimitServiceServer {
	this.runtime.EXPECT().AddUpdateCallback(gomock.Any()).Do(
		func(callback chan<- int) {
			this.runtimeUpdateCallback = callback
		})
	this.runtime.EXPECT().Snapshot().Return(this.snapshot).MinTimes(1)
	this.snapshot.EXPECT().Keys().Return([]string{"foo", "config.basic_config"}).MinTimes(1)
	this.snapshot.EXPECT().Get("config.basic_config").Return("fake_yaml").MinTimes(1)
	this.configLoader.EXPECT().Load(
		[]config.RateLimitConfigToLoad{{"config.basic_config", "fake_yaml"}},
		gomock.Any()).Return(this.config)
	return ratelimit.NewService(this.runtime, this.cache, this.configLoader, this.sm, true)
}

func TestDetailedMetricsTotalHits(test *testing.T) {
	t := commonSetup(test, true)
	defer t.controller.Finish()

	key := "hello_world"
	detailedKey1 := "hello_world_detailed1"
	detailedKey2 := "hello_world_detailed2"
	rlStats := t.sm.NewStats(key)
	t.sm.AddTotalHits(11, rlStats, detailedKey1)
	t.sm.AddTotalHits(22, rlStats, detailedKey2)

	assert.Equal(test, uint64(33), t.sm.NewStats(key).TotalHits.Value())
	assert.Equal(test, uint64(11), t.sm.NewDetailedStats(detailedKey1).TotalHits.Value())
	assert.Equal(test, uint64(22), t.sm.NewDetailedStats(detailedKey2).TotalHits.Value())
}
func TestDetailedMetricsNearLimit(test *testing.T) {
	t := commonSetup(test, true)
	defer t.controller.Finish()

	key := "hello_world"
	detailedKey1 := "hello_world_detailed1"
	detailedKey2 := "hello_world_detailed2"
	rlStats := t.sm.NewStats(key)
	t.sm.AddNearLimit(11, rlStats, detailedKey1)
	t.sm.AddNearLimit(22, rlStats, detailedKey2)

	assert.Equal(test, uint64(33), t.sm.NewStats(key).NearLimit.Value())
	assert.Equal(test, uint64(11), t.sm.NewDetailedStats(detailedKey1).NearLimit.Value())
	assert.Equal(test, uint64(22), t.sm.NewDetailedStats(detailedKey2).NearLimit.Value())
}
func TestDetailedMetricsOverLimit(test *testing.T) {
	t := commonSetup(test, true)
	defer t.controller.Finish()

	key := "hello_world"
	detailedKey1 := "hello_world_detailed1"
	detailedKey2 := "hello_world_detailed2"
	rlStats := t.sm.NewStats(key)
	t.sm.AddOverLimit(11, rlStats, detailedKey1)
	t.sm.AddOverLimit(22, rlStats, detailedKey2)

	assert.Equal(test, uint64(33), t.sm.NewStats(key).OverLimit.Value())
	assert.Equal(test, uint64(11), t.sm.NewDetailedStats(detailedKey1).OverLimit.Value())
	assert.Equal(test, uint64(22), t.sm.NewDetailedStats(detailedKey2).OverLimit.Value())
}
func TestDetailedMetricsOverLimitWithLocalCache(test *testing.T) {
	t := commonSetup(test, true)
	defer t.controller.Finish()

	key := "hello_world"
	detailedKey1 := "hello_world_detailed1"
	detailedKey2 := "hello_world_detailed2"
	rlStats := t.sm.NewStats(key)
	t.sm.AddOverLimitWithLocalCache(11, rlStats, detailedKey1)
	t.sm.AddOverLimitWithLocalCache(22, rlStats, detailedKey2)

	assert.Equal(test, uint64(33), t.sm.NewStats(key).OverLimitWithLocalCache.Value())
	assert.Equal(test, uint64(11), t.sm.NewDetailedStats(detailedKey1).OverLimitWithLocalCache.Value())
	assert.Equal(test, uint64(22), t.sm.NewDetailedStats(detailedKey2).OverLimitWithLocalCache.Value())
}
func TestDetailedMetricsTurnedOff(test *testing.T) {
	t := commonSetup(test, false)
	defer t.controller.Finish()

	key := "hello_world"
	detailedKey1 := "hello_world_detailed1"
	detailedKey2 := "hello_world_detailed2"
	rlStats := t.sm.NewStats(key)
	t.sm.AddOverLimitWithLocalCache(11, rlStats, detailedKey1)
	t.sm.AddOverLimitWithLocalCache(22, rlStats, detailedKey2)

	assert.Equal(test, uint64(33), t.sm.NewStats(key).OverLimitWithLocalCache.Value())
	assert.Equal(test, uint64(0), t.sm.NewDetailedStats(detailedKey1).OverLimitWithLocalCache.Value())
	assert.Equal(test, uint64(0), t.sm.NewDetailedStats(detailedKey2).OverLimitWithLocalCache.Value())
}

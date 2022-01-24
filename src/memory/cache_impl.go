package memory

import (
	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
	"github.com/envoyproxy/ratelimit/src/utils"
	gostats "github.com/lyft/gostats"
	"golang.org/x/net/context"
	"math/rand"
	"sync"
)

type keyCounters struct {
	dividerCounters    map[int64]map[string]uint32 // divider to (key to counter)
	dividerKeySegments map[int64]int64             // divider to keySegment
	segmentLock        sync.Mutex
}

type memoryCacheImpl struct {
	baseRateLimiter *limiter.BaseRateLimiter
	counters        keyCounters
}

func (this *keyCounters) update(unit pb.RateLimitResponse_RateLimit_Unit, now int64, keyName string, hitsAddend uint32) (uint32, uint32) {
	divider := utils.UnitToDivider(unit)
	keySegment := (now / divider) * divider

	this.segmentLock.Lock()
	currentKeySegment := this.dividerKeySegments[divider]
	if currentKeySegment == 0 {
		this.dividerKeySegments[divider] = keySegment
	} else if currentKeySegment != keySegment {
		this.dividerKeySegments[divider] = keySegment
		this.dividerCounters[divider] = make(map[string]uint32)
	}

	counters := this.dividerCounters[keySegment]
	if counters == nil {
		counters = make(map[string]uint32)
		this.dividerCounters[keySegment] = counters
	}
	limitBeforeIncrease := counters[keyName]
	limitAfterIncrease := limitBeforeIncrease + hitsAddend
	counters[keyName] = limitAfterIncrease
	this.segmentLock.Unlock()

	return limitBeforeIncrease, limitAfterIncrease
}

func (this *memoryCacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus {

	result := make([]*pb.RateLimitResponse_DescriptorStatus, len(limits))
	hitsAddend := utils.Max(1, request.HitsAddend)
	cacheKeys, now := this.baseRateLimiter.GenerateCacheKeys(request, limits, hitsAddend)

	for idx, limit := range limits {
		keyName := cacheKeys[idx].Key
		var limitBeforeIncrease, limitAfterIncrease uint32
		if limit != nil {
			limitBeforeIncrease, limitAfterIncrease = this.counters.update(limit.Limit.Unit, now, keyName, hitsAddend)
		} else {
			limitBeforeIncrease, limitAfterIncrease = 0, 1
		}

		limitInfo := limiter.NewRateLimitInfo(limit, limitBeforeIncrease, limitAfterIncrease, 0, 0)
		result[idx] = this.baseRateLimiter.GetResponseDescriptorStatus(keyName,
			limitInfo, false, request.HitsAddend)
	}

	return result
}

func (this *memoryCacheImpl) Flush() {}

func NewRateLimiterCacheImplFromSettings(s settings.Settings, timeSource utils.TimeSource, jitterRand *rand.Rand,
	localCache *freecache.Cache, scope gostats.Scope, statsManager stats.Manager) limiter.RateLimitCache {
	return &memoryCacheImpl{
		baseRateLimiter: limiter.NewBaseRateLimit(timeSource, jitterRand, s.ExpirationJitterMaxSeconds, localCache, s.NearLimitRatio, s.CacheKeyPrefix, statsManager),
		counters:        keyCounters{segmentLock: sync.Mutex{}, dividerCounters: make(map[int64]map[string]uint32), dividerKeySegments: make(map[int64]int64)},
	}
}

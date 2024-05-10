// The memcached limiter uses GetMulti() to check keys in parallel and then does
// increments asynchronously in the backend, since the memcache interface doesn't
// support multi-increment and it seems worthwhile to minimize the number of
// concurrent or sequential RPCs in the critical path.
//
// Another difference from redis is that memcache doesn't create a key implicitly by
// incrementing a missing entry. Instead, when increment fails an explicit "add" needs
// to be called. The process of increment becomes a bit of a dance since we try to
// limit the number of RPCs. First we call increment, then add if the increment
// failed, then increment again if the add failed (which could happen if there was
// a race to call "add").
//
// Note that max memcache key length is 250 characters. Attempting to get or increment
// a longer key will return memcache.ErrMalformedKey

package memcached

import (
	"context"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ratelimit/src/stats"

	"github.com/coocood/freecache"
	gostats "github.com/lyft/gostats"

	"github.com/bradfitz/gomemcache/memcache"

	logger "github.com/sirupsen/logrus"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/srv"
	"github.com/envoyproxy/ratelimit/src/utils"
)

var tracer = otel.Tracer("memcached.cacheImpl")

type rateLimitMemcacheImpl struct {
	client                     Client
	timeSource                 utils.TimeSource
	jitterRand                 *rand.Rand
	expirationJitterMaxSeconds int64
	localCache                 *freecache.Cache
	waitGroup                  sync.WaitGroup
	nearLimitRatio             float32
	baseRateLimiter            *limiter.BaseRateLimiter
}

var AutoFlushForIntegrationTests bool = false

var _ limiter.RateLimitCache = (*rateLimitMemcacheImpl)(nil)

func (this *rateLimitMemcacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit,
) []*pb.RateLimitResponse_DescriptorStatus {
	logger.Debugf("starting cache lookup")

	// request.HitsAddend could be 0 (default value) if not specified by the caller in the Ratelimit request.
	hitsAddend := utils.Max(1, request.HitsAddend)

	// First build a list of all cache keys that we are actually going to hit.
	cacheKeys := this.baseRateLimiter.GenerateCacheKeys(request, limits, hitsAddend)

	isOverLimitWithLocalCache := make([]bool, len(request.Descriptors))

	keysToGet := make([]string, 0, len(request.Descriptors))

	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" {
			continue
		}

		// Check if key is over the limit in local cache.
		if this.baseRateLimiter.IsOverLimitWithLocalCache(cacheKey.Key) {
			isOverLimitWithLocalCache[i] = true
			logger.Debugf("cache key is over the limit: %s", cacheKey.Key)
			continue
		}

		logger.Debugf("looking up cache key: %s", cacheKey.Key)
		keysToGet = append(keysToGet, cacheKey.Key)
	}

	// Generate trace
	_, span := tracer.Start(ctx, "Memcached Fetch Execution",
		trace.WithAttributes(
			attribute.Int("keysToGet length", len(keysToGet)),
		),
	)
	defer span.End()

	// Now fetch from memcache.
	responseDescriptorStatuses := make([]*pb.RateLimitResponse_DescriptorStatus,
		len(request.Descriptors))

	var memcacheValues map[string]*memcache.Item
	var err error

	if len(keysToGet) > 0 {
		memcacheValues, err = this.client.GetMulti(keysToGet)
		if err != nil {
			logger.Errorf("Error multi-getting memcache keys (%s): %s", keysToGet, err)
		}
	}

	for i, cacheKey := range cacheKeys {

		rawMemcacheValue, ok := memcacheValues[cacheKey.Key]
		var limitBeforeIncrease uint32
		if ok {
			decoded, err := strconv.ParseInt(string(rawMemcacheValue.Value), 10, 32)
			if err != nil {
				logger.Errorf("Unexpected non-numeric value in memcached: %v", rawMemcacheValue)
			} else {
				limitBeforeIncrease = uint32(decoded)
			}

		}

		limitAfterIncrease := limitBeforeIncrease + hitsAddend

		limitInfo := limiter.NewRateLimitInfo(limits[i], limitBeforeIncrease, limitAfterIncrease, 0, 0)

		responseDescriptorStatuses[i] = this.baseRateLimiter.GetResponseDescriptorStatus(cacheKey.Key,
			limitInfo, isOverLimitWithLocalCache[i], hitsAddend)
	}

	this.waitGroup.Add(1)
	runAsync(func() { this.increaseAsync(cacheKeys, isOverLimitWithLocalCache, limits, uint64(hitsAddend)) })
	if AutoFlushForIntegrationTests {
		this.Flush()
	}

	return responseDescriptorStatuses
}

func (this *rateLimitMemcacheImpl) increaseAsync(cacheKeys []limiter.CacheKey, isOverLimitWithLocalCache []bool,
	limits []*config.RateLimit, hitsAddend uint64,
) {
	defer this.waitGroup.Done()
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" || isOverLimitWithLocalCache[i] {
			continue
		}

		_, err := this.client.Increment(cacheKey.Key, hitsAddend)
		if err == memcache.ErrCacheMiss {
			expirationSeconds := utils.UnitToDividerWithMultiplier(limits[i].Limit.Unit, limits[i].Limit.UnitMultiplier)
			if this.expirationJitterMaxSeconds > 0 {
				expirationSeconds += this.jitterRand.Int63n(this.expirationJitterMaxSeconds)
			}

			// Need to add instead of increment.
			err = this.client.Add(&memcache.Item{
				Key:        cacheKey.Key,
				Value:      []byte(strconv.FormatUint(hitsAddend, 10)),
				Expiration: int32(expirationSeconds),
			})
			if err == memcache.ErrNotStored {
				// There was a race condition to do this add. We should be able to increment
				// now instead.
				_, err := this.client.Increment(cacheKey.Key, hitsAddend)
				if err != nil {
					logger.Errorf("Failed to increment key %s after failing to add: %s", cacheKey.Key, err)
					continue
				}
			} else if err != nil {
				logger.Errorf("Failed to add key %s: %s", cacheKey.Key, err)
				continue
			}
		} else if err != nil {
			logger.Errorf("Failed to increment key %s: %s", cacheKey.Key, err)
			continue
		}
	}
}

func (this *rateLimitMemcacheImpl) Flush() {
	this.waitGroup.Wait()
}

func refreshServersPeriodically(serverList *memcache.ServerList, srv string, d time.Duration, resolver srv.SrvResolver, finish <-chan struct{}) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			err := refreshServers(serverList, srv, resolver)
			if err != nil {
				logger.Warn("failed to refresh memcahce hosts")
			} else {
				logger.Debug("refreshed memcache hosts")
			}
		case <-finish:
			return
		}
	}
}

func refreshServers(serverList *memcache.ServerList, srv string, resolver srv.SrvResolver) error {
	servers, err := resolver.ServerStringsFromSrv(srv)
	if err != nil {
		return err
	}
	err = serverList.SetServers(servers...)
	if err != nil {
		return err
	}
	return nil
}

func newMemcachedFromSrv(srv string, d time.Duration, resolver srv.SrvResolver) Client {
	serverList := new(memcache.ServerList)
	err := refreshServers(serverList, srv, resolver)
	if err != nil {
		errorText := "Unable to fetch servers from SRV"
		logger.Errorf(errorText)
		panic(MemcacheError(errorText))
	}

	if d > 0 {
		logger.Infof("refreshing memcache hosts every: %v milliseconds", d.Milliseconds())
		finish := make(chan struct{})
		go refreshServersPeriodically(serverList, srv, d, resolver, finish)
	} else {
		logger.Debugf("not periodically refreshing memcached hosts")
	}

	return memcache.NewFromSelector(serverList)
}

func newMemcacheFromSettings(s settings.Settings) Client {
	if s.MemcacheSrv != "" && len(s.MemcacheHostPort) > 0 {
		panic(MemcacheError("Both MEMCADHE_HOST_PORT and MEMCACHE_SRV are set"))
	}
	if s.MemcacheSrv != "" {
		logger.Debugf("Using MEMCACHE_SRV: %v", s.MemcacheSrv)
		return newMemcachedFromSrv(s.MemcacheSrv, s.MemcacheSrvRefresh, new(srv.DnsSrvResolver))
	}
	logger.Debugf("Usng MEMCACHE_HOST_PORT:: %v", s.MemcacheHostPort)
	client := memcache.New(s.MemcacheHostPort...)
	client.MaxIdleConns = s.MemcacheMaxIdleConns
	return client
}

var taskQueue = make(chan func())

func runAsync(task func()) {
	select {
	case taskQueue <- task:
		// submitted, everything is ok

	default:
		go func() {
			// do the given task
			task()

			tasksProcessedWithinOnePeriod := 0
			const tickDuration = 10 * time.Second
			tick := time.NewTicker(tickDuration)
			defer tick.Stop()

			for {
				select {
				case t := <-taskQueue:
					t()
					tasksProcessedWithinOnePeriod++
				case <-tick.C:
					if tasksProcessedWithinOnePeriod > 0 {
						tasksProcessedWithinOnePeriod = 0
						continue
					}
					return
				}
			}
		}()
	}
}

func NewRateLimitCacheImpl(client Client, timeSource utils.TimeSource, jitterRand *rand.Rand,
	expirationJitterMaxSeconds int64, localCache *freecache.Cache, statsManager stats.Manager, nearLimitRatio float32, cacheKeyPrefix string,
) limiter.RateLimitCache {
	return &rateLimitMemcacheImpl{
		client:                     client,
		timeSource:                 timeSource,
		jitterRand:                 jitterRand,
		expirationJitterMaxSeconds: expirationJitterMaxSeconds,
		localCache:                 localCache,
		nearLimitRatio:             nearLimitRatio,
		baseRateLimiter:            limiter.NewBaseRateLimit(timeSource, jitterRand, expirationJitterMaxSeconds, localCache, nearLimitRatio, cacheKeyPrefix, statsManager),
	}
}

func NewRateLimitCacheImplFromSettings(s settings.Settings, timeSource utils.TimeSource, jitterRand *rand.Rand,
	localCache *freecache.Cache, scope gostats.Scope, statsManager stats.Manager,
) limiter.RateLimitCache {
	return NewRateLimitCacheImpl(
		CollectStats(newMemcacheFromSettings(s), scope.Scope("memcache")),
		timeSource,
		jitterRand,
		s.ExpirationJitterMaxSeconds,
		localCache,
		statsManager,
		s.NearLimitRatio,
		s.CacheKeyPrefix,
	)
}

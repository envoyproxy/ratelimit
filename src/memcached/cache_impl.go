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
	"fmt"
	"math/rand"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/coocood/freecache"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/utils"
	stats "github.com/lyft/gostats"
)

var AutoFlushForIntegrationTests bool = false

func NewRateLimitCacheImplFromSettings(s settings.Settings, timeSource utils.TimeSource, jitterRand *rand.Rand,
	localCache *freecache.Cache, scope stats.Scope) (limiter.RateLimitCache, error) {
	if s.RateLimitAlgorithm == settings.FixedRateLimit {
		return NewFixedRateLimitCacheImpl(
			memcache.New(s.MemcacheHostPort),
			timeSource,
			jitterRand,
			s.ExpirationJitterMaxSeconds,
			localCache,
			scope,
			s.NearLimitRatio,
			s.CacheKeyPrefix), nil
	}
	if s.RateLimitAlgorithm == settings.WindowedRateLimit {

		return NewWindowedRateLimitCacheImpl(
			memcache.New(s.MemcacheHostPort),
			timeSource,
			jitterRand,
			s.ExpirationJitterMaxSeconds,
			localCache,
			scope,
			s.NearLimitRatio,
			s.CacheKeyPrefix), nil
	}
	return nil, fmt.Errorf("Unknown rate limit algorithm. %s\n", s.RateLimitAlgorithm)
}

// Package redis_decay implements limiter.RateLimitCache with a continuously
// decaying counter (GCRA family) executed atomically in Redis via EVAL.
// Unlike the fixed-window backend there is no window timestamp in the cache
// key, so there is no boundary at which the budget resets and the classic
// 2x boundary-burst cannot occur (see issue #32).
package redis_decay

import (
	"io"
	"math"
	"strconv"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"golang.org/x/net/context"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/utils"
)

// Counts are stored and returned as integer milli-counts so the existing
// redis driver's uint64 result decoding can be reused unchanged.
const decayScript = `
local data = redis.call('GET', KEYS[1])
local now = tonumber(ARGV[1])
local period = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local addend = tonumber(ARGV[4])
local count = addend
if data then
  local sep = string.find(data, ':')
  if sep then
    local prev_ts = tonumber(string.sub(data, 1, sep-1))
    local prev_count = tonumber(string.sub(data, sep+1))
    if prev_ts and prev_count then
      local passed = math.max(now - prev_ts, 0)
      local unused = passed / period * limit * 1000.0
      count = math.max(prev_count - unused, 0) + addend
    end
  end
end
redis.call('SET', KEYS[1], now .. ':' .. count, 'EX', math.ceil(period/1000))
return math.floor(count)
`

type decayRateLimitCacheImpl struct {
	client     redis.Client
	timeSource utils.TimeSource
	prefix     string
}

func NewDecayRateLimitCacheImpl(client redis.Client, timeSource utils.TimeSource, cacheKeyPrefix string) limiter.RateLimitCache {
	return &decayRateLimitCacheImpl{client: client, timeSource: timeSource, prefix: cacheKeyPrefix}
}

// NewFromSettings mirrors redis.NewRateLimiterCacheImplFromSettings's client
// construction (single pool; the per-second pool split is a fixed-window
// optimization that does not apply to a decaying counter).
func NewFromSettings(ctx context.Context, s settings.Settings, srv server.Server, timeSource utils.TimeSource) (limiter.RateLimitCache, io.Closer) {
	client := redis.NewClientImpl(ctx, srv.Scope().Scope("redis_decay_pool"), s.RedisTls, s.RedisAuth, s.RedisSocketType,
		s.RedisType, s.RedisUrl, s.RedisPoolSize,
		s.RedisPipelineWindow, s.RedisPipelineLimit, s.RedisTlsConfig, s.RedisHealthCheckActiveConnection, srv, s.RedisTimeout,
		s.RedisPoolOnEmptyBehavior, s.RedisSentinelAuth,
		s.RedisStartupInitialInterval, s.RedisStartupMaxInterval, s.RedisStartupMaxElapsedTime,
		s.RedisCloseConnectionOnReadOnlyError)
	return NewDecayRateLimitCacheImpl(client, timeSource, s.CacheKeyPrefix), client
}

func (this *decayRateLimitCacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit,
) []*pb.RateLimitResponse_DescriptorStatus {
	statuses := make([]*pb.RateLimitResponse_DescriptorStatus, len(request.Descriptors))
	results := make([]uint64, len(request.Descriptors))
	var pipeline redis.Pipeline

	nowMs := this.timeSource.UnixNow() * 1000
	hitsAddends := utils.GetHitsAddends(request)

	for i, descriptor := range request.Descriptors {
		statuses[i] = &pb.RateLimitResponse_DescriptorStatus{Code: pb.RateLimitResponse_OK, LimitRemaining: math.MaxUint32}
		if limits[i] == nil {
			continue
		}
		// Key without a window timestamp — the decaying value itself carries time.
		key := this.prefix + "decay_" + request.Domain
		for _, entry := range descriptor.Entries {
			key += "_" + entry.Key + "_" + entry.Value
		}
		periodMs := utils.UnitToDivider(limits[i].Limit.Unit) * 1000
		if pipeline == nil {
			pipeline = redis.Pipeline{}
		}
		pipeline = this.client.PipeAppendWithRoutingKey(pipeline, key, &results[i], "EVAL", decayScript, 1, key,
			strconv.FormatInt(nowMs, 10), strconv.FormatInt(periodMs, 10),
			strconv.FormatInt(int64(limits[i].Limit.RequestsPerUnit), 10),
			strconv.FormatUint(hitsAddends[i].Value*1000, 10))
	}

	if pipeline != nil {
		if err := this.client.PipeDo(ctx, pipeline); err != nil {
			// Surface the failure exactly as the fixed-window backend does: the
			// service layer recovers it, increments the RedisError stat, and the
			// caller's configured failure-mode policy decides whether to admit.
			// Returning OK here would hide the outage and override that policy.
			panic(redis.RedisError(err.Error()))
		}
	}

	for i := range request.Descriptors {
		if limits[i] == nil {
			continue
		}
		limit := uint64(limits[i].Limit.RequestsPerUnit)
		countMilli := results[i]
		count := countMilli / 1000
		remaining := int64(limit) - int64(count)
		if remaining < 0 {
			remaining = 0
		}
		statuses[i].CurrentLimit = limits[i].Limit
		statuses[i].LimitRemaining = uint32(remaining)
		statuses[i].DurationUntilReset = &durationpb.Duration{Seconds: utils.UnitToDivider(limits[i].Limit.Unit)}
		if countMilli > limit*1000 {
			statuses[i].Code = pb.RateLimitResponse_OVER_LIMIT
			limits[i].Stats.OverLimit.Add(1)
		} else {
			limits[i].Stats.WithinLimit.Add(1)
		}
		limits[i].Stats.TotalHits.Add(1)
	}
	return statuses
}

func (this *decayRateLimitCacheImpl) Flush() {}

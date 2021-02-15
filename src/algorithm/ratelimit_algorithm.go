package algorithm

import (
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/utils"
	"github.com/golang/protobuf/ptypes/duration"
)

type RatelimitAlgorithm interface {
	CalculateSimpleReset(limit *config.RateLimit, timeSource utils.TimeSource) *duration.Duration
	CalculateReset(isOverLimit bool, limit *config.RateLimit, timeSource utils.TimeSource) *duration.Duration
	IsOverLimit(limit *config.RateLimit, results int64, hitsAddend int64) (bool, int64, int)
	GetExpirationSeconds() int64
	GetResultsAfterIncrease() int64
}

package ratelimit

import (
	"fmt"
	"github.com/envoyproxy/ratelimit/src/stats"
	"math"
	"strings"
	"sync"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/assert"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/lyft/goruntime/loader"
	logger "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

type RateLimitServiceServer interface {
	pb.RateLimitServiceServer
	GetCurrentConfig() config.RateLimitConfig
	GetLegacyService() RateLimitLegacyServiceServer
}

type service struct {
	runtime            loader.IFace
	configLock         sync.RWMutex
	configLoader       config.RateLimitConfigLoader
	config             config.RateLimitConfig
	runtimeUpdateEvent chan int
	cache              limiter.RateLimitCache
	stats              stats.ServiceStats
	legacy             *legacyService
	runtimeWatchRoot   bool
}

func (this *service) reloadConfig(statsManager stats.Manager) {
	defer func() {
		if e := recover(); e != nil {
			configError, ok := e.(config.RateLimitConfigError)
			if !ok {
				panic(e)
			}

			this.stats.ConfigLoadError.Inc()
			logger.Errorf("error loading new configuration from runtime: %s", configError.Error())
		}
	}()

	files := []config.RateLimitConfigToLoad{}
	snapshot := this.runtime.Snapshot()
	for _, key := range snapshot.Keys() {
		if this.runtimeWatchRoot && !strings.HasPrefix(key, "config.") {
			continue
		}

		files = append(files, config.RateLimitConfigToLoad{key, snapshot.Get(key)})
	}

	newConfig := this.configLoader.Load(files, statsManager)
	this.stats.ConfigLoadSuccess.Inc()
	this.configLock.Lock()
	this.config = newConfig
	this.configLock.Unlock()
}

type serviceError string

func (e serviceError) Error() string {
	return string(e)
}

func checkServiceErr(something bool, msg string) {
	if !something {
		panic(serviceError(msg))
	}
}

func (this *service) constructLimitsToCheck(request *pb.RateLimitRequest, ctx context.Context) ([]*config.RateLimit, []bool) {
	snappedConfig := this.GetCurrentConfig()
	checkServiceErr(snappedConfig != nil, "no rate limit configuration loaded")

	limitsToCheck := make([]*config.RateLimit, len(request.Descriptors))
	isUnlimited := make([]bool, len(request.Descriptors))

	for i, descriptor := range request.Descriptors {
		if logger.IsLevelEnabled(logger.DebugLevel) {
			var descriptorEntryStrings []string
			for _, descriptorEntry := range descriptor.GetEntries() {
				descriptorEntryStrings = append(
					descriptorEntryStrings,
					fmt.Sprintf("(%s=%s)", descriptorEntry.Key, descriptorEntry.Value),
				)
			}
			logger.Debugf("got descriptor: %s", strings.Join(descriptorEntryStrings, ","))
		}
		limitsToCheck[i] = snappedConfig.GetLimit(ctx, request.Domain, descriptor)
		if logger.IsLevelEnabled(logger.DebugLevel) {
			if limitsToCheck[i] == nil {
				logger.Debugf("descriptor does not match any limit, no limits applied")
			} else {
				if limitsToCheck[i].Unlimited {
					logger.Debugf("descriptor is unlimited, not passing to the cache")
				} else {
					logger.Debugf(
						"applying limit: %d requests per %s",
						limitsToCheck[i].Limit.RequestsPerUnit,
						limitsToCheck[i].Limit.Unit.String(),
					)
				}
			}
		}

		if limitsToCheck[i] != nil && limitsToCheck[i].Unlimited {
			isUnlimited[i] = true
			limitsToCheck[i] = nil
		}
	}
	return limitsToCheck, isUnlimited
}

func (this *service) shouldRateLimitWorker(
	ctx context.Context, request *pb.RateLimitRequest) *pb.RateLimitResponse {

	checkServiceErr(request.Domain != "", "rate limit domain must not be empty")
	checkServiceErr(len(request.Descriptors) != 0, "rate limit descriptor list must not be empty")

	limitsToCheck, isUnlimited := this.constructLimitsToCheck(request, ctx)

	responseDescriptorStatuses := this.cache.DoLimit(ctx, request, limitsToCheck)
	assert.Assert(len(limitsToCheck) == len(responseDescriptorStatuses))

	response := &pb.RateLimitResponse{}
	response.Statuses = make([]*pb.RateLimitResponse_DescriptorStatus, len(request.Descriptors))
	finalCode := pb.RateLimitResponse_OK
	for i, descriptorStatus := range responseDescriptorStatuses {
		if isUnlimited[i] {
			response.Statuses[i] = &pb.RateLimitResponse_DescriptorStatus{
				Code:           pb.RateLimitResponse_OK,
				LimitRemaining: math.MaxUint32,
			}
		} else {
			response.Statuses[i] = descriptorStatus
			if descriptorStatus.Code == pb.RateLimitResponse_OVER_LIMIT {
				finalCode = descriptorStatus.Code
			}
		}
	}

	response.OverallCode = finalCode
	return response
}

func (this *service) ShouldRateLimit(
	ctx context.Context,
	request *pb.RateLimitRequest) (finalResponse *pb.RateLimitResponse, finalError error) {

	defer func() {
		err := recover()
		if err == nil {
			return
		}

		logger.Debugf("caught error during call")
		finalResponse = nil
		switch t := err.(type) {
		case redis.RedisError:
			{
				this.stats.ShouldRateLimit.RedisError.Inc()
				finalError = t
			}
		case serviceError:
			{
				this.stats.ShouldRateLimit.ServiceError.Inc()
				finalError = t
			}
		default:
			panic(err)
		}
	}()

	response := this.shouldRateLimitWorker(ctx, request)
	logger.Debugf("returning normal response")
	return response, nil
}

func (this *service) GetLegacyService() RateLimitLegacyServiceServer {
	return this.legacy
}

func (this *service) GetCurrentConfig() config.RateLimitConfig {
	this.configLock.RLock()
	defer this.configLock.RUnlock()
	return this.config
}

func NewService(runtime loader.IFace, cache limiter.RateLimitCache,
	configLoader config.RateLimitConfigLoader, statsManager stats.Manager, runtimeWatchRoot bool) RateLimitServiceServer {

	newService := &service{
		runtime:            runtime,
		configLock:         sync.RWMutex{},
		configLoader:       configLoader,
		config:             nil,
		runtimeUpdateEvent: make(chan int),
		cache:              cache,
		stats:              statsManager.NewServiceStats(),
		runtimeWatchRoot:   runtimeWatchRoot,
	}
	newService.legacy = &legacyService{
		s:                          newService,
		shouldRateLimitLegacyStats: statsManager.NewShouldRateLimitLegacyStats(),
	}

	runtime.AddUpdateCallback(newService.runtimeUpdateEvent)

	newService.reloadConfig(statsManager)
	go func() {
		// No exit right now.
		for {
			logger.Debugf("waiting for runtime update")
			<-newService.runtimeUpdateEvent
			logger.Debugf("got runtime update and reloading config")
			newService.reloadConfig(statsManager)
		}
	}()

	return newService
}

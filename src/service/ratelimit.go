package ratelimit

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"

	"github.com/envoyproxy/ratelimit/src/utils"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	logger "github.com/sirupsen/logrus"
	"golang.org/x/net/context"

	"github.com/envoyproxy/ratelimit/src/assert"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/provider"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/server"
)

var tracer = otel.Tracer("ratelimit")

type RateLimitServiceServer interface {
	pb.RateLimitServiceServer
	GetCurrentConfig() (config.RateLimitConfig, bool)
	SetConfig(updateEvent provider.ConfigUpdateEvent, healthyWithAtLeastOneConfigLoad bool)
}

type service struct {
	configLock                  sync.RWMutex
	configUpdateEvent           <-chan provider.ConfigUpdateEvent
	config                      config.RateLimitConfig
	cache                       limiter.RateLimitCache
	stats                       stats.ServiceStats
	health                      *server.HealthChecker
	customHeadersEnabled        bool
	customHeaderLimitHeader     string
	customHeaderRemainingHeader string
	customHeaderResetHeader     string
	customHeaderClock           utils.TimeSource
	globalShadowMode            bool
}

func (this *service) SetConfig(updateEvent provider.ConfigUpdateEvent, healthyWithAtLeastOneConfigLoad bool) {
	newConfig, err := updateEvent.GetConfig()
	if err != nil {
		configError, ok := err.(config.RateLimitConfigError)
		if !ok {
			panic(err)
		}

		this.stats.ConfigLoadError.Inc()
		logger.Errorf("Error loading new configuration: %s", configError.Error())
		return
	}

	if healthyWithAtLeastOneConfigLoad {
		err = nil
		if !newConfig.IsEmptyDomains() {
			err = this.health.Ok(server.ConfigHealthComponentName)
		} else {
			err = this.health.Fail(server.ConfigHealthComponentName)
		}
		if err != nil {
			logger.Errorf("Unable to update health status: %s", err)
		}
	}

	this.stats.ConfigLoadSuccess.Inc()

	this.configLock.Lock()
	this.config = newConfig

	rlSettings := settings.NewSettings()
	this.globalShadowMode = rlSettings.GlobalShadowMode

	if rlSettings.RateLimitResponseHeadersEnabled {
		this.customHeadersEnabled = true

		this.customHeaderLimitHeader = rlSettings.HeaderRatelimitLimit

		this.customHeaderRemainingHeader = rlSettings.HeaderRatelimitRemaining

		this.customHeaderResetHeader = rlSettings.HeaderRatelimitReset
	}
	this.configLock.Unlock()
	logger.Info("Successfully loaded new configuration")
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

func (this *service) constructLimitsToCheck(request *pb.RateLimitRequest, ctx context.Context, snappedConfig config.RateLimitConfig) ([]*config.RateLimit, []bool) {
	checkServiceErr(snappedConfig != nil, "no rate limit configuration loaded")

	limitsToCheck := make([]*config.RateLimit, len(request.Descriptors))
	isUnlimited := make([]bool, len(request.Descriptors))

	replacing := make(map[string]bool)

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
						"applying limit: %d requests per %s, shadow_mode: %t",
						limitsToCheck[i].Limit.RequestsPerUnit,
						limitsToCheck[i].Limit.Unit.String(),
						limitsToCheck[i].ShadowMode,
					)
				}
			}
		}

		if limitsToCheck[i] != nil {
			for _, replace := range limitsToCheck[i].Replaces {
				replacing[replace] = true
			}

			if limitsToCheck[i].Unlimited {
				isUnlimited[i] = true
				limitsToCheck[i] = nil
			}
		}
	}

	for i, limit := range limitsToCheck {
		if limit == nil || limit.Name == "" {
			continue
		}
		_, exists := replacing[limit.Name]
		if exists {
			limitsToCheck[i] = nil
			if logger.IsLevelEnabled(logger.DebugLevel) {
				logger.Debugf("replacing %s", limit.Name)
			}
		}
	}
	return limitsToCheck, isUnlimited
}

const MaxUint32 = uint32(1<<32 - 1)

func (this *service) shouldRateLimitWorker(
	ctx context.Context, request *pb.RateLimitRequest) *pb.RateLimitResponse {

	checkServiceErr(request.Domain != "", "rate limit domain must not be empty")
	checkServiceErr(len(request.Descriptors) != 0, "rate limit descriptor list must not be empty")

	snappedConfig, globalShadowMode := this.GetCurrentConfig()
	limitsToCheck, isUnlimited := this.constructLimitsToCheck(request, ctx, snappedConfig)

	responseDescriptorStatuses := this.cache.DoLimit(ctx, request, limitsToCheck)
	assert.Assert(len(limitsToCheck) == len(responseDescriptorStatuses))

	response := &pb.RateLimitResponse{}
	response.Statuses = make([]*pb.RateLimitResponse_DescriptorStatus, len(request.Descriptors))
	finalCode := pb.RateLimitResponse_OK

	// Keep track of the descriptor which is closest to hit the ratelimit
	minLimitRemaining := MaxUint32
	var minimumDescriptor *pb.RateLimitResponse_DescriptorStatus = nil

	for i, descriptorStatus := range responseDescriptorStatuses {
		// Keep track of the descriptor closest to hit the ratelimit
		if this.customHeadersEnabled &&
			descriptorStatus.CurrentLimit != nil &&
			descriptorStatus.LimitRemaining < minLimitRemaining {
			minimumDescriptor = descriptorStatus
			minLimitRemaining = descriptorStatus.LimitRemaining
		}

		if isUnlimited[i] {
			response.Statuses[i] = &pb.RateLimitResponse_DescriptorStatus{
				Code:           pb.RateLimitResponse_OK,
				LimitRemaining: math.MaxUint32,
			}
		} else {
			response.Statuses[i] = descriptorStatus
			if descriptorStatus.Code == pb.RateLimitResponse_OVER_LIMIT {
				finalCode = descriptorStatus.Code

				minimumDescriptor = descriptorStatus
				minLimitRemaining = 0
			}
		}
	}

	// Add Headers if requested
	if this.customHeadersEnabled && minimumDescriptor != nil {
		response.ResponseHeadersToAdd = []*core.HeaderValue{
			this.rateLimitLimitHeader(minimumDescriptor),
			this.rateLimitRemainingHeader(minimumDescriptor),
			this.rateLimitResetHeader(minimumDescriptor),
		}
	}

	// If there is a global shadow_mode, it should always return OK
	if finalCode == pb.RateLimitResponse_OVER_LIMIT && globalShadowMode {
		finalCode = pb.RateLimitResponse_OK
		this.stats.GlobalShadowMode.Inc()
	}

	response.OverallCode = finalCode
	return response
}

func (this *service) rateLimitLimitHeader(descriptor *pb.RateLimitResponse_DescriptorStatus) *core.HeaderValue {
	// Limit header only provides the mandatory part from the spec, the actual limit
	// the optional quota policy is currently not provided
	return &core.HeaderValue{
		Key:   this.customHeaderLimitHeader,
		Value: strconv.FormatUint(uint64(descriptor.CurrentLimit.RequestsPerUnit), 10),
	}
}

func (this *service) rateLimitRemainingHeader(descriptor *pb.RateLimitResponse_DescriptorStatus) *core.HeaderValue {
	// How much of the limit is remaining
	return &core.HeaderValue{
		Key:   this.customHeaderRemainingHeader,
		Value: strconv.FormatUint(uint64(descriptor.LimitRemaining), 10),
	}
}

func (this *service) rateLimitResetHeader(
	descriptor *pb.RateLimitResponse_DescriptorStatus) *core.HeaderValue {

	return &core.HeaderValue{
		Key:   this.customHeaderResetHeader,
		Value: strconv.FormatInt(utils.CalculateReset(&descriptor.CurrentLimit.Unit, this.customHeaderClock).GetSeconds(), 10),
	}
}

func (this *service) ShouldRateLimit(
	ctx context.Context,
	request *pb.RateLimitRequest) (finalResponse *pb.RateLimitResponse, finalError error) {

	// Generate trace
	_, span := tracer.Start(ctx, "ShouldRateLimit Execution",
		trace.WithAttributes(
			attribute.String("domain", request.Domain),
			attribute.String("request string", request.String()),
		),
	)
	defer span.End()

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

func (this *service) GetCurrentConfig() (config.RateLimitConfig, bool) {
	this.configLock.RLock()
	defer this.configLock.RUnlock()
	return this.config, this.globalShadowMode
}

func NewService(cache limiter.RateLimitCache, configProvider provider.RateLimitConfigProvider, statsManager stats.Manager,
	health *server.HealthChecker, clock utils.TimeSource, shadowMode, forceStart bool, healthyWithAtLeastOneConfigLoad bool) RateLimitServiceServer {

	newService := &service{
		configLock:        sync.RWMutex{},
		configUpdateEvent: configProvider.ConfigUpdateEvent(),
		config:            nil,
		cache:             cache,
		stats:             statsManager.NewServiceStats(),
		health:            health,
		globalShadowMode:  shadowMode,
		customHeaderClock: clock,
	}

	if !forceStart {
		logger.Info("Waiting for initial ratelimit config update event")
		newService.SetConfig(<-newService.configUpdateEvent, healthyWithAtLeastOneConfigLoad)
		logger.Info("Successfully loaded the initial ratelimit configs")
	}

	go func() {
		for {
			logger.Debug("Waiting for config update event")
			updateEvent := <-newService.configUpdateEvent
			logger.Debug("Setting config retrieved from config provider")
			newService.SetConfig(updateEvent, healthyWithAtLeastOneConfigLoad)
		}
	}()

	return newService
}

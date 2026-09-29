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
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"

	"github.com/envoyproxy/ratelimit/src/utils"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
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
	GetCurrentConfig() (config.RateLimitConfig, bool, bool)
	SetConfig(updateEvent provider.ConfigUpdateEvent, healthyWithAtLeastOneConfigLoad bool)
}

type service struct {
	configLock                     sync.RWMutex
	configUpdateEvent              <-chan provider.ConfigUpdateEvent
	config                         config.RateLimitConfig
	cache                          limiter.RateLimitCache
	stats                          stats.ServiceStats
	health                         *server.HealthChecker
	customHeadersEnabled           bool
	customHeaderLimitHeader        string
	customHeaderRemainingHeader    string
	customHeaderResetHeader        string
	customHeaderClock              utils.TimeSource
	requestHeadersEnabled          bool
	requestHeaderLimitHeader       string
	requestHeaderRemainingHeader   string
	requestHeaderResetHeader       string
	globalShadowMode               bool
	globalQuotaMode                bool
	responseDynamicMetadataEnabled bool
	useCalendarMonthRateLimit      bool
	allDescriptorsHeadersEnabled   bool
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
	this.globalQuotaMode = rlSettings.GlobalQuotaMode
	this.responseDynamicMetadataEnabled = rlSettings.ResponseDynamicMetadata
	this.useCalendarMonthRateLimit = rlSettings.UseCalendarMonthRateLimit
	this.allDescriptorsHeadersEnabled = rlSettings.RateLimitAllDescriptorsHeadersEnabled

	this.customHeadersEnabled = rlSettings.RateLimitResponseHeadersEnabled
	if rlSettings.RateLimitResponseHeadersEnabled {
		this.customHeaderLimitHeader = rlSettings.HeaderRatelimitLimit

		this.customHeaderRemainingHeader = rlSettings.HeaderRatelimitRemaining

		this.customHeaderResetHeader = rlSettings.HeaderRatelimitReset
	}

	this.requestHeadersEnabled = rlSettings.RateLimitRequestHeadersEnabled
	if rlSettings.RateLimitRequestHeadersEnabled {
		this.requestHeaderLimitHeader = rlSettings.HeaderRequestRatelimitLimit
		this.requestHeaderRemainingHeader = rlSettings.HeaderRequestRatelimitRemaining
		this.requestHeaderResetHeader = rlSettings.HeaderRequestRatelimitReset
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
						"applying limit: %d requests per %s, shadow_mode: %t, quota: %t",
						limitsToCheck[i].Limit.RequestsPerUnit,
						limitsToCheck[i].Limit.Unit.String(),
						limitsToCheck[i].ShadowMode,
						limitsToCheck[i].QuotaMode,
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

// Descriptor entry keys used to identify a quota enforcement group (one model).
const (
	backendNameDescriptorKey       = "backend_name"
	modelNameOverrideDescriptorKey = "model_name_override"
)

func (this *service) shouldRateLimitWorker(
	ctx context.Context, request *pb.RateLimitRequest,
) *pb.RateLimitResponse {
	checkServiceErr(request.Domain != "", "rate limit domain must not be empty")
	checkServiceErr(len(request.Descriptors) != 0, "rate limit descriptor list must not be empty")

	snappedConfig, globalShadowMode, globalQuotaMode := this.GetCurrentConfig()
	limitsToCheck, isUnlimited := this.constructLimitsToCheck(request, ctx, snappedConfig)

	assert.Assert(len(limitsToCheck) == len(isUnlimited))
	assert.Assert(len(limitsToCheck) == len(request.Descriptors))

	responseDescriptorStatuses := this.cache.DoLimit(ctx, request, limitsToCheck)
	logger.Debugf("descriptor statuses: %+v", responseDescriptorStatuses)
	assert.Assert(len(limitsToCheck) == len(responseDescriptorStatuses))

	response := &pb.RateLimitResponse{}
	response.Statuses = make([]*pb.RateLimitResponse_DescriptorStatus, len(request.Descriptors))

	// Keep track of the descriptor which is closest to hit the ratelimit
	minLimitRemaining := MaxUint32
	var minimumDescriptor *pb.RateLimitResponse_DescriptorStatus = nil

	// Track quota mode violations for metadata
	var passedDescriptors []int
	failedRateLimitDescriptors := 0

	// Quota-mode descriptors are grouped by their enforcement scope (the model,
	// identified by the backend_name + model_name_override descriptor entries).
	// A group is over the limit if ANY of its descriptors is over
	// (e.g. the per-tenant bucket OR the model's default bucket).
	// The overall request is OVER_LIMIT only when EVERY quota group is
	// over, which preserves cross-model failover while still enforcing each
	// model's buckets independently.
	type quotaGroupState struct {
		over bool
	}
	quotaGroups := make(map[string]*quotaGroupState)
	// descriptorGroupKey[i] holds the group key for quota descriptor i (empty for
	// non-quota descriptors) so that passed descriptors belonging to an exhausted
	// group can be excluded from the failover metadata below.
	descriptorGroupKey := make([]string, len(responseDescriptorStatuses))

	for i, descriptorStatus := range responseDescriptorStatuses {
		// Keep track of the descriptor closest to hit the ratelimit
		if (this.customHeadersEnabled || this.requestHeadersEnabled) &&
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
			continue
		}

		response.Statuses[i] = descriptorStatus
		isQuotaMode := globalQuotaMode || (limitsToCheck[i] != nil && limitsToCheck[i].QuotaMode)
		over := descriptorStatus.Code == pb.RateLimitResponse_OVER_LIMIT
		if !over {
			// Keep track of the descriptors that have passed
			passedDescriptors = append(passedDescriptors, i)
		}

		if isQuotaMode {
			groupKey := quotaGroupKey(request.Descriptors[i])
			descriptorGroupKey[i] = groupKey
			group := quotaGroups[groupKey]
			if group == nil {
				group = &quotaGroupState{}
				quotaGroups[groupKey] = group
			}
			// OR the statuses within a quota group.
			if over {
				group.over = true
			}
		} else if over {
			failedRateLimitDescriptors += 1
			minimumDescriptor = descriptorStatus
			minLimitRemaining = 0
		}
	}

	// Quota is over the limit only when there is at least one quota group and
	// every quota group is over its limit (AND across model groups → failover).
	quotaOverLimit := len(quotaGroups) > 0
	for _, group := range quotaGroups {
		if !group.over {
			quotaOverLimit = false
			break
		}
	}

	finalCode := pb.RateLimitResponse_OK
	// The final code is OVER_LIMIT iff at least one non-quota rate limit descriptor
	// is over the limit, or every quota group is over its limit.
	if failedRateLimitDescriptors > 0 || quotaOverLimit {
		finalCode = pb.RateLimitResponse_OVER_LIMIT
	}

	// Add Headers if requested
	if this.allDescriptorsHeadersEnabled {
		response.ResponseHeadersToAdd = this.allDescriptorsHeaders(responseDescriptorStatuses)
	}
	if this.customHeadersEnabled && minimumDescriptor != nil {
		response.ResponseHeadersToAdd = append(response.ResponseHeadersToAdd,
			this.rateLimitLimitHeader(minimumDescriptor),
			this.rateLimitRemainingHeader(minimumDescriptor),
			this.rateLimitResetHeader(minimumDescriptor),
		)
	}

	// Add request headers if requested
	if this.requestHeadersEnabled && minimumDescriptor != nil {
		response.RequestHeadersToAdd = []*core.HeaderValue{
			this.rateLimitRequestLimitHeader(minimumDescriptor),
			this.rateLimitRequestRemainingHeader(minimumDescriptor),
			this.rateLimitRequestResetHeader(minimumDescriptor),
		}
	}

	// If there is a global shadow_mode, it should always return OK
	if finalCode == pb.RateLimitResponse_OVER_LIMIT && globalShadowMode {
		finalCode = pb.RateLimitResponse_OK
		this.stats.GlobalShadowMode.Inc()
	}

	// If response dynamic data enabled, set dynamic data on response.
	if this.responseDynamicMetadataEnabled {
		// Only advertise descriptors that still have quota. A passed descriptor
		// whose quota group is exhausted must not be offered as an available routing target.
		availableDescriptors := passedDescriptors
		if len(quotaGroups) > 0 {
			availableDescriptors = make([]int, 0, len(passedDescriptors))
			for _, idx := range passedDescriptors {
				if groupKey := descriptorGroupKey[idx]; groupKey != "" {
					if group := quotaGroups[groupKey]; group != nil && group.over {
						continue
					}
				}
				availableDescriptors = append(availableDescriptors, idx)
			}
		}
		response.DynamicMetadata = ratelimitToMetadata(request, availableDescriptors, limitsToCheck, len(quotaGroups) > 0)
	}

	response.OverallCode = finalCode
	return response
}

func (this *service) allDescriptorsHeaders(
	descriptorStatuses []*pb.RateLimitResponse_DescriptorStatus,
) []*core.HeaderValue {
	headers := make([]*core.HeaderValue, 0, len(descriptorStatuses)*2)
	for _, status := range descriptorStatuses {
		if status.CurrentLimit == nil {
			continue
		}

		unitSuffix := strings.ToLower(status.CurrentLimit.Unit.String()) + "s"
		headers = append(headers,
			&core.HeaderValue{
				Key:   "ratelimit-limit-" + unitSuffix,
				Value: strconv.FormatUint(uint64(status.CurrentLimit.RequestsPerUnit), 10),
			},
			&core.HeaderValue{
				Key:   "ratelimit-remaining-" + unitSuffix,
				Value: strconv.FormatUint(uint64(status.LimitRemaining), 10),
			},
		)
	}

	return headers
}

// quotaGroupKey returns the enforcement scope ("group") for a quota-mode
// descriptor. Descriptors that belong to the same model share a group, so their
// buckets (for example a per-tenant bucket rule and the model's default bucket)
// are evaluated together. The group is identified by the backend_name and
// model_name_override descriptor entries. If neither entry is present (for
// example a service-level catch-all quota), the full entry list is used so that
// unrelated descriptors are never accidentally merged into the same group.
func quotaGroupKey(descriptor *ratelimitv3.RateLimitDescriptor) string {
	var backend, model string
	var all strings.Builder
	for _, entry := range descriptor.GetEntries() {
		switch entry.GetKey() {
		case backendNameDescriptorKey:
			backend = entry.GetValue()
		case modelNameOverrideDescriptorKey:
			model = entry.GetValue()
		}
		all.WriteString(entry.GetKey())
		all.WriteString("=")
		all.WriteString(entry.GetValue())
		all.WriteString(";")
	}
	if backend != "" || model != "" {
		return backend + "|" + model
	}
	return all.String()
}

func ratelimitToMetadata(req *pb.RateLimitRequest, passedDescriptors []int, limitsToCheck []*config.RateLimit, quotaMode bool) *structpb.Struct {
	fields := make(map[string]*structpb.Value)

	// Domain
	fields["domain"] = structpb.NewStringValue(req.Domain)

	// Descriptors
	descriptorsValues := make([]*structpb.Value, 0, len(req.Descriptors))
	for _, descriptor := range req.Descriptors {
		s := descriptorToStruct(descriptor)
		if s == nil {
			continue
		}
		descriptorsValues = append(descriptorsValues, structpb.NewStructValue(s))
	}
	fields["descriptors"] = structpb.NewListValue(&structpb.ListValue{
		Values: descriptorsValues,
	})

	// HitsAddend
	if hitsAddend := req.GetHitsAddend(); hitsAddend != 0 {
		fields["hitsAddend"] = structpb.NewNumberValue(float64(hitsAddend))
	}

	passedMetadata := &structpb.Struct{Fields: make(map[string]*structpb.Value)}
	for _, idx := range passedDescriptors {
		if idx < len(limitsToCheck) {
			limit := limitsToCheck[idx]
			if limit != nil && limit.Metadata != nil {
				mergeMetadata(passedMetadata, limit.Metadata)
			}
		}
	}

	if len(passedMetadata.GetFields()) > 0 {
		fields["metadata"] = structpb.NewStructValue(passedMetadata)
	}

	// In quota mode, advertise the unique (backend_name, model_name_override) pairs
	// that still have quota so the data plane can make a routing decision and send the
	// request to a non-exhausted model/backend pair.
	if quotaMode {
		if backends := passedBackendsList(req, passedDescriptors); len(backends) > 0 {
			fields["passedBackends"] = structpb.NewListValue(&structpb.ListValue{Values: backends})
		}
	}

	return &structpb.Struct{Fields: fields}
}

// passedBackendsList builds the list of (backend_name, model_name_override)
// pairs for the passed quota descriptors. Entries are deduplicated by quotaGroupKey so
// that each model/backend pair appears at most once.
func passedBackendsList(req *pb.RateLimitRequest, passedDescriptors []int) []*structpb.Value {
	seen := make(map[string]bool)
	backends := make([]*structpb.Value, 0, len(passedDescriptors))
	for _, idx := range passedDescriptors {
		if idx < 0 || idx >= len(req.GetDescriptors()) {
			continue
		}
		descriptor := req.Descriptors[idx]
		groupKey := quotaGroupKey(descriptor)
		if seen[groupKey] {
			continue
		}
		var backend, model string
		for _, entry := range descriptor.GetEntries() {
			switch entry.GetKey() {
			case backendNameDescriptorKey:
				backend = entry.GetValue()
			case modelNameOverrideDescriptorKey:
				model = entry.GetValue()
			}
		}
		if backend == "" && model == "" {
			continue
		}
		seen[groupKey] = true
		backends = append(backends, structpb.NewStructValue(&structpb.Struct{
			Fields: map[string]*structpb.Value{
				backendNameDescriptorKey:       structpb.NewStringValue(backend),
				modelNameOverrideDescriptorKey: structpb.NewStringValue(model),
			},
		}))
	}
	return backends
}

func descriptorToStruct(descriptor *ratelimitv3.RateLimitDescriptor) *structpb.Struct {
	if descriptor == nil {
		return nil
	}

	fields := make(map[string]*structpb.Value)

	// Entries
	entriesValues := make([]*structpb.Value, 0, len(descriptor.Entries))
	for _, entry := range descriptor.Entries {
		val := fmt.Sprintf("%s=%s", entry.GetKey(), entry.GetValue())
		entriesValues = append(entriesValues, structpb.NewStringValue(val))
	}
	fields["entries"] = structpb.NewListValue(&structpb.ListValue{
		Values: entriesValues,
	})

	// Limit
	if descriptor.GetLimit() != nil {
		fields["limit"] = structpb.NewStringValue(descriptor.Limit.String())
	}

	// HitsAddend
	if hitsAddend := descriptor.GetHitsAddend(); hitsAddend != nil {
		fields["hitsAddend"] = structpb.NewNumberValue(float64(hitsAddend.GetValue()))
	}

	return &structpb.Struct{Fields: fields}
}

func mergeMetadata(dest *structpb.Struct, src *structpb.Struct) {
	if src == nil {
		return
	}
	for k, v := range src.GetFields() {
		destVal, exists := dest.GetFields()[k]
		if exists {
			// If both are structs, merge them recursively
			if destStruct := destVal.GetStructValue(); destStruct != nil {
				if srcStruct := v.GetStructValue(); srcStruct != nil {
					mergeMetadata(destStruct, srcStruct)
					continue
				}
			}
			// TODO(yanavlasov): add option to overwrite or add if type is a list
		} else {
			// Otherwise overwrite or add
			dest.GetFields()[k] = v
		}
	}
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
	descriptor *pb.RateLimitResponse_DescriptorStatus,
) *core.HeaderValue {
	return &core.HeaderValue{
		Key:   this.customHeaderResetHeader,
		Value: strconv.FormatInt(utils.CalculateReset(&descriptor.CurrentLimit.Unit, this.customHeaderClock, this.useCalendarMonthRateLimit).GetSeconds(), 10),
	}
}

func (this *service) rateLimitRequestLimitHeader(descriptor *pb.RateLimitResponse_DescriptorStatus) *core.HeaderValue {
	return &core.HeaderValue{
		Key:   this.requestHeaderLimitHeader,
		Value: strconv.FormatUint(uint64(descriptor.CurrentLimit.RequestsPerUnit), 10),
	}
}

func (this *service) rateLimitRequestRemainingHeader(descriptor *pb.RateLimitResponse_DescriptorStatus) *core.HeaderValue {
	return &core.HeaderValue{
		Key:   this.requestHeaderRemainingHeader,
		Value: strconv.FormatUint(uint64(descriptor.LimitRemaining), 10),
	}
}

func (this *service) rateLimitRequestResetHeader(descriptor *pb.RateLimitResponse_DescriptorStatus) *core.HeaderValue {
	return &core.HeaderValue{
		Key:   this.requestHeaderResetHeader,
		Value: strconv.FormatInt(utils.CalculateReset(&descriptor.CurrentLimit.Unit, this.customHeaderClock, this.useCalendarMonthRateLimit).GetSeconds(), 10),
	}
}

func (this *service) ShouldRateLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
) (finalResponse *pb.RateLimitResponse, finalError error) {
	logger.Debugf("ShouldRateLimit: %+v", request)
	// Generate trace
	_, span := tracer.Start(
		ctx, "ShouldRateLimit Execution",
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

		logger.Debugf("caught error during call: %v", err)

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
	logger.Debugf("returning normal response: %+v", response)

	return response, nil
}

func (this *service) GetCurrentConfig() (config.RateLimitConfig, bool, bool) {
	this.configLock.RLock()
	defer this.configLock.RUnlock()
	return this.config, this.globalShadowMode, this.globalQuotaMode
}

func NewService(cache limiter.RateLimitCache, configProvider provider.RateLimitConfigProvider, statsManager stats.Manager,
	health *server.HealthChecker, clock utils.TimeSource, shadowMode, forceStart bool, healthyWithAtLeastOneConfigLoad bool,
) RateLimitServiceServer {
	newService := &service{
		configLock:        sync.RWMutex{},
		configUpdateEvent: configProvider.ConfigUpdateEvent(),
		config:            nil,
		cache:             cache,
		stats:             statsManager.NewServiceStats(),
		health:            health,
		globalShadowMode:  shadowMode,
		globalQuotaMode:   false,
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

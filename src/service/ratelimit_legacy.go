package ratelimit

import (
	core_legacy "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb_legacy "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v2"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/lyft/gostats"
	"golang.org/x/net/context"
)

type RateLimitLegacyServiceServer interface {
	pb_legacy.RateLimitServiceServer
}

// legacyService is used to implement v2 rls.proto (https://github.com/envoyproxy/data-plane-api/blob/master/envoy/service/ratelimit/v2/rls.proto)
// the legacyService receives RateLimitRequests, converts the request, and calls the service's ShouldRateLimit method.
type legacyService struct {
	s                          *service
	shouldRateLimitLegacyStats shouldRateLimitLegacyStats
}

type shouldRateLimitLegacyStats struct {
	reqConversionError   stats.Counter
	respConversionError  stats.Counter
	shouldRateLimitError stats.Counter
}

func newShouldRateLimitLegacyStats(scope stats.Scope) shouldRateLimitLegacyStats {
	s := scope.Scope("call.should_rate_limit_legacy")
	return shouldRateLimitLegacyStats{
		reqConversionError:   s.NewCounter("req_conversion_error"),
		respConversionError:  s.NewCounter("resp_conversion_error"),
		shouldRateLimitError: s.NewCounter("should_rate_limit_error"),
	}
}

func (this *legacyService) ShouldRateLimit(
	ctx context.Context,
	legacyRequest *pb_legacy.RateLimitRequest) (finalResponse *pb_legacy.RateLimitResponse, finalError error) {

	request, err := ConvertLegacyRequest(legacyRequest)
	if err != nil {
		this.shouldRateLimitLegacyStats.reqConversionError.Inc()
		return nil, err
	}
	resp, err := this.s.ShouldRateLimit(ctx, request)
	if err != nil {
		this.shouldRateLimitLegacyStats.shouldRateLimitError.Inc()
		return nil, err
	}

	legacyResponse, err := ConvertResponse(resp)
	if err != nil {
		this.shouldRateLimitLegacyStats.respConversionError.Inc()
		return nil, err
	}

	return legacyResponse, nil
}

func ConvertLegacyRequest(legacyRequest *pb_legacy.RateLimitRequest) (*pb.RateLimitRequest, error) {
	if legacyRequest == nil {
		return nil, nil
	}
	request := &pb.RateLimitRequest{
		Domain:     legacyRequest.GetDomain(),
		HitsAddend: legacyRequest.GetHitsAddend(),
	}
	if legacyRequest.GetDescriptors() != nil {
		descriptors := make([]*pb_struct.RateLimitDescriptor, len(legacyRequest.GetDescriptors()))
		for i, descriptor := range legacyRequest.GetDescriptors() {
			if descriptor != nil {
				descriptors[i] = &pb_struct.RateLimitDescriptor{}
				if descriptor.GetEntries() != nil {
					entries := make([]*pb_struct.RateLimitDescriptor_Entry, len(descriptor.GetEntries()))
					for j, entry := range descriptor.GetEntries() {
						if entry != nil {
							entries[j] = &pb_struct.RateLimitDescriptor_Entry{
								Key:   entry.GetKey(),
								Value: entry.GetValue(),
							}
						}
					}
					descriptors[i].Entries = entries
				}
			}
		}
		request.Descriptors = descriptors
	}
	return request, nil
}

func ConvertResponse(response *pb.RateLimitResponse) (*pb_legacy.RateLimitResponse, error) {
	if response == nil {
		return nil, nil
	}

	legacyResponse := &pb_legacy.RateLimitResponse{
		OverallCode: pb_legacy.RateLimitResponse_Code(response.GetOverallCode()),
	}

	if response.GetStatuses() != nil {
		statuses := make([]*pb_legacy.RateLimitResponse_DescriptorStatus, len(response.GetStatuses()))
		for i, status := range response.GetStatuses() {
			if status != nil {
				statuses[i] = &pb_legacy.RateLimitResponse_DescriptorStatus{
					Code:           pb_legacy.RateLimitResponse_Code(status.GetCode()),
					LimitRemaining: status.GetLimitRemaining(),
				}
				if status.GetCurrentLimit() != nil {
					statuses[i].CurrentLimit = &pb_legacy.RateLimitResponse_RateLimit{
						Name:            status.GetCurrentLimit().GetName(),
						RequestsPerUnit: status.GetCurrentLimit().GetRequestsPerUnit(),
						Unit:            pb_legacy.RateLimitResponse_RateLimit_Unit(status.GetCurrentLimit().GetUnit()),
					}
				}
			}
		}
		legacyResponse.Statuses = statuses
	}

	if response.GetRequestHeadersToAdd() != nil {
		requestHeadersToAdd := make([]*core_legacy.HeaderValue, len(response.GetRequestHeadersToAdd()))
		for i, header := range response.GetRequestHeadersToAdd() {
			if header != nil {
				requestHeadersToAdd[i] = &core_legacy.HeaderValue{
					Key:   header.GetKey(),
					Value: header.GetValue(),
				}
			}
		}
		legacyResponse.RequestHeadersToAdd = requestHeadersToAdd
	}

	if response.GetResponseHeadersToAdd() != nil {
		responseHeadersToAdd := make([]*core_legacy.HeaderValue, len(response.GetResponseHeadersToAdd()))
		for i, header := range response.GetResponseHeadersToAdd() {
			if header != nil {
				responseHeadersToAdd[i] = &core_legacy.HeaderValue{
					Key:   header.GetKey(),
					Value: header.GetValue(),
				}
			}
		}
		legacyResponse.Headers = responseHeadersToAdd
	}

	return legacyResponse, nil
}

package ratelimit

import (
	pb_struct "github.com/lyft/ratelimit/proto/envoy/api/v2/ratelimit"
	pb "github.com/lyft/ratelimit/proto/envoy/service/ratelimit/v2"
	pb_legacy "github.com/lyft/ratelimit/proto/ratelimit"
	"golang.org/x/net/context"
)

type RateLimitLegacyServiceServer interface {
	pb_legacy.RateLimitServiceServer
}

func (this *legacyService) ShouldRateLimit(
	ctx context.Context,
	legacyRequest *pb_legacy.RateLimitRequest) (finalResponse *pb_legacy.RateLimitResponse, finalError error) {

	request := ConvertLegacyRequest(legacyRequest)
	resp, err := this.s.ShouldRateLimit(ctx, request)
	legacyResponse := ConvertResponse(resp)
	return legacyResponse, err
}

func ConvertLegacyRequest(legacyRequest *pb_legacy.RateLimitRequest) *pb.RateLimitRequest {
	if legacyRequest == nil {
		return nil
	}
	req := &pb.RateLimitRequest{
		Domain:      legacyRequest.Domain,
		HitsAddend:  legacyRequest.HitsAddend,
		Descriptors: ConvertLegacyDescriptors(legacyRequest.Descriptors),
	}
	return req
}

func ConvertLegacyDescriptors(legacyDescriptors []*pb_legacy.RateLimitDescriptor) []*pb_struct.RateLimitDescriptor {
	if legacyDescriptors == nil {
		return nil
	}

	ret := make([]*pb_struct.RateLimitDescriptor, 0)
	for _, d := range legacyDescriptors {
		if d == nil {
			ret = append(ret, nil)
			continue
		}

		ret = append(ret, &pb_struct.RateLimitDescriptor{
			Entries: ConvertLegacyEntries(d.GetEntries()),
		})
	}
	return ret
}

func ConvertLegacyEntries(legacyEntries []*pb_legacy.RateLimitDescriptor_Entry) []*pb_struct.RateLimitDescriptor_Entry {
	if legacyEntries == nil {
		return nil
	}

	entries := make([]*pb_struct.RateLimitDescriptor_Entry, 0)
	for _, e := range legacyEntries {
		if e == nil {
			entries = append(entries, nil)
			continue
		}
		entries = append(entries, &pb_struct.RateLimitDescriptor_Entry{
			Key:   e.GetKey(),
			Value: e.GetValue(),
		})
	}
	return entries
}

func ConvertResponse(response *pb.RateLimitResponse) *pb_legacy.RateLimitResponse {
	if response == nil {
		return nil
	}
	return &pb_legacy.RateLimitResponse{
		OverallCode: ConvertResponseCode(response.OverallCode),
		Statuses:    ConvertResponseStatuses(response.Statuses),
	}
}

func ConvertResponseCode(code pb.RateLimitResponse_Code) pb_legacy.RateLimitResponse_Code {
	switch code {
	case pb.RateLimitResponse_OK:
		return pb_legacy.RateLimitResponse_OK
	case pb.RateLimitResponse_OVER_LIMIT:
		return pb_legacy.RateLimitResponse_OVER_LIMIT
	case pb.RateLimitResponse_UNKNOWN:
		return pb_legacy.RateLimitResponse_UNKNOWN
	default:
		return pb_legacy.RateLimitResponse_UNKNOWN
	}
}

func ConvertResponseStatuses(statuses []*pb.RateLimitResponse_DescriptorStatus) []*pb_legacy.RateLimitResponse_DescriptorStatus {
	if statuses == nil {
		return nil
	}

	legacyStatuses := make([]*pb_legacy.RateLimitResponse_DescriptorStatus, 0)
	for _, s := range statuses {
		if s == nil {
			legacyStatuses = append(legacyStatuses, nil)
			continue
		}
		legacyStatus := &pb_legacy.RateLimitResponse_DescriptorStatus{
			Code:           ConvertResponseCode(s.GetCode()),
			CurrentLimit:   ConvertRatelimit(s.GetCurrentLimit()),
			LimitRemaining: s.GetLimitRemaining(),
		}
		legacyStatuses = append(legacyStatuses, legacyStatus)
	}
	return legacyStatuses
}

func ConvertRatelimit(ratelimit *pb.RateLimitResponse_RateLimit) *pb_legacy.RateLimit {
	if ratelimit == nil {
		return nil
	}
	return &pb_legacy.RateLimit{
		RequestsPerUnit: ratelimit.GetRequestsPerUnit(),
		Unit:            ConvertRatelimitUnit(ratelimit.GetUnit()),
	}
}

func ConvertRatelimitUnit(unit pb.RateLimitResponse_RateLimit_Unit) pb_legacy.RateLimit_Unit {
	switch unit {
	case pb.RateLimitResponse_RateLimit_SECOND:
		return pb_legacy.RateLimit_SECOND
	case pb.RateLimitResponse_RateLimit_MINUTE:
		return pb_legacy.RateLimit_MINUTE
	case pb.RateLimitResponse_RateLimit_HOUR:
		return pb_legacy.RateLimit_HOUR
	case pb.RateLimitResponse_RateLimit_DAY:
		return pb_legacy.RateLimit_DAY
	case pb.RateLimitResponse_RateLimit_UNKNOWN:
		return pb_legacy.RateLimit_UNKNOWN
	default:
		return pb_legacy.RateLimit_UNKNOWN
	}
}

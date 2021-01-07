package util

import (
	"fmt"
	"strconv"
	"strings"

	envoy_extensions_common_ratelimit_v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
)

type MetricsDescriptor struct {
	Limit string
	Unit  string
	Key   string
	Value string
}

func ConvertToMetricsDescriptor(descriptorStatus *pb.RateLimitResponse_DescriptorStatus, descriptor *envoy_extensions_common_ratelimit_v3.RateLimitDescriptor) MetricsDescriptor {
	var descriptorKey strings.Builder
	var descriptorValue strings.Builder
	limit := ""
	unit := ""

	for _, entry := range descriptor.Entries {
		if descriptorKey.Len() != 0 {
			descriptorKey.WriteString("_")
		}
		if descriptorValue.Len() != 0 {
			descriptorValue.WriteString("_")
		}
		descriptorKey.WriteString(entry.Key)
		descriptorValue.WriteString(fmt.Sprintf("%.*s", 40, entry.Value))
	}
	if descriptorStatus.CurrentLimit != nil {
		limit = strconv.FormatUint(uint64(descriptorStatus.CurrentLimit.RequestsPerUnit), 10)
		unit = descriptorStatus.CurrentLimit.Unit.String()
	}

	return MetricsDescriptor{
		Unit:  unit,
		Limit: limit,
		Key:   descriptorKey.String(),
		Value: descriptorValue.String(),
	}
}

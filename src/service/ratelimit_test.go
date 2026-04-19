package ratelimit

import (
	"testing"

	ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ratelimit/src/config"
)

func TestRatelimitToMetadata(t *testing.T) {
	cases := []struct {
		name              string
		req               *pb.RateLimitRequest
		passedDescriptors []int
		limitsToCheck     []*config.RateLimit
		expected          string
	}{
		{
			name: "Single descriptor with single entry, no quota violations",
			req: &pb.RateLimitRequest{
				Domain: "fake-domain",
				Descriptors: []*ratelimitv3.RateLimitDescriptor{
					{
						Entries: []*ratelimitv3.RateLimitDescriptor_Entry{
							{
								Key:   "key1",
								Value: "val1",
							},
						},
					},
				},
			},
			passedDescriptors: nil,
			limitsToCheck:     []*config.RateLimit{nil},
			expected: `{
    "descriptors": [
        {
            "entries": [
                "key1=val1"
            ]
        }
    ],
    "domain": "fake-domain"
}`,
		},
		{
			name: "Single descriptor with quota mode violation",
			req: &pb.RateLimitRequest{
				Domain: "quota-domain",
				Descriptors: []*ratelimitv3.RateLimitDescriptor{
					{
						Entries: []*ratelimitv3.RateLimitDescriptor_Entry{
							{
								Key:   "quota_key",
								Value: "quota_val",
							},
						},
					},
				},
			},
			passedDescriptors: []int{0},
			limitsToCheck: []*config.RateLimit{
				{
					QuotaMode: true,
					Metadata: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							"name": structpb.NewStringValue("service_1"),
						},
					},
				},
			},
			expected: `{
    "descriptors": [
        {
            "entries": [
                "quota_key=quota_val"
            ]
        }
    ],
    "domain": "quota-domain",
    "metadata": {
        "name": "service_1"
    }
}`,
		},
		{
			name: "Multiple descriptors with mixed quota violations",
			req: &pb.RateLimitRequest{
				Domain: "mixed-domain",
				Descriptors: []*ratelimitv3.RateLimitDescriptor{
					{
						Entries: []*ratelimitv3.RateLimitDescriptor_Entry{
							{
								Key:   "regular_key",
								Value: "regular_val",
							},
						},
					},
					{
						Entries: []*ratelimitv3.RateLimitDescriptor_Entry{
							{
								Key:   "quota_key",
								Value: "quota_val",
							},
						},
					},
					{
						Entries: []*ratelimitv3.RateLimitDescriptor_Entry{
							{
								Key:   "another_quota",
								Value: "another_val",
							},
						},
					},
				},
			},
			passedDescriptors: []int{1, 2},
			limitsToCheck: []*config.RateLimit{
				{
					QuotaMode: false,
					Metadata: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							"name": structpb.NewStringValue("service_1"),
						},
					},
				},
				{
					QuotaMode: true,
					Metadata: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							"name": structpb.NewStringValue("service_2"),
						},
					},
				},
				{
					QuotaMode: true,
					Metadata: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							"name": structpb.NewStringValue("service_3"),
						},
					},
				},
			},
			expected: `{
    "descriptors": [
        {
            "entries": [
                "regular_key=regular_val"
            ]
        },
        {
            "entries": [
                "quota_key=quota_val"
            ]
        },
        {
            "entries": [
                "another_quota=another_val"
            ]
        }
    ],
    "domain": "mixed-domain",
    "metadata": {
        "name": "service_2"
    }
}`,
		},
		{
			name: "Request with hits addend",
			req: &pb.RateLimitRequest{
				Domain:     "addend-domain",
				HitsAddend: 5,
				Descriptors: []*ratelimitv3.RateLimitDescriptor{
					{
						Entries: []*ratelimitv3.RateLimitDescriptor_Entry{
							{
								Key:   "test_key",
								Value: "test_val",
							},
						},
					},
				},
			},
			passedDescriptors: []int{0},
			limitsToCheck: []*config.RateLimit{
				{
					QuotaMode: true,
				},
			},
			expected: `{
    "descriptors": [
        {
            "entries": [
                "test_key=test_val"
            ]
        }
    ],
    "domain": "addend-domain",
    "hitsAddend": 5
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ratelimitToMetadata(tc.req, tc.passedDescriptors, tc.limitsToCheck)
			expected := &structpb.Struct{}
			err := protojson.Unmarshal([]byte(tc.expected), expected)
			require.NoError(t, err)

			if diff := cmp.Diff(got, expected, protocmp.Transform()); diff != "" {
				t.Errorf("diff: %s", diff)
			}
		})
	}
}

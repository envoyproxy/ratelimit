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
)

func TestRatelimitToMetadata(t *testing.T) {
	cases := []struct {
		name     string
		req      *pb.RateLimitRequest
		expected string
	}{
		{
			name: "Single descriptor with single entry",
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ratelimitToMetadata(tc.req)
			expected := &structpb.Struct{}
			err := protojson.Unmarshal([]byte(tc.expected), expected)
			require.NoError(t, err)

			if diff := cmp.Diff(got, expected, protocmp.Transform()); diff != "" {
				t.Errorf("diff: %s", diff)
			}
		})
	}
}

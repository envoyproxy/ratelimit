package config

import (
	"reflect"
	"testing"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
)

func Test_extractKeys(t *testing.T) {
	type args struct {
		descriptorMap map[string]*rateLimitDescriptor
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "Split two descriptors",
			args: args{
				descriptorMap: map[string]*rateLimitDescriptor{
					"PATH_/api/v1": {
						descriptors: map[string]*rateLimitDescriptor{},
						limit: &RateLimit{
							FullKey: "someKey",
						},
					},
					"PATH_^\\/api\\/[\\w\\/]+$": {
						descriptors: map[string]*rateLimitDescriptor{},
						limit: &RateLimit{
							FullKey: "someRegex",
						},
					},
				},
			},
			want: []string{"/api/v1", "^\\/api\\/[\\w\\/]+$"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractKeys(tt.args.descriptorMap); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractKeys() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_checkRegex(t *testing.T) {
	type args struct {
		descriptorMap map[string]*rateLimitDescriptor
		descriptor    *pb_struct.RateLimitDescriptor
	}
	tests := []struct {
		name      string
		args      args
		wantValue string
	}{
		{
			name: "Regex match -> return regex expression to match descriptor in rate limiting",
			args: args{
				descriptorMap: map[string]*rateLimitDescriptor{
					"PATH_/api/v1": {
						descriptors: map[string]*rateLimitDescriptor{},
						limit: &RateLimit{
							FullKey: "someKey",
						},
					},
					"PATH_^\\/api\\/[\\w\\/]+$": {
						descriptors: map[string]*rateLimitDescriptor{},
						limit: &RateLimit{
							FullKey: "someRegex",
						},
					},
				},
				descriptor: &pb_struct.RateLimitDescriptor{
					Entries: []*pb_struct.RateLimitDescriptor_Entry{{
						Key:   "PATH",
						Value: "/api/v1/",
					},
					},
				},
			},
			wantValue: "^\\/api\\/[\\w\\/]+$",
		},
		{
			name: "No match -> return original value",
			args: args{
				descriptorMap: map[string]*rateLimitDescriptor{
					"PATH_/api/v1": {
						descriptors: map[string]*rateLimitDescriptor{},
						limit: &RateLimit{
							FullKey: "someKey",
						},
					},
					"PATH_^\\/api\\/[\\w\\/]+$": {
						descriptors: map[string]*rateLimitDescriptor{},
						limit: &RateLimit{
							FullKey: "someRegex",
						},
					},
				},
				descriptor: &pb_struct.RateLimitDescriptor{
					Entries: []*pb_struct.RateLimitDescriptor_Entry{{
						Key:   "PATH",
						Value: "/livez",
					},
					},
				},
			},
			wantValue: "/livez",
		},
		{
			name: "No Regex match but simple uri match -> return original value",
			args: args{
				descriptorMap: map[string]*rateLimitDescriptor{
					"PATH_/someurl": {
						descriptors: map[string]*rateLimitDescriptor{},
						limit: &RateLimit{
							FullKey: "someKey",
						},
					},
					"PATH_^\\/api\\/[\\w\\/]+$": {
						descriptors: map[string]*rateLimitDescriptor{},
						limit: &RateLimit{
							FullKey: "someRegex",
						},
					},
				},
				descriptor: &pb_struct.RateLimitDescriptor{
					Entries: []*pb_struct.RateLimitDescriptor_Entry{{
						Key:   "PATH",
						Value: "/someurl",
					},
					},
				},
			},
			wantValue: "/someurl",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotValue := checkRegex(tt.args.descriptorMap, tt.args.descriptor); gotValue != tt.wantValue {
				t.Errorf("checkRegex() = %v, want %v", gotValue, tt.wantValue)
			}
		})
	}
}

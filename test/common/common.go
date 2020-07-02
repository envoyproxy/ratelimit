package common

import (
	"fmt"
	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"sync"

	pb_struct_legacy "github.com/envoyproxy/go-control-plane/envoy/api/v2/ratelimit"
	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb_legacy "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v2"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
)

type TestStatSink struct {
	sync.Mutex
	Record map[string]interface{}
}

func (s *TestStatSink) Clear() {
	s.Lock()
	s.Record = map[string]interface{}{}
	s.Unlock()
}

func (s *TestStatSink) FlushCounter(name string, value uint64) {
	s.Lock()
	s.Record[name] = value
	s.Unlock()
}

func (s *TestStatSink) FlushGauge(name string, value uint64) {
	s.Lock()
	s.Record[name] = value
	s.Unlock()
}

func (s *TestStatSink) FlushTimer(name string, value float64) {
	s.Lock()
	s.Record[name] = value
	s.Unlock()
}

func NewRateLimitRequest(domain string, descriptors [][][2]string, hitsAddend uint32) *pb.RateLimitRequest {
	request := &pb.RateLimitRequest{}
	request.Domain = domain
	for _, descriptor := range descriptors {
		newDescriptor := &pb_struct.RateLimitDescriptor{}
		for _, entry := range descriptor {
			newDescriptor.Entries = append(
				newDescriptor.Entries,
				&pb_struct.RateLimitDescriptor_Entry{Key: entry[0], Value: entry[1]})
		}
		request.Descriptors = append(request.Descriptors, newDescriptor)
	}
	request.HitsAddend = hitsAddend
	return request
}

func NewRateLimitRequestLegacy(domain string, descriptors [][][2]string, hitsAddend uint32) *pb_legacy.RateLimitRequest {
	request := &pb_legacy.RateLimitRequest{}
	request.Domain = domain
	for _, descriptor := range descriptors {
		newDescriptor := &pb_struct_legacy.RateLimitDescriptor{}
		for _, entry := range descriptor {
			newDescriptor.Entries = append(
				newDescriptor.Entries,
				&pb_struct_legacy.RateLimitDescriptor_Entry{Key: entry[0], Value: entry[1]})
		}
		request.Descriptors = append(request.Descriptors, newDescriptor)
	}
	request.HitsAddend = hitsAddend
	return request
}

func AssertProtoEqual(assert *assert.Assertions, expected proto.Message, actual proto.Message) {
	assert.True(proto.Equal(expected, actual),
		fmt.Sprintf("These two protobuf messages are not equal:\nexpected: %v\nactual:  %v", expected, actual))
}

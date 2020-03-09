package common

import (
	"sync"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/api/v2/ratelimit"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v2"
	pb_legacy "github.com/envoyproxy/ratelimit/proto/ratelimit"
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
		newDescriptor := &pb_legacy.RateLimitDescriptor{}
		for _, entry := range descriptor {
			newDescriptor.Entries = append(
				newDescriptor.Entries,
				&pb_legacy.RateLimitDescriptor_Entry{Key: entry[0], Value: entry[1]})
		}
		request.Descriptors = append(request.Descriptors, newDescriptor)
	}
	request.HitsAddend = hitsAddend
	return request
}

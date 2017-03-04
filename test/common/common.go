package common

import (
	pb "github.com/lyft/ratelimit/proto/ratelimit"
)

func NewRateLimitRequest(domain string, descriptors [][][2]string, hitsAddend uint32) *pb.RateLimitRequest {
	request := &pb.RateLimitRequest{}
	request.Domain = domain
	for _, descriptor := range descriptors {
		newDescriptor := &pb.RateLimitDescriptor{}
		for _, entry := range descriptor {
			newDescriptor.Entries = append(
				newDescriptor.Entries,
				&pb.RateLimitDescriptor_Entry{Key: entry[0], Value: entry[1]})
		}
		request.Descriptors = append(request.Descriptors, newDescriptor)
	}
	request.HitsAddend = hitsAddend
	return request
}

package metrics

import (
	"context"
	stats "github.com/lyft/gostats"
	"google.golang.org/grpc"
	"time"
)

// ServerMetrics consists of server-side metrics for ratelimit gRPC server
type ServerMetrics struct {
	totalRequests stats.Counter
	responseTime  stats.Timer
}

// NewServerMetrics returns a ServerMetrics object.
func NewServerMetrics(scope stats.Scope) *ServerMetrics {
	ret := ServerMetrics{}
	ret.totalRequests = scope.NewCounter("total_requests")
	ret.responseTime = scope.NewTimer("response_time")
	return &ret
}

// UnaryServerInterceptor is a gRPC server-side interceptor that provides server metrics for Unary RPCs.
func (s *ServerMetrics) UnaryServerInterceptor() func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		s.totalRequests.Inc()
		start := time.Now()
		resp, err := handler(ctx, req)
		s.responseTime.AddValue(float64(time.Since(start).Milliseconds()))
		return resp, err
	}
}

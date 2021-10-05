package metrics

import (
	"context"
	"time"

	stats "github.com/lyft/gostats"
	"google.golang.org/grpc"
)

type serverMetrics struct {
	totalRequests stats.Counter
	responseTime  stats.Timer
}

// ServerReporter reports server-side metrics for ratelimit gRPC server
type ServerReporter struct {
	scope stats.Scope
}

func newServerMetrics(scope stats.Scope, fullMethod string) *serverMetrics {
	_, methodName := splitMethodName(fullMethod)
	ret := serverMetrics{}
	ret.totalRequests = scope.NewCounter(methodName + ".total_requests")
	ret.responseTime = scope.NewTimer(methodName + ".response_time")
	return &ret
}

// NewServerReporter returns a ServerReporter object.
func NewServerReporter(scope stats.Scope) *ServerReporter {
	return &ServerReporter{
		scope: scope,
	}
}

// UnaryServerInterceptor is a gRPC server-side interceptor that provides server metrics for Unary RPCs.
func (r *ServerReporter) UnaryServerInterceptor() func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		s := newServerMetrics(r.scope, info.FullMethod)
		s.totalRequests.Inc()
		resp, err := handler(ctx, req)
		s.responseTime.AddValue(float64(time.Since(start).Milliseconds()))
		return resp, err
	}
}

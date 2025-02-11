package metrics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	stats "github.com/lyft/gostats"
	"google.golang.org/grpc"
)

type capturingResponseWriter struct {
	http.ResponseWriter
	code int
}

type serverMetrics struct {
	totalRequests stats.Counter
	responseTime  stats.Timer
}

// ServerReporter reports server-side metrics for ratelimit gRPC server
type ServerReporter struct {
	scope stats.Scope
}

func newServerMetrics(scope stats.Scope, methodName string, tags map[string]string) *serverMetrics {
	ret := serverMetrics{}
	ret.totalRequests = scope.NewCounterWithTags(methodName+".total_requests", tags)
	ret.responseTime = scope.NewTimerWithTags(methodName+".response_time", tags)
	return &ret
}

func newCapturingResponseWriter(w http.ResponseWriter) *capturingResponseWriter {
	return &capturingResponseWriter{w, http.StatusOK}
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
		_, methodName := splitMethodName(info.FullMethod)
		s := newServerMetrics(r.scope, methodName, map[string]string{})
		s.totalRequests.Inc()
		resp, err := handler(ctx, req)
		s.responseTime.AddValue(float64(time.Since(start).Milliseconds()))
		return resp, err
	}
}

// HttpServerMetricsHandler returns a wrapping handler to add metrics to HTTP requests.
func (r *ServerReporter) HttpServerMetricsHandler(routeName string, handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(writer http.ResponseWriter, request *http.Request) {
		start := time.Now()
		capturingWriter := newCapturingResponseWriter(writer)
		handler(capturingWriter, request)

		tags := map[string]string{"code": fmt.Sprintf("%v", capturingWriter.code)}
		s := newServerMetrics(r.scope, routeName, tags)
		s.totalRequests.Inc()
		s.responseTime.AddValue(float64(time.Since(start).Milliseconds()))
	}
}

func (c *capturingResponseWriter) WriteHeader(code int) {
	c.code = code
	c.ResponseWriter.WriteHeader(code)
}

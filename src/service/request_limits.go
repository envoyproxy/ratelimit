package ratelimit

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/envoyproxy/ratelimit/src/stats"
)

// ServiceOption configures a service before it starts accepting requests.
type ServiceOption func(*service)

// WithRequestLimits limits concurrent ShouldRateLimit calls and supplies a
// deadline to their cache operations. Zero disables the corresponding limit.
// The deadline does not guarantee that a cache operation returns promptly.
func WithRequestLimits(maxConcurrentRequests int, timeout time.Duration) ServiceOption {
	if maxConcurrentRequests < 0 {
		panic("MAX_CONCURRENT_REQUESTS must be >= 0")
	}
	if timeout < 0 {
		panic("REQUEST_TIMEOUT must be >= 0")
	}
	return func(s *service) {
		if maxConcurrentRequests == 0 && timeout == 0 {
			return
		}
		limits := &requestLimits{timeout: timeout, stats: s.stats.RequestAdmission}
		if maxConcurrentRequests > 0 {
			limits.active = make(chan struct{}, maxConcurrentRequests)
		}
		s.requestLimits = limits
	}
}

type requestLimits struct {
	active  chan struct{}
	timeout time.Duration
	stats   stats.RequestAdmissionStats
}

// acquire does not queue requests. The caller must defer release until all
// synchronous cache work has returned, even if the request context is cancelled.
func (l *requestLimits) acquire(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, status.FromContextError(err).Err()
	}
	if l.active != nil {
		select {
		case l.active <- struct{}{}:
		default:
			l.stats.Rejected.Inc()
			return nil, nil, status.Error(codes.ResourceExhausted, "maximum concurrent rate limit requests reached")
		}
	}

	cancel := func() {}
	if l.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, l.timeout)
	}
	l.stats.Admitted.Inc()
	l.stats.InFlight.Add(1)
	start := time.Now()
	return ctx, func() {
		cancel()
		defer func() {
			l.stats.InFlight.Sub(1)
			if l.active != nil {
				<-l.active
			}
		}()
		// Record processing time up to metric emission, not just until caller
		// cancellation. Milliseconds are converted to seconds by the mapper.
		// Keep the admission slot during synchronous metric export as well.
		l.stats.CompletedDuration.AddValue(float64(time.Since(start)) / float64(time.Millisecond))
	}, nil
}

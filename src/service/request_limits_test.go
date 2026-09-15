package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	gostats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
)

type admissionTestCache func(context.Context) []*pb.RateLimitResponse_DescriptorStatus

func (f admissionTestCache) DoLimit(ctx context.Context, _ *pb.RateLimitRequest, _ []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus {
	return f(ctx)
}

func (admissionTestCache) Flush() {}

func admissionOK() []*pb.RateLimitResponse_DescriptorStatus {
	return []*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK}}
}

func admissionRequest() *pb.RateLimitRequest {
	return &pb.RateLimitRequest{
		Domain: "test",
		Descriptors: []*ratelimitv3.RateLimitDescriptor{{
			Entries: []*ratelimitv3.RateLimitDescriptor_Entry{{Key: "key", Value: "value"}},
		}},
	}
}

func newAdmissionTestService(t *testing.T, cache limiter.RateLimitCache, max int, timeout time.Duration) *service {
	t.Helper()
	store := gostats.NewStore(gostats.NewNullSink(), false)
	manager := stats.NewStatManager(store, settings.Settings{})
	s := &service{
		cache: cache,
		stats: manager.NewServiceStats(),
		config: config.NewRateLimitConfigImpl([]config.RateLimitConfigToLoad{{
			Name: "test",
			ConfigYaml: &config.YamlRoot{
				Domain: "test",
				Descriptors: []config.YamlDescriptor{{
					Key: "key", Value: "value",
					RateLimit: &config.YamlRateLimit{RequestsPerUnit: 100, Unit: "second"},
				}},
			},
		}}, manager, false),
	}
	WithRequestLimits(max, timeout)(s)
	return s
}

func waitAdmissionResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("request did not return")
		return nil
	}
}

func waitAdmissionSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("cache operation did not start")
	}
}

func TestRequestAdmissionRejectsExcessWithoutQueueing(t *testing.T) {
	const max = 3
	entered := make(chan struct{}, max)
	unblock := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(unblock) }) }
	t.Cleanup(release)
	var calls atomic.Int64
	s := newAdmissionTestService(t, admissionTestCache(func(context.Context) []*pb.RateLimitResponse_DescriptorStatus {
		calls.Add(1)
		entered <- struct{}{}
		<-unblock
		return admissionOK()
	}), max, 0)
	done := make(chan error, max)
	for range max {
		go func() {
			_, err := s.ShouldRateLimit(context.Background(), admissionRequest())
			done <- err
		}()
		waitAdmissionSignal(t, entered)
	}
	require.Equal(t, uint64(max), s.stats.RequestAdmission.InFlight.Value())

	// The overload response is independent of descriptor/global shadow mode:
	// it is a service failure, never a successful OVER_LIMIT quota decision.
	for _, shadow := range []bool{false, true} {
		s.configLock.Lock()
		s.globalShadowMode = shadow
		s.configLock.Unlock()
		for range 50 {
			response, err := s.ShouldRateLimit(context.Background(), admissionRequest())
			require.Nil(t, response)
			require.Equal(t, codes.ResourceExhausted, status.Code(err))
		}
	}
	require.Equal(t, int64(max), calls.Load())
	require.Equal(t, uint64(100), s.stats.RequestAdmission.Rejected.Value())
	require.Equal(t, uint64(max), s.stats.RequestAdmission.Admitted.Value())
	release()
	for range max {
		require.NoError(t, waitAdmissionResult(t, done))
	}
	require.Zero(t, s.stats.RequestAdmission.InFlight.Value())
	response, err := s.ShouldRateLimit(context.Background(), admissionRequest())
	require.NoError(t, err)
	require.Equal(t, pb.RateLimitResponse_OK, response.OverallCode)
}

type admissionConfigUpdate struct{ config.RateLimitConfig }

func (u admissionConfigUpdate) GetConfig() (config.RateLimitConfig, any) {
	return u.RateLimitConfig, nil
}

func TestRequestAdmissionCancellationAndConfigReloadKeepSlotUntilCacheReturns(t *testing.T) {
	entered := make(chan struct{}, 1)
	unblock := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(unblock) }) }
	t.Cleanup(release)
	s := newAdmissionTestService(t, admissionTestCache(func(ctx context.Context) []*pb.RateLimitResponse_DescriptorStatus {
		entered <- struct{}{}
		<-ctx.Done()
		<-unblock // Deliberately model a backend which has not finished cancellation.
		return admissionOK()
	}), 1, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.ShouldRateLimit(ctx, admissionRequest()); done <- err }()
	waitAdmissionSignal(t, entered)
	cancel()
	s.SetConfig(admissionConfigUpdate{s.config}, false)

	_, err := s.ShouldRateLimit(context.Background(), admissionRequest())
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.Equal(t, uint64(1), s.stats.RequestAdmission.InFlight.Value())
	select {
	case <-done:
		t.Fatal("cancelled caller released its slot before cache work returned")
	default:
	}
	release()
	require.Equal(t, codes.Canceled, status.Code(waitAdmissionResult(t, done)))
	require.Zero(t, s.stats.RequestAdmission.InFlight.Value())
}

func TestRequestAdmissionDeadlinePreservesEarlierCallerDeadline(t *testing.T) {
	for _, shorterCaller := range []bool{false, true} {
		t.Run(map[bool]string{false: "server deadline", true: "earlier caller deadline"}[shorterCaller], func(t *testing.T) {
			timeout := 100 * time.Millisecond
			ctx := context.Background()
			var expected time.Time
			if shorterCaller {
				timeout = time.Hour
			}
			var observed time.Time
			s := newAdmissionTestService(t, admissionTestCache(func(ctx context.Context) []*pb.RateLimitResponse_DescriptorStatus {
				observed, _ = ctx.Deadline()
				<-ctx.Done()
				return admissionOK() // A late success must not become a quota response.
			}), 1, timeout)
			if shorterCaller {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(100*time.Millisecond))
				defer cancel()
				expected, _ = ctx.Deadline()
			}
			before := time.Now()
			response, err := s.ShouldRateLimit(ctx, admissionRequest())
			require.Nil(t, response)
			require.Equal(t, codes.DeadlineExceeded, status.Code(err))
			if shorterCaller {
				require.Equal(t, expected, observed)
			} else {
				require.False(t, observed.Before(before.Add(timeout)))
				require.True(t, observed.Before(time.Now().Add(timeout)))
			}
			require.Zero(t, s.stats.RequestAdmission.InFlight.Value())
		})
	}
}

func TestRequestAdmissionRejectsAlreadyCancelledContextBeforeCache(t *testing.T) {
	s := newAdmissionTestService(t, admissionTestCache(func(context.Context) []*pb.RateLimitResponse_DescriptorStatus {
		t.Fatal("cache should not be called")
		return nil
	}), 1, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response, err := s.ShouldRateLimit(ctx, admissionRequest())
	require.Nil(t, response)
	require.Equal(t, codes.Canceled, status.Code(err))
	require.Zero(t, s.stats.RequestAdmission.Admitted.Value())
	require.Zero(t, s.stats.RequestAdmission.Rejected.Value())
	require.Zero(t, s.stats.RequestAdmission.InFlight.Value())
}

func TestRequestAdmissionPanicReleasesSlot(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     any
		recovered bool
	}{
		{"Redis error", redis.RedisError("backend failed"), true},
		{"unexpected panic", "unexpected failure", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := newAdmissionTestService(t, admissionTestCache(func(context.Context) []*pb.RateLimitResponse_DescriptorStatus {
				calls++
				if calls == 1 {
					panic(tc.value)
				}
				return admissionOK()
			}), 1, 0)
			if tc.recovered {
				response, err := s.ShouldRateLimit(context.Background(), admissionRequest())
				require.Nil(t, response)
				require.Equal(t, tc.value, err)
			} else {
				require.PanicsWithValue(t, tc.value, func() {
					_, _ = s.ShouldRateLimit(context.Background(), admissionRequest())
				})
			}
			require.Zero(t, s.stats.RequestAdmission.InFlight.Value())
			response, err := s.ShouldRateLimit(context.Background(), admissionRequest())
			require.NoError(t, err)
			require.Equal(t, pb.RateLimitResponse_OK, response.OverallCode)
		})
	}
}

func TestRequestTimeoutAloneDoesNotLimitConcurrency(t *testing.T) {
	entered := make(chan struct{}, 2)
	s := newAdmissionTestService(t, admissionTestCache(func(ctx context.Context) []*pb.RateLimitResponse_DescriptorStatus {
		entered <- struct{}{}
		<-ctx.Done()
		return admissionOK()
	}), 0, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := s.ShouldRateLimit(ctx, admissionRequest()); done <- err }()
		waitAdmissionSignal(t, entered)
	}
	require.Nil(t, s.requestLimits.active)
	require.Equal(t, uint64(2), s.stats.RequestAdmission.InFlight.Value())
	cancel()
	for range 2 {
		require.Equal(t, codes.Canceled, status.Code(waitAdmissionResult(t, done)))
	}
	require.Zero(t, s.stats.RequestAdmission.InFlight.Value())
}

type admissionTimerSink struct{ flush func() }

func (admissionTimerSink) FlushCounter(string, uint64)  {}
func (admissionTimerSink) FlushGauge(string, uint64)    {}
func (s admissionTimerSink) FlushTimer(string, float64) { s.flush() }

func TestRequestAdmissionRetainsSlotThroughMetricExport(t *testing.T) {
	entered := make(chan struct{}, 1)
	unblock := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(unblock) }) }
	t.Cleanup(release)
	s := newAdmissionTestService(t, admissionTestCache(func(context.Context) []*pb.RateLimitResponse_DescriptorStatus {
		return admissionOK()
	}), 1, 0)
	store := gostats.NewStore(admissionTimerSink{flush: func() {
		entered <- struct{}{}
		<-unblock
	}}, false)
	s.requestLimits.stats.CompletedDuration = store.NewMilliTimer("completed_duration")
	done := make(chan error, 1)
	go func() { _, err := s.ShouldRateLimit(context.Background(), admissionRequest()); done <- err }()
	waitAdmissionSignal(t, entered)
	require.Equal(t, uint64(1), s.stats.RequestAdmission.InFlight.Value())
	response, err := s.ShouldRateLimit(context.Background(), admissionRequest())
	require.Nil(t, response)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	release()
	require.NoError(t, waitAdmissionResult(t, done))
	require.Zero(t, s.stats.RequestAdmission.InFlight.Value())
}

func TestRequestAdmissionMetricPanicReleasesSlot(t *testing.T) {
	s := newAdmissionTestService(t, admissionTestCache(func(context.Context) []*pb.RateLimitResponse_DescriptorStatus {
		return admissionOK()
	}), 1, 0)
	store := gostats.NewStore(admissionTimerSink{flush: func() { panic("metric failure") }}, false)
	s.requestLimits.stats.CompletedDuration = store.NewMilliTimer("completed_duration")
	require.PanicsWithValue(t, "metric failure", func() {
		_, _ = s.ShouldRateLimit(context.Background(), admissionRequest())
	})
	require.Zero(t, s.stats.RequestAdmission.InFlight.Value())
	require.Empty(t, s.requestLimits.active)
}

func TestRequestLimitsValidateAndDefaultToDisabled(t *testing.T) {
	s := &service{}
	WithRequestLimits(0, 0)(s)
	assert.Nil(t, s.requestLimits)
	assert.Panics(t, func() { WithRequestLimits(-1, 0) })
	assert.Panics(t, func() { WithRequestLimits(1, -time.Second) })
}

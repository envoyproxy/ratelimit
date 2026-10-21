package redis_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/service_cmd/runner"
	"github.com/envoyproxy/ratelimit/src/settings"
)

// The tests in this file run the whole service (gRPC server, health check,
// settings, Redis clients) against a fake Redis that can stop answering
// without closing its connections, as a Redis failover that keeps the
// server's IP does.

const e2eConfig = `domain: e2e
descriptors:
  - key: minute
    rate_limit:
      unit: minute
      requests_per_unit: 1000000
  - key: second
    rate_limit:
      unit: second
      requests_per_unit: 1000000
`

type e2eService struct {
	client pb.RateLimitServiceClient
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// startService runs the service against redisSrv (and, if not nil, a
// per-second Redis perSecondSrv), after applying configure to its settings.
func startService(t *testing.T, redisSrv, perSecondSrv *redis.FakeRedisForTest, configure func(*settings.Settings)) *e2eService {
	t.Helper()
	root := t.TempDir()
	configDir := filepath.Join(root, "ratelimit", "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "e2e.yaml"), []byte(e2eConfig), 0o644))

	s := settings.NewSettings()
	s.Host = "127.0.0.1"
	s.GrpcHost = "127.0.0.1"
	s.DebugHost = "127.0.0.1"
	s.Port = freePort(t)
	s.GrpcPort = freePort(t)
	s.DebugPort = freePort(t)
	s.DisableStats = true
	s.UseStatsd = false
	s.LogLevel = "error"
	s.RuntimePath = root
	s.RuntimeSubdirectory = "ratelimit"
	s.RuntimeAppDirectory = "config"
	s.RuntimeWatchRoot = false
	s.BackendType = "redis"
	s.RedisSocketType = "tcp"
	s.RedisUrl = redisSrv.Addr
	if perSecondSrv != nil {
		s.RedisPerSecond = true
		s.RedisPerSecondSocketType = "tcp"
		s.RedisPerSecondUrl = perSecondSrv.Addr
	}
	if configure != nil {
		configure(&s)
	}

	r := runner.NewRunner(s)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				t.Errorf("runner panicked: %v", p)
			}
		}()
		r.Run()
	}()
	t.Cleanup(r.Stop)
	// Cleanups run last-in first-out: close the fake Redis servers before
	// stopping the service, so that a regression that leaves calls blocked on
	// them fails the test instead of hanging its cleanup.
	t.Cleanup(redisSrv.Close)
	if perSecondSrv != nil {
		t.Cleanup(perSecondSrv.Close)
	}

	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", s.GrpcPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	svc := &e2eService{client: pb.NewRateLimitServiceClient(conn)}
	require.Eventually(t, func() bool { return svc.call("minute", time.Second) == nil },
		10*time.Second, 50*time.Millisecond, "service did not start")
	return svc
}

// call sends a rate limit request for the descriptor key with the given
// deadline, as Envoy's rate limit filter does with its timeout.
func (s *e2eService) call(key string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err := s.client.ShouldRateLimit(ctx, &pb.RateLimitRequest{
		Domain: "e2e",
		Descriptors: []*pb_struct.RateLimitDescriptor{{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: key, Value: "v"}},
		}},
		HitsAddend: 1,
	})
	return err
}

// callConcurrently sends n calls at once and returns how long each took and
// whether it failed.
func (s *e2eService) callConcurrently(key string, n int, timeout time.Duration) ([]time.Duration, []error) {
	durations := make([]time.Duration, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start := time.Now()
			errs[i] = s.call(key, timeout)
			durations[i] = time.Since(start)
		}(i)
	}
	wg.Wait()
	return durations, errs
}

func TestServiceFailsFastWhenRedisStopsAnswering(t *testing.T) {
	ioTimeout := 300 * time.Millisecond
	// Generous client deadline: the server must fail on its own, well before it.
	const clientTimeout = 5 * time.Second

	tests := []struct {
		name      string
		perSecond bool
		configure func(*settings.Settings)
		key       string
	}{
		{
			name:      "REDIS_TIMEOUT bounds I/O",
			configure: func(s *settings.Settings) { s.RedisTimeout = ioTimeout },
			key:       "minute",
		},
		{
			name: "REDIS_IO_TIMEOUT overrides REDIS_TIMEOUT",
			configure: func(s *settings.Settings) {
				s.RedisTimeout = time.Minute
				s.RedisIoTimeout = &ioTimeout
			},
			key: "minute",
		},
		{
			name:      "REDIS_PERSECOND_TIMEOUT bounds per-second I/O",
			perSecond: true,
			configure: func(s *settings.Settings) {
				s.RedisTimeout = time.Minute
				s.RedisPerSecondTimeout = ioTimeout
			},
			key: "second",
		},
		{
			name:      "REDIS_PERSECOND_IO_TIMEOUT overrides REDIS_PERSECOND_TIMEOUT",
			perSecond: true,
			configure: func(s *settings.Settings) {
				s.RedisTimeout = time.Minute
				s.RedisPerSecondTimeout = time.Minute
				s.RedisPerSecondIoTimeout = &ioTimeout
			},
			key: "second",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main := redis.NewFakeRedisForTest(t)
			failing := main
			var perSecond *redis.FakeRedisForTest
			if tt.perSecond {
				failing = redis.NewFakeRedisForTest(t)
				perSecond = &failing
			}
			svc := startService(t, &main, perSecond, tt.configure)

			failing.SetBlackhole(true)
			durations, errs := svc.callConcurrently(tt.key, 50, clientTimeout)
			for i := range errs {
				assert.Error(t, errs[i])
				assert.Less(t, durations[i], 10*ioTimeout, "call %d took %s", i, durations[i])
			}
			if tt.perSecond {
				// The other Redis is unaffected.
				assert.NoError(t, svc.call("minute", clientTimeout))
			}

			// The service recovers on its own once Redis answers again.
			failing.SetBlackhole(false)
			assert.Eventually(t, func() bool { return svc.call(tt.key, clientTimeout) == nil },
				10*time.Second, 50*time.Millisecond)
		})
	}
}

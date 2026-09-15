package ratelimit

import (
	"bufio"
	"context"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	gostats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4/resp"
	"github.com/mediocregopher/radix/v4/resp/resp3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
	"github.com/envoyproxy/ratelimit/src/utils"
)

// stalledRedis receives real RESP commands over loopback, but withholds command
// replies until released. Reads continue while replies are stalled, so the test
// can prove that another request was actually submitted behind a cancelled read.
func stalledRedis(t *testing.T) (string, <-chan struct{}, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	commands := make(chan struct{}, 16)
	unblock := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(unblock) }) }
	var workers sync.WaitGroup
	var mu sync.Mutex
	var conns []net.Conn
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				type reply struct {
					value string
					stall bool
				}
				replies := make(chan reply, 16)
				writerDone := make(chan struct{})
				go func() {
					defer close(writerDone)
					for response := range replies {
						if response.stall {
							<-unblock
						}
						if _, err := conn.Write([]byte(response.value)); err != nil {
							return
						}
					}
				}()
				defer func() { close(replies); <-writerDone }()
				reader := bufio.NewReader(conn)
				opts := resp.NewOpts()
				for {
					var command []string
					if err := resp3.Unmarshal(reader, &command, opts); err != nil {
						return
					}
					response := reply{value: "-ERR unsupported test command\r\n"}
					switch command[0] {
					case "PING":
						response.value = "+PONG\r\n"
					case "INCRBY":
						commands <- struct{}{}
						response = reply{value: ":1\r\n", stall: true}
					case "EXPIRE":
						response.value = ":1\r\n"
					}
					select {
					case replies <- response:
					case <-writerDone:
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		release()
		_ = listener.Close()
		mu.Lock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	return listener.Addr().String(), commands, release
}

func TestRequestAdmissionBoundsWorkBehindStalledRedisCancellation(t *testing.T) {
	addr, commands, releaseRedis := stalledRedis(t)
	store := gostats.NewStore(gostats.NewNullSink(), false)
	manager := stats.NewStatManager(store, settings.Settings{})
	client := redis.NewClientImpl(context.Background(), store, false, "", "tcp", "single", addr,
		1, 0, 0, nil, false, nil, time.Second, "WAIT", "", time.Millisecond, time.Millisecond, time.Second, false)
	t.Cleanup(func() {
		// Unblock the peer before closing the pool, including on assertion
		// failure: pool shutdown may wait for active shared calls to return.
		releaseRedis()
		_ = client.Close()
	})
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, utils.NewTimeSourceImpl(),
		rand.New(utils.NewLockedSource(1)), 0, nil, 0.8, "", manager, false, false)
	s := newAdmissionTestService(t, cache, 1, 200*time.Millisecond)

	// The first deadline returns to its caller, but Radix can still be draining
	// that response internally. This increment does not claim to stop Redis work.
	firstDone := make(chan error, 1)
	go func() { _, err := s.ShouldRateLimit(context.Background(), admissionRequest()); firstDone <- err }()
	waitAdmissionSignal(t, commands)
	require.Equal(t, codes.DeadlineExceeded, status.Code(waitAdmissionResult(t, firstDone)))
	require.Zero(t, s.stats.RequestAdmission.InFlight.Value())

	// A second request is written behind the unread response. Cancelling it
	// cannot interrupt the earlier response's drain; its service slot must stay
	// occupied until its own synchronous cache call has returned.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	secondDone := make(chan error, 1)
	go func() { _, err := s.ShouldRateLimit(ctx, admissionRequest()); secondDone <- err }()
	waitAdmissionSignal(t, commands)
	cancel()
	select {
	case <-secondDone:
		t.Fatal("Radix unexpectedly returned while the earlier response was still withheld")
	case <-time.After(50 * time.Millisecond):
	}
	require.Equal(t, uint64(1), s.stats.RequestAdmission.InFlight.Value())
	for range 100 {
		response, err := s.ShouldRateLimit(context.Background(), admissionRequest())
		require.Nil(t, response)
		require.Equal(t, codes.ResourceExhausted, status.Code(err))
	}
	require.Equal(t, uint64(2), s.stats.RequestAdmission.Admitted.Value())
	require.Equal(t, uint64(100), s.stats.RequestAdmission.Rejected.Value())
	select {
	case <-commands:
		t.Fatal("a rejected request submitted another Redis increment")
	default:
	}

	// Recovery is established only after the server releases the withheld
	// responses. A subsequent normal request can then reuse the freed capacity.
	releaseRedis()
	require.Equal(t, codes.Canceled, status.Code(waitAdmissionResult(t, secondDone)))
	require.Zero(t, s.stats.RequestAdmission.InFlight.Value())
	response, err := s.ShouldRateLimit(context.Background(), admissionRequest())
	require.NoError(t, err)
	require.Equal(t, pb.RateLimitResponse_OK, response.OverallCode)
	require.Equal(t, uint64(3), s.stats.RequestAdmission.Admitted.Value())
}

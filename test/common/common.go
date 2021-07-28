package common

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/assert"

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

type RedisConfig struct {
	Port     int
	Password string
	Cluster  bool
}

type MemcacheConfig struct {
	Port int
}

// startCacheProcess starts memcache or redis as a subprocess and waits until the TCP port is open.
func startCacheProcess(ctx context.Context, command string, args []string, port int) (context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, command, args...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Start()

	if err != nil {
		cancel()
		return nil, fmt.Errorf("Problem starting %s subprocess: %v", command, err)
	}

	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 1*time.Second)
	defer timeoutCancel()

	// Wait up to 1s for the redis instance to start accepting connections.
	for {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", "localhost:"+strconv.Itoa(port))
		if err == nil {
			conn.Close()
			// TCP connections are working. All is well.
			break
		}
		// Unable to connect to the TCP port. Wait and try again.
		select {
		case <-time.After(100 * time.Millisecond):
		case <-timeoutCtx.Done():
			cancel()
			return nil, fmt.Errorf("Timed out waiting for %s to start up and accept connections: %v", command, err)
		}
	}

	return func() {
		cancel()
		cmd.Wait()
	}, nil
}

func WithMultiRedis(t *testing.T, configs []RedisConfig, f func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, config := range configs {
		args := []string{"--port", strconv.Itoa(config.Port)}
		if config.Password != "" {
			args = append(args, "--requirepass", config.Password)
		}
		if config.Cluster {
			args = append(args, "--cluster-enabled", "yes")
		}

		cancel, err := startCacheProcess(ctx, "redis-server", args, config.Port)
		if err != nil {
			t.Errorf("Error starting redis: %v", err)
			return
		}
		defer cancel()
	}

	f()
}

func WithMultiMemcache(t *testing.T, configs []MemcacheConfig, f func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, config := range configs {
		args := []string{"-u", "root", "-m", "64", "--port", strconv.Itoa(config.Port)}

		cancel, err := startCacheProcess(ctx, "memcached", args, config.Port)
		if err != nil {
			t.Errorf("Error starting memcache: %v", err)
			return
		}
		defer cancel()
	}

	f()
}

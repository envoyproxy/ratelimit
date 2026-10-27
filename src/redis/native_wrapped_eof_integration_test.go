package redis_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	common "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/settings"
)

type nativeEOFCommand struct {
	AtUTC         string   `json:"at_utc"`
	Connection    int      `json:"connection"`
	Arguments     []string `json:"arguments"`
	ReplyWritten  bool     `json:"reply_written"`
	FaultBoundary bool     `json:"fault_boundary"`
}

type nativeEOFServer struct {
	listener        net.Listener
	mutex           sync.Mutex
	connections     map[int]net.Conn
	nextConnection  int
	commands        []nativeEOFCommand
	values          map[string]int
	armed           bool
	faultKey        string
	faultConnection int
	halfClosed      bool
	stopping        bool
	once            sync.Once
	workers         sync.WaitGroup
}

func newNativeEOFServer(t *testing.T) *nativeEOFServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f := &nativeEOFServer{listener: listener, connections: map[int]net.Conn{}, values: map[string]int{}}
	f.workers.Add(1)
	go func() {
		defer f.workers.Done()
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			f.mutex.Lock()
			if f.stopping {
				f.mutex.Unlock()
				c.Close()
				continue
			}
			f.nextConnection++
			id := f.nextConnection
			f.connections[id] = c
			f.workers.Add(1)
			f.mutex.Unlock()
			go f.serve(c, id)
		}
	}()
	t.Cleanup(f.close)
	return f
}

func (f *nativeEOFServer) close() {
	f.once.Do(func() {
		f.mutex.Lock()
		f.stopping = true
		f.listener.Close()
		for _, c := range f.connections {
			c.Close()
		}
		f.mutex.Unlock()
		f.workers.Wait()
	})
}

func nativeEOFRead(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 4 || line[0] != '*' {
		return nil, fmt.Errorf("RESP array missing")
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || n < 1 || n > 32 {
		return nil, fmt.Errorf("RESP array length invalid")
	}
	args := make([]string, n)
	for i := range args {
		line, err = r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if len(line) < 4 || line[0] != '$' {
			return nil, fmt.Errorf("RESP bulk missing")
		}
		size, err := strconv.Atoi(strings.TrimSpace(line[1:]))
		if err != nil || size < 0 || size > 1<<20 {
			return nil, fmt.Errorf("RESP bulk length invalid")
		}
		data := make([]byte, size+2)
		if _, err = io.ReadFull(r, data); err != nil {
			return nil, err
		}
		if string(data[size:]) != "\r\n" {
			return nil, fmt.Errorf("RESP bulk terminator invalid")
		}
		args[i] = string(data[:size])
	}
	return args, nil
}

func (f *nativeEOFServer) serve(c net.Conn, id int) {
	defer f.workers.Done()
	defer c.Close()
	reader := bufio.NewReader(c)
	for {
		args, err := nativeEOFRead(reader)
		if err != nil {
			return
		}
		command := strings.ToUpper(args[0])
		reply := ":1\r\n"
		withhold := false
		closeWrite := false
		f.mutex.Lock()
		entry := nativeEOFCommand{AtUTC: time.Now().UTC().Format(time.RFC3339Nano), Connection: id, Arguments: append([]string(nil), args...)}
		switch command {
		case "PING":
			reply = "+PONG\r\n"
		case "AUTH", "SELECT", "SET":
			reply = "+OK\r\n"
		case "CLUSTER":
			host, port, _ := net.SplitHostPort(f.listener.Addr().String())
			reply = fmt.Sprintf("*1\r\n*3\r\n:0\r\n:16383\r\n*3\r\n$%d\r\n%s\r\n:%s\r\n$4\r\nfake\r\n", len(host), host, port)
		case "INCRBY":
			if len(args) != 3 {
				f.mutex.Unlock()
				return
			}
			amount, parseErr := strconv.Atoi(args[2])
			if parseErr != nil {
				f.mutex.Unlock()
				return
			}
			f.values[args[1]] += amount
			reply = fmt.Sprintf(":%d\r\n", f.values[args[1]])
			if f.armed {
				f.armed = false
				f.faultKey = args[1]
				f.faultConnection = id
			}
		}
		if id == f.faultConnection {
			withhold = true
			entry.FaultBoundary = true
			// Read the complete same-key pair before FIN. Unread queued bytes can
			// cause RST rather than EOF and would exercise a different native error.
			if command == "EXPIRE" && len(args) > 1 && args[1] == f.faultKey && !f.halfClosed {
				closeWrite = true
			}
		}
		index := len(f.commands)
		f.commands = append(f.commands, entry)
		f.mutex.Unlock()
		if closeWrite {
			tcp, ok := c.(*net.TCPConn)
			if !ok {
				return
			}
			if err := tcp.CloseWrite(); err != nil {
				return
			}
			f.mutex.Lock()
			f.halfClosed = true
			f.mutex.Unlock()
		}
		if withhold {
			continue
		}
		if _, err := io.WriteString(c, reply); err != nil {
			return
		}
		f.mutex.Lock()
		f.commands[index].ReplyWritten = true
		f.mutex.Unlock()
	}
}

type nativeEOFCall struct {
	Name           string `json:"name"`
	StartedAtUTC   string `json:"rpc_started_at_utc"`
	CompletedAtUTC string `json:"rpc_completed_at_utc"`
	Code           string `json:"grpc_code"`
	Message        string `json:"rpc_error_message"`
	ResponseValid  bool   `json:"response_valid"`
}

func TestNativeWrappedEOFThroughGRPC(t *testing.T) {
	f := newNativeEOFServer(t)
	var calls []nativeEOFCall
	wrapper := redis.FakeRedisForTest{Addr: f.listener.Addr().String(), Close: f.close}
	svc := startService(t, &wrapper, nil, func(s *settings.Settings) {
		s.RedisType = "CLUSTER"
		s.RedisPoolSize = 10
		s.RedisPipelineWindow = 150 * time.Microsecond
		s.RedisTimeout = 250 * time.Millisecond
		s.RedisAuth = ""
		s.RedisTls = false
		s.TracingEnabled = false
		s.LogLevel = "warn"
	})
	var previousStart time.Time
	invoke := func(name string) (*pb.RateLimitResponse, error) {
		if !previousStart.IsZero() {
			if delay := time.Until(previousStart.Add(time.Second)); delay > 0 {
				time.Sleep(delay)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		started := time.Now()
		previousStart = started
		response, err := svc.client.ShouldRateLimit(ctx, &pb.RateLimitRequest{Domain: "e2e", Descriptors: []*common.RateLimitDescriptor{{Entries: []*common.RateLimitDescriptor_Entry{{Key: "minute", Value: name}}}}, HitsAddend: 1})
		completed := time.Now().UTC()
		code := status.Code(err)
		message := ""
		if err != nil {
			message = status.Convert(err).Message()
		}
		valid := err == nil && response != nil && response.OverallCode == pb.RateLimitResponse_OK && len(response.Statuses) == 1
		calls = append(calls, nativeEOFCall{Name: name, StartedAtUTC: started.UTC().Format(time.RFC3339Nano), CompletedAtUTC: completed.Format(time.RFC3339Nano), Code: code.String(), Message: message, ResponseValid: valid})
		return response, err
	}
	response, err := invoke("baseline")
	require.NoError(t, err)
	require.Equal(t, pb.RateLimitResponse_OK, response.OverallCode)
	f.mutex.Lock()
	f.armed = true
	f.mutex.Unlock()
	_, err = invoke("fault")
	require.Error(t, err)
	require.Equal(t, codes.Unknown, status.Code(err))
	require.Equal(t, "response returned from Conn: EOF", status.Convert(err).Message())
	response, err = invoke("fresh-1")
	require.NoError(t, err)
	require.Equal(t, pb.RateLimitResponse_OK, response.OverallCode)
	response, err = invoke("fresh-2")
	require.NoError(t, err)
	require.Equal(t, pb.RateLimitResponse_OK, response.OverallCode)
	failedAt, parseErr := time.Parse(time.RFC3339Nano, calls[1].CompletedAtUTC)
	require.NoError(t, parseErr)
	freshAt, parseErr := time.Parse(time.RFC3339Nano, calls[2].CompletedAtUTC)
	require.NoError(t, parseErr)
	require.GreaterOrEqual(t, freshAt.Sub(failedAt), time.Duration(0))
	require.LessOrEqual(t, freshAt.Sub(failedAt), 2*time.Second, "fresh native success must arrive within the bounded recovery window")
	f.mutex.Lock()
	defer f.mutex.Unlock()
	counts := map[string]int{}
	for _, command := range f.commands {
		if command.FaultBoundary {
			counts[command.Arguments[0]]++
			require.False(t, command.ReplyWritten)
		}
	}
	require.Equal(t, map[string]int{"INCRBY": 1, "EXPIRE": 1}, counts, "fully received fault pipeline must not replay")
	require.Equal(t, 1, f.values[f.faultKey])
	require.True(t, f.halfClosed)
	// Unique descriptor values yield one separate Redis key per requested call.
	// The startup readiness key is separate; every observed key increments once.
	require.Len(t, calls, 4)
	for _, count := range f.values {
		require.Equal(t, 1, count, "native request pipeline repeated")
	}
}

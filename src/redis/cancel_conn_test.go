package redis

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mediocregopher/radix/v4"
	"github.com/mediocregopher/radix/v4/resp"
	"github.com/mediocregopher/radix/v4/resp/resp3"
	"github.com/stretchr/testify/require"
)

type pipeDialer struct{ conn net.Conn }

func (d pipeDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return d.conn, nil
}

func TestExperimentCloseOnCancelPreservesPreCanceledAndCompletedSocket(t *testing.T) {
	var calls atomic.Int64
	base := radix.NewStubConn("tcp", "stub", func(context.Context, []string) interface{} {
		calls.Add(1)
		return "PONG"
	})
	t.Cleanup(func() { _ = base.Close() })
	conn := cancelClosingConn{Conn: base}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, conn.Do(canceled, radix.Cmd(nil, "PING")), context.Canceled)
	require.Zero(t, calls.Load(), "pre-canceled call must not write a command")

	finished, cancelFinished := context.WithCancel(context.Background())
	var pong string
	require.NoError(t, conn.Do(finished, radix.Cmd(&pong, "PING")))
	require.Equal(t, "PONG", pong)
	cancelFinished()
	require.NoError(t, conn.Do(context.Background(), radix.Cmd(&pong, "PING")),
		"canceling an already-completed call must not close a healthy socket")
	require.Equal(t, int64(2), calls.Load())
}

func TestExperimentCloseOnCancelPreservesAuthAndClusterReadOnly(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close(); _ = clientSide.Close() })
	commands := make(chan string, 3)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		reader := bufio.NewReader(serverSide)
		for {
			var args []string
			if err := resp3.Unmarshal(reader, &args, resp.NewOpts()); err != nil {
				return
			}
			if len(args) == 0 {
				return
			}
			commands <- args[0]
			reply := "+OK\r\n"
			if args[0] == "PING" {
				reply = "+PONG\r\n"
			}
			if _, err := serverSide.Write([]byte(reply)); err != nil {
				return
			}
		}
	}()
	base := radix.Dialer{NetDialer: pipeDialer{conn: clientSide}, AuthPass: "experimental-pass"}
	conn, err := wrapDialerCloseOnCancel(base, true, time.Second).Dial(context.Background(), "tcp", "pipe")
	require.NoError(t, err)
	var pong string
	require.NoError(t, conn.Do(context.Background(), radix.Cmd(&pong, "PING")))
	require.Equal(t, "PONG", pong)
	require.Equal(t, "AUTH", <-commands)
	require.Equal(t, "READONLY", <-commands)
	require.Equal(t, "PING", <-commands)
	_ = conn.Close()
	<-serverDone
}

func TestExperimentCloseOnCancelBoundsBootstrapAuthAndReadOnly(t *testing.T) {
	for _, tc := range []struct {
		name        string
		auth        bool
		cluster     bool
		wantCommand string
	}{
		{name: "AUTH", auth: true, wantCommand: "AUTH"},
		{name: "READONLY", cluster: true, wantCommand: "READONLY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientSide, serverSide := net.Pipe()
			t.Cleanup(func() { _ = serverSide.Close(); _ = clientSide.Close() })
			observed := make(chan string, 1)
			go func() {
				reader := bufio.NewReader(serverSide)
				var args []string
				if err := resp3.Unmarshal(reader, &args, resp.NewOpts()); err != nil {
					return
				}
				if len(args) > 0 {
					observed <- args[0]
				}
				_, _ = io.Copy(io.Discard, reader)
			}()
			base := radix.Dialer{NetDialer: pipeDialer{conn: clientSide}}
			if tc.auth {
				base.AuthPass = "experimental-pass"
			}
			done := make(chan error, 1)
			go func() {
				conn, err := wrapDialerCloseOnCancel(base, tc.cluster, 40*time.Millisecond).Dial(context.Background(), "tcp", "pipe")
				if conn != nil {
					_ = conn.Close()
				}
				done <- err
			}()
			select {
			case command := <-observed:
				require.Equal(t, tc.wantCommand, command)
			case <-time.After(500 * time.Millisecond):
				t.Fatal("bootstrap command was not written")
			}
			select {
			case err := <-done:
				require.Error(t, err)
			case <-time.After(500 * time.Millisecond):
				t.Fatal("bootstrap command did not return at its bounded deadline")
			}
		})
	}
}

func TestExperimentCloseOnCancelBoundsBootstrapWrites(t *testing.T) {
	for _, tc := range []struct {
		name    string
		auth    bool
		cluster bool
	}{
		{name: "AUTH write", auth: true},
		{name: "READONLY write", cluster: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientSide, serverSide := net.Pipe()
			t.Cleanup(func() { _ = serverSide.Close(); _ = clientSide.Close() })
			// The peer never reads a byte, so bufio.Flush cannot complete.
			base := radix.Dialer{NetDialer: pipeDialer{conn: clientSide}}
			if tc.auth {
				base.AuthPass = "experimental-pass"
			}
			done := make(chan error, 1)
			go func() {
				conn, err := wrapDialerCloseOnCancel(base, tc.cluster, 40*time.Millisecond).Dial(context.Background(), "tcp", "pipe")
				if conn != nil {
					_ = conn.Close()
				}
				done <- err
			}()
			select {
			case err := <-done:
				require.Error(t, err)
			case <-time.After(500 * time.Millisecond):
				t.Fatal("bootstrap write did not stop at its deadline")
			}
		})
	}
}

func TestExperimentCloseOnCancelPreservesTLSAuthAndFlushInterval(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"redis.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	commands := make(chan string, 3)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		server := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{cert}})
		defer server.Close()
		reader := bufio.NewReader(server)
		for {
			var args []string
			if err := resp3.Unmarshal(reader, &args, resp.NewOpts()); err != nil {
				return
			}
			if len(args) == 0 {
				return
			}
			commands <- args[0]
			reply := "+OK\r\n"
			if args[0] == "PING" {
				reply = "+PONG\r\n"
			}
			if _, err := server.Write([]byte(reply)); err != nil {
				return
			}
		}
	}()
	base := radix.Dialer{
		NetDialer: &tls.Dialer{
			NetDialer: &net.Dialer{},
			Config:    &tls.Config{InsecureSkipVerify: true}, // self-signed loopback test certificate
		},
		AuthPass: "experimental-pass", WriteFlushInterval: 150 * time.Microsecond,
	}
	conn, err := wrapDialerCloseOnCancel(base, true, time.Second).Dial(context.Background(), "tcp", listener.Addr().String())
	require.NoError(t, err)
	var pong string
	require.NoError(t, conn.Do(context.Background(), radix.Cmd(&pong, "PING")))
	require.Equal(t, "PONG", pong)
	for _, want := range []string{"AUTH", "READONLY", "PING"} {
		require.Equal(t, want, <-commands)
	}
	_ = conn.Close()
	<-serverDone
}

func TestExperimentCloseOnCancelComposesWithReadOnlyRetirement(t *testing.T) {
	var dials atomic.Int64
	base := radix.Dialer{CustomConn: func(ctx context.Context, network, addr string) (radix.Conn, error) {
		demoted := dials.Add(1) == 1
		return radix.NewStubConn(network, addr, func(_ context.Context, args []string) interface{} {
			if args[0] == "READONLY" {
				return "OK"
			}
			if demoted {
				return readOnlyReply()
			}
			return "OK"
		}), nil
	}}
	dialer := wrapDialerCloseOnReadOnly(wrapDialerCloseOnCancel(base, true, time.Second))
	pool, err := (radix.PoolConfig{Dialer: dialer, Size: 1, PingInterval: -1}).New(context.Background(), "tcp", "stub")
	require.NoError(t, err)
	defer pool.Close()
	err = pool.Do(context.Background(), radix.Cmd(nil, "SET", "key", "value"))
	require.Error(t, err)
	require.True(t, isReadOnlyError(err))
	require.Eventually(t, func() bool {
		var response string
		return pool.Do(context.Background(), radix.Cmd(&response, "SET", "key", "value")) == nil && response == "OK"
	}, time.Second, 10*time.Millisecond)
	require.GreaterOrEqual(t, dials.Load(), int64(2))
}

// A net.Pipe write cannot finish until the peer reads. This proves that the
// wrapper's context callback closes the socket even when Radix EncodeDecode
// cannot first return from its writer Flush.
func TestExperimentCloseOnCancelInterruptsBlockedWrite(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close(); _ = clientSide.Close() })
	dialer := wrapDialerCloseOnCancel(radix.Dialer{NetDialer: pipeDialer{conn: clientSide}}, false, time.Second)
	conn, err := dialer.Dial(context.Background(), "tcp", "pipe")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- conn.Do(ctx, radix.Cmd(nil, "PING")) }()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("blocked Redis write did not return after context cancellation")
	}
}

// A canceled shared operation closes the socket for every caller using it.
// The second PING has ample time left and would receive a valid response if
// the peer were allowed to write, but its socket is retired with the first.
func TestExperimentCloseOnCancelSharedSocketCollateral(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close(); _ = clientSide.Close() })
	commands := make(chan string, 2)
	releaseReplies := make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseReplies) })
	defer release()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		reader := bufio.NewReader(serverSide)
		for range 2 {
			var args []string
			if err := resp3.Unmarshal(reader, &args, resp.NewOpts()); err != nil {
				return
			}
			commands <- args[0]
		}
		<-releaseReplies
		_, _ = serverSide.Write([]byte("+PONG\r\n+PONG\r\n"))
	}()
	base, err := (radix.Dialer{NetDialer: pipeDialer{conn: clientSide}}).Dial(context.Background(), "tcp", "pipe")
	require.NoError(t, err)
	conn := cancelClosingConn{Conn: base}
	defer conn.Close()

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- conn.Do(firstCtx, radix.Cmd(nil, "PING")) }()
	select {
	case command := <-commands:
		require.Equal(t, "PING", command)
	case <-time.After(time.Second):
		t.Fatal("first PING was not written")
	}
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), time.Second)
	defer cancelSecond()
	secondDone := make(chan error, 1)
	go func() { secondDone <- conn.Do(secondCtx, radix.Cmd(nil, "PING")) }()
	select {
	case command := <-commands:
		require.Equal(t, "PING", command)
	case <-time.After(time.Second):
		t.Fatal("second PING was not written")
	}
	cancelFirst()
	select {
	case err := <-firstDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled PING did not return")
	}
	select {
	case err := <-secondDone:
		require.Error(t, err, "the other shared call must observe the retired socket")
		require.NoError(t, secondCtx.Err(), "the other call's own deadline has not expired")
	case <-time.After(time.Second):
		t.Fatal("other shared call did not return after socket retirement")
	}
	release()
	<-serverDone
}

// This isolates the per-operation callback cost from Redis/network latency.
// It is a local microbenchmark, not a production throughput qualification.
func BenchmarkExperimentCancelClosingConnHealthy(b *testing.B) {
	for _, tc := range []struct {
		name    string
		wrapped bool
	}{
		{name: "default"},
		{name: "close_on_cancel", wrapped: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			base := radix.NewStubConn("tcp", "stub", func(context.Context, []string) interface{} { return "PONG" })
			defer base.Close()
			var conn radix.Conn = base
			if tc.wrapped {
				conn = cancelClosingConn{Conn: base}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				var response string
				err := conn.Do(ctx, radix.Cmd(&response, "PING"))
				cancel()
				if err != nil || response != "PONG" {
					b.Fatalf("PING: response=%q err=%v", response, err)
				}
			}
		})
	}
}

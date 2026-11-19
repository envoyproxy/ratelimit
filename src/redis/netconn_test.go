package redis

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFakeRedisClient(t *testing.T, addr string, poolSize int, useTls bool, tlsConfig *tls.Config, opts clientOptions) (*clientImpl, stats.Store) {
	t.Helper()
	store := stats.NewStore(stats.NewNullSink(), false)
	c := newClientImpl(context.Background(), store, useTls, "", "tcp", "single", addr, poolSize,
		0, 0, tlsConfig, false, nil, time.Second, "", "", 10*time.Millisecond, 100*time.Millisecond,
		time.Second, 1, false, nil, opts).(*clientImpl)
	t.Cleanup(func() { c.Close() })
	return c, store
}

func incrPipeline(c Client, rcv *uint64) Pipeline {
	p := c.PipeAppend(nil, rcv, "INCRBY", "key", 1)
	return c.PipeAppend(p, nil, "EXPIRE", "key", 60)
}

func TestDeadlineConnBoundsReadsWaitingForAReply(t *testing.T) {
	srv := newFakeRedis(t, nil)
	raw, err := net.Dial("tcp", srv.addr())
	require.NoError(t, err)
	conn := &deadlineConn{Conn: raw, ioTimeout: 50 * time.Millisecond}
	defer conn.Close()

	// A zero read deadline, which radix sets right before reading a reply,
	// becomes now+ioTimeout.
	require.NoError(t, conn.SetReadDeadline(time.Time{}))
	start := time.Now()
	_, err = conn.Read(make([]byte, 1))
	assert.ErrorIs(t, err, ErrIOTimeout)
	assert.False(t, errors.Is(err, os.ErrDeadlineExceeded), "radix must not mistake it for a context cancellation")
	assert.Less(t, time.Since(start), time.Second)

	// Explicit deadlines, used by radix to interrupt a read when a context is
	// done, keep their meaning.
	require.NoError(t, conn.SetReadDeadline(time.Unix(0, 0)))
	_, err = conn.Read(make([]byte, 1))
	assert.ErrorIs(t, err, os.ErrDeadlineExceeded)
}

func TestClientFailsFastWhenServerStopsAnswering(t *testing.T) {
	srv := newFakeRedis(t, nil)
	ioTimeout := 200 * time.Millisecond
	c, store := newFakeRedisClient(t, srv.addr(), 10, false, nil, clientOptions{ioTimeout: ioTimeout})

	var rcv uint64
	require.NoError(t, c.PipeDo(context.Background(), incrPipeline(c, &rcv)))
	assert.Equal(t, uint64(1), rcv)

	srv.setBlackhole(true)

	const calls = 100
	var wg sync.WaitGroup
	errs := make([]error, calls)
	durations := make([]time.Duration, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var rcv uint64
			start := time.Now()
			errs[i] = c.PipeDo(context.Background(), incrPipeline(c, &rcv))
			durations[i] = time.Since(start)
		}(i)
	}
	wg.Wait()

	for i := 0; i < calls; i++ {
		assert.Error(t, errs[i])
		// Every call fails within a few I/O timeouts: one for its own
		// connection, plus pool contention while dead connections are
		// discarded and replaced.
		assert.Less(t, durations[i], 5*ioTimeout, "call %d took %s", i, durations[i])
	}
	assert.Positive(t, store.NewCounter("cx_local_close").Value(), "dead connections must be discarded")

	// Once the server answers again, the pool recovers on its own.
	srv.setBlackhole(false)
	require.Eventually(t, func() bool {
		var rcv uint64
		return c.PipeDo(context.Background(), incrPipeline(c, &rcv)) == nil
	}, 5*time.Second, 50*time.Millisecond)
}

func TestClientWithoutIOTimeoutBlocksOnUnresponsiveServer(t *testing.T) {
	srv := newFakeRedis(t, nil)
	c, _ := newFakeRedisClient(t, srv.addr(), 1, false, nil, clientOptions{})

	srv.setBlackhole(true)
	done := make(chan error, 1)
	go func() {
		var rcv uint64
		done <- c.PipeDo(context.Background(), incrPipeline(c, &rcv))
	}()
	select {
	case err := <-done:
		t.Fatalf("expected the call to block without an I/O timeout, got %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	// Unblock the call so the test can clean up.
	srv.close()
	<-done
}

func TestIOTimeoutDoesNotCloseIdleConnections(t *testing.T) {
	srv := newFakeRedis(t, nil)
	ioTimeout := 50 * time.Millisecond
	c, store := newFakeRedisClient(t, srv.addr(), 1, false, nil, clientOptions{ioTimeout: ioTimeout})

	time.Sleep(5 * ioTimeout)

	var rcv uint64
	require.NoError(t, c.PipeDo(context.Background(), incrPipeline(c, &rcv)))
	assert.Equal(t, uint64(0), store.NewCounter("cx_local_close").Value())
}

func TestIOTimeoutAppliesToTLSConnections(t *testing.T) {
	serverCfg, clientCfg := selfSignedTLS(t)
	srv := newFakeRedis(t, serverCfg)
	ioTimeout := 200 * time.Millisecond
	// clientCfg has no ServerName: the dialer verifies against the dialed host.
	c, _ := newFakeRedisClient(t, srv.addr(), 2, true, clientCfg, clientOptions{ioTimeout: ioTimeout})

	var rcv uint64
	require.NoError(t, c.PipeDo(context.Background(), incrPipeline(c, &rcv)))

	srv.setBlackhole(true)
	start := time.Now()
	err := c.PipeDo(context.Background(), incrPipeline(c, &rcv))
	assert.Error(t, err)
	assert.Less(t, time.Since(start), 5*ioTimeout)
}

func TestTLSVerifiesServerName(t *testing.T) {
	serverCfg, clientCfg := selfSignedTLS(t)
	srv := newFakeRedis(t, serverCfg)
	clientCfg.ServerName = "not-the-server"

	d := newNetDialer(time.Second, clientOptions{ioTimeout: time.Second}, true, clientCfg)
	_, err := d.DialContext(context.Background(), "tcp", srv.addr())
	assert.Error(t, err)
}

func TestNetDialerWorksWithRadixDialer(t *testing.T) {
	srv := newFakeRedis(t, nil)
	conn, err := radix.Dialer{NetDialer: newNetDialer(time.Second, clientOptions{ioTimeout: time.Second}, false, nil)}.
		Dial(context.Background(), "tcp", srv.addr())
	require.NoError(t, err)
	defer conn.Close()

	var pong string
	require.NoError(t, conn.Do(context.Background(), radix.Cmd(&pong, "PING")))
	assert.Equal(t, "PONG", pong)
}

func selfSignedTLS(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fake-redis"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(cert)
	server = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	client = &tls.Config{RootCAs: pool}
	return server, client
}

func TestNetDialerTCPKeepAliveConfig(t *testing.T) {
	assert.Equal(t, net.KeepAliveConfig{Enable: true, Idle: defaultTCPKeepAlive, Interval: defaultTCPKeepAlive},
		newNetDialer(time.Second, clientOptions{}, false, nil).dialer.KeepAliveConfig)
	assert.Equal(t, net.KeepAliveConfig{Enable: true, Idle: 30 * time.Second, Interval: 30 * time.Second},
		newNetDialer(time.Second, clientOptions{tcpKeepAlive: 30 * time.Second}, false, nil).dialer.KeepAliveConfig)

	disabled := newNetDialer(time.Second, clientOptions{tcpKeepAlive: -1}, false, nil).dialer
	assert.False(t, disabled.KeepAliveConfig.Enable)
	assert.Negative(t, disabled.KeepAlive)
}

func TestNetDialerTCPUserTimeoutOnlyWhenSupported(t *testing.T) {
	assert.Nil(t, newNetDialer(time.Second, clientOptions{}, false, nil).dialer.Control)
	d := newNetDialer(time.Second, clientOptions{tcpUserTimeout: time.Second}, false, nil)
	assert.Equal(t, tcpUserTimeoutSupported, d.dialer.Control != nil)
}

func TestSentinelConnectionsHaveNoIOTimeout(t *testing.T) {
	opts := clientOptions{ioTimeout: time.Second, tcpKeepAlive: 30 * time.Second, tcpUserTimeout: 5 * time.Second}
	assert.Equal(t, clientOptions{tcpKeepAlive: 30 * time.Second, tcpUserTimeout: 5 * time.Second}, sentinelClientOptions(opts))

	// radix's pubsub connection to the sentinels reads while waiting for
	// messages that may not come for a long time (EncodeDecode with nothing
	// to write). An I/O timeout would kill it.
	waitForMessage := func(opts clientOptions) error {
		srv := newFakeRedis(t, nil)
		conn, err := createDialer(time.Second, opts, false, nil, "", "").Dial(context.Background(), "tcp", srv.addr())
		require.NoError(t, err)
		defer conn.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		var msg string
		return conn.EncodeDecode(ctx, nil, &msg)
	}
	ioOpts := clientOptions{ioTimeout: 20 * time.Millisecond}
	assert.ErrorIs(t, waitForMessage(ioOpts), ErrIOTimeout, "Redis connections time out")
	err := waitForMessage(sentinelClientOptions(ioOpts))
	assert.ErrorIs(t, err, context.DeadlineExceeded, "sentinel connections wait")
	assert.NotErrorIs(t, err, ErrIOTimeout)
}

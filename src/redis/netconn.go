package redis

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"time"
)

// ErrIOTimeout is returned when Redis does not answer, or does not accept a
// write, within the configured I/O timeout. The connection is closed and the
// pool dials a replacement.
var ErrIOTimeout = errors.New("redis i/o timeout: no reply from server within the configured timeout")

// netDialer is the radix NetDialer used for Redis connections. radix v4 has no
// read or write timeout of its own: once a command is written it waits for the
// reply for as long as the socket stays open, which, for a peer that vanished
// without a reset (e.g. a failover that keeps the IP), is until the kernel
// gives up retransmitting, about 15 minutes with default Linux settings.
// netDialer wraps every connection in a deadlineConn that bounds each read and
// write by ioTimeout, and does the TLS handshake itself so TLS connections are
// bounded too.
type netDialer struct {
	dialer    net.Dialer
	tlsConfig *tls.Config
	ioTimeout time.Duration
}

// defaultTCPKeepAlive is the keepalive radix v4 sets on the TCP connections
// it dials. radix cannot see through our connection wrapper, so we set it.
const defaultTCPKeepAlive = 10 * time.Second

func newNetDialer(dialTimeout time.Duration, opts clientOptions, useTls bool, tlsConfig *tls.Config) *netDialer {
	d := &netDialer{ioTimeout: opts.ioTimeout}
	if dialTimeout > 0 {
		d.dialer.Timeout = dialTimeout
	}
	switch {
	case opts.tcpKeepAlive < 0:
		d.dialer.KeepAliveConfig = net.KeepAliveConfig{Enable: false}
		d.dialer.KeepAlive = -1
	case opts.tcpKeepAlive > 0:
		d.dialer.KeepAliveConfig = net.KeepAliveConfig{Enable: true, Idle: opts.tcpKeepAlive, Interval: opts.tcpKeepAlive}
	default:
		d.dialer.KeepAliveConfig = net.KeepAliveConfig{Enable: true, Idle: defaultTCPKeepAlive, Interval: defaultTCPKeepAlive}
	}
	if opts.tcpUserTimeout > 0 && tcpUserTimeoutSupported {
		userTimeout := opts.tcpUserTimeout
		d.dialer.Control = func(network, _ string, c syscall.RawConn) error {
			if network != "tcp" && network != "tcp4" && network != "tcp6" {
				return nil
			}
			return setTCPUserTimeout(c, userTimeout)
		}
	}
	if useTls {
		if tlsConfig == nil {
			tlsConfig = &tls.Config{}
		}
		d.tlsConfig = tlsConfig
	}
	return d
}

func (d *netDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := d.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	if d.ioTimeout > 0 {
		conn = &deadlineConn{Conn: conn, ioTimeout: d.ioTimeout}
	}
	if d.tlsConfig == nil {
		return conn, nil
	}

	cfg := d.tlsConfig
	if cfg.ServerName == "" {
		// Same as tls.Dialer: verify against the host we dialed.
		cfg = cfg.Clone()
		host, _, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			host = addr
		}
		cfg.ServerName = host
	}
	if d.dialer.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.dialer.Timeout)
		defer cancel()
	}
	tlsConn := tls.Client(conn, cfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

// deadlineConn bounds every read and write on a Redis connection by ioTimeout.
//
// radix clears the read deadline (SetReadDeadline with a zero time) right
// before it reads a reply, and only then, so an idle connection is never
// timed out. deadlineConn turns that zero deadline into now+ioTimeout: a reply
// must start arriving within ioTimeout of radix starting to wait for it.
// Explicit deadlines, which radix uses to interrupt a read when a caller's
// context is done, pass through unchanged.
//
// When one of our deadlines expires, Read returns ErrIOTimeout rather than
// os.ErrDeadlineExceeded. radix treats os.ErrDeadlineExceeded as a context
// cancellation and keeps the connection; any other error makes it close the
// connection, and the pool dial a new one, which is what a dead peer needs.
type deadlineConn struct {
	net.Conn
	ioTimeout time.Duration
	// livenessDeadline is true while the active read deadline is one we set.
	livenessDeadline atomic.Bool
}

func (c *deadlineConn) SetReadDeadline(t time.Time) error {
	if t.IsZero() {
		c.livenessDeadline.Store(true)
		return c.Conn.SetReadDeadline(time.Now().Add(c.ioTimeout))
	}
	c.livenessDeadline.Store(false)
	return c.Conn.SetReadDeadline(t)
}

func (c *deadlineConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.Conn.SetWriteDeadline(t)
}

func (c *deadlineConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err != nil && c.livenessDeadline.Load() && errors.Is(err, os.ErrDeadlineExceeded) {
		err = ErrIOTimeout
	}
	return n, err
}

func (c *deadlineConn) Write(b []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(c.ioTimeout)); err != nil {
		return 0, err
	}
	n, err := c.Conn.Write(b)
	if err != nil && errors.Is(err, os.ErrDeadlineExceeded) {
		err = ErrIOTimeout
	}
	return n, err
}

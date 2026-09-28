package redis

import (
	"context"
	"net"
	"time"

	"github.com/mediocregopher/radix/v4"
	"github.com/mediocregopher/radix/v4/resp"
)

// cancelClosingConn retires a shared socket immediately upon caller
// cancellation, even when EncodeDecode is blocked in a queued read or write.
// Radix v4 otherwise treats an unread
// canceled response as usable, then waits indefinitely to discard it. Closing
// the socket interrupts that background discard, including for the pool's
// internal PING and a cluster's topology Sync.
type cancelClosingConn struct {
	radix.Conn
}

func (c cancelClosingConn) EncodeDecode(ctx context.Context, toWrite, toRead interface{}) error {
	// A context which was already canceled cannot have written a command;
	// closing this pooled socket would only disrupt unrelated callers.
	if err := ctx.Err(); err != nil {
		return resp.ErrConnUsable{Err: err}
	}
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(callbackDone)
		_ = c.Conn.Close()
	})
	err := c.Conn.EncodeDecode(ctx, toWrite, toRead)
	if !stop() {
		// Once the callback has started, wait for it to finish before the
		// pool can reuse this connection after a successful-looking read.
		<-callbackDone
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		// A concurrent Redis RESP error must not mask cancellation: Radix's
		// exclusive pool path otherwise treats it as a valid application reply
		// and can return this now-closed socket to the pool.
		return resp.ErrConnUnusable(ctxErr)
	}
	return err
}

func (c cancelClosingConn) Do(ctx context.Context, action radix.Action) error {
	return action.Perform(ctx, c)
}

// bootstrapClosingConn closes its socket if AUTH, SELECT, or another startup
// command remains blocked after the bootstrap context expires.
type bootstrapClosingConn struct {
	net.Conn
	stop func() bool
	done chan struct{}
}

func (c *bootstrapClosingConn) disarm() {
	if !c.stop() {
		<-c.done
	}
}

func (c *bootstrapClosingConn) SetKeepAlive(enabled bool) error {
	if tcp, ok := c.Conn.(interface{ SetKeepAlive(bool) error }); ok {
		return tcp.SetKeepAlive(enabled)
	}
	return nil
}

func (c *bootstrapClosingConn) SetKeepAlivePeriod(period time.Duration) error {
	if tcp, ok := c.Conn.(interface{ SetKeepAlivePeriod(time.Duration) error }); ok {
		return tcp.SetKeepAlivePeriod(period)
	}
	return nil
}

type bootstrapNetDialer struct {
	base interface {
		DialContext(context.Context, string, string) (net.Conn, error)
	}
	ctx    context.Context
	onConn func(*bootstrapClosingConn)
}

func (d bootstrapNetDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := d.base.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	wrapped := &bootstrapClosingConn{Conn: conn, done: make(chan struct{})}
	wrapped.stop = context.AfterFunc(d.ctx, func() {
		defer close(wrapped.done)
		_ = conn.Close()
	})
	d.onConn(wrapped)
	return wrapped, nil
}

// wrapDialerCloseOnCancel captures the fully configured dialer so that TLS,
// AUTH, and write buffering stay in effect. Install it before any other
// CustomConn wrapper so that it can intercept the original NetDialer.
// Radix disables automatic cluster READONLY when CustomConn is set, so this
// wrapper performs READONLY explicitly through cancelClosingConn.
func wrapDialerCloseOnCancel(base radix.Dialer, cluster bool, bootstrapTimeout time.Duration) radix.Dialer {
	effectiveTimeout := bootstrapTimeout
	if effectiveTimeout <= 0 {
		effectiveTimeout = 10 * time.Second
	}
	baseNetDialer := base.NetDialer
	if baseNetDialer == nil {
		baseNetDialer = new(net.Dialer)
	}
	return radix.Dialer{CustomConn: func(ctx context.Context, network, addr string) (radix.Conn, error) {
		bootstrapCtx, cancel := context.WithTimeout(ctx, effectiveTimeout)
		defer cancel()
		var bootstrapSocket *bootstrapClosingConn
		cloned := base
		cloned.NetDialer = bootstrapNetDialer{
			base:   baseNetDialer,
			ctx:    bootstrapCtx,
			onConn: func(conn *bootstrapClosingConn) { bootstrapSocket = conn },
		}
		conn, err := cloned.Dial(bootstrapCtx, network, addr)
		if bootstrapSocket != nil {
			bootstrapSocket.disarm()
		}
		if err != nil {
			return nil, err
		}
		if err := bootstrapCtx.Err(); err != nil {
			_ = conn.Close()
			return nil, err
		}
		wrapped := cancelClosingConn{Conn: conn}
		if cluster {
			if err := wrapped.Do(bootstrapCtx, radix.Cmd(nil, "READONLY")); err != nil {
				_ = wrapped.Close()
				return nil, err
			}
		}
		return wrapped, nil
	}}
}

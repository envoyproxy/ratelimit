package redis

import (
	"context"
	"errors"
	"strings"

	"github.com/mediocregopher/radix/v4"
	"github.com/mediocregopher/radix/v4/resp"
	"github.com/mediocregopher/radix/v4/resp/resp3"
	logger "github.com/sirupsen/logrus"
)

// wrappedConn wraps a radix.Conn to correct which errors make the enclosing
// pool discard the connection. The pool discards a connection when a command
// on it fails with an error not wrapped in resp.ErrConnUsable.
type wrappedConn struct {
	radix.Conn
	closeOnReadOnly bool
}

func (c wrappedConn) EncodeDecode(ctx context.Context, toWrite, toRead interface{}) error {
	// radix reports a context that is done before the command is queued on
	// the connection as a connection error, which makes the pool discard a
	// healthy connection, and doing so stalls every other command on the pool
	// while it takes the pool's write lock. Calls blocked on that lock are the
	// ones most likely to see their context expire, so under load a slow
	// Redis turns into a steady stream of discarded connections. Reject those
	// commands here as radix does for its other context checks: as an error
	// that keeps the connection.
	if err := ctx.Err(); err != nil {
		return resp.ErrConnUsable{Err: err}
	}

	err := c.Conn.EncodeDecode(ctx, toWrite, toRead)
	if err == nil {
		return nil
	}
	if isUnqueuedContextError(err) {
		return resp.ErrConnUsable{Err: err}
	}
	if c.closeOnReadOnly && isReadOnlyError(err) {
		// A READONLY reply means the server behind this connection is a
		// replica. Redis-compatible deployments that fail over by repointing
		// an address at the new master (a Kubernetes Service, DNS, or a
		// proxy — e.g. Redis without Sentinel, Dragonfly, KeyDB) leave the
		// demoted master running, and it keeps already-established
		// connections open. radix only discards pooled connections on IO
		// errors, so those stale connections are reused forever and every
		// write on them keeps failing with READONLY until the process
		// restarts. Discarding the connection instead makes the pool
		// reconnect through the configured address, which reaches the
		// current master.
		logger.Warnf("redis connection to %s returned READONLY (server demoted to replica); discarding connection so the pool reconnects to the current master", c.Addr())
		// Stripping the resp.ErrConnUsable wrapper tells the pool to discard
		// this connection; callers still receive the underlying resp error.
		return resp.ErrConnUnusable(err)
	}
	return err
}

// Do routes the Action through this wrapper (rather than the embedded Conn)
// so the Action's EncodeDecode calls hit the checks above.
func (c wrappedConn) Do(ctx context.Context, a radix.Action) error {
	return a.Perform(ctx, c)
}

// isUnqueuedContextError reports whether err is radix's error for a context
// that was done before the command could be queued on the connection: nothing
// was written, so the connection is still usable. Any other context error,
// such as one interrupting a partially read reply, leaves the connection
// unusable, and radix closes it itself.
func isUnqueuedContextError(err error) bool {
	return !errors.As(err, new(resp.ErrConnUsable)) &&
		(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) &&
		strings.HasPrefix(err.Error(), "writing EncodeDecode to Conn channel")
}

func isReadOnlyError(err error) bool {
	var respErr resp3.SimpleError
	return errors.As(err, &respErr) && strings.HasPrefix(respErr.S, "READONLY")
}

// wrapDialerConn returns a Dialer whose connections are wrapped in a
// wrappedConn. closeOnReadOnly makes connections whose commands fail with a
// READONLY error reply mark themselves unusable. It must be built from the
// fully-configured base Dialer: radix uses CustomConn in place of every other
// Dialer field, so the base Dialer is captured here to keep TLS, auth,
// timeouts, and write buffering working.
func wrapDialerConn(base radix.Dialer, closeOnReadOnly bool) radix.Dialer {
	return radix.Dialer{
		CustomConn: func(ctx context.Context, network, addr string) (radix.Conn, error) {
			conn, err := base.Dial(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return wrappedConn{Conn: conn, closeOnReadOnly: closeOnReadOnly}, nil
		},
	}
}

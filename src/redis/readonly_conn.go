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

// readOnlyClosingConn wraps a radix.Conn so that a READONLY error reply marks
// the connection as unusable, which makes the enclosing pool discard it and
// dial a replacement.
//
// A READONLY reply means the server behind this connection is a replica.
// Redis-compatible deployments that fail over by repointing an address at the
// new master (a Kubernetes Service, DNS, or a proxy — e.g. Redis without
// Sentinel, Dragonfly, KeyDB) leave the demoted master running, and it keeps
// already-established connections open. radix only discards pooled
// connections on IO errors, so those stale connections are reused forever and
// every write on them keeps failing with READONLY until the process restarts.
// Discarding the connection instead makes the pool reconnect through the
// configured address, which reaches the current master.
type readOnlyClosingConn struct {
	radix.Conn
}

func (c readOnlyClosingConn) EncodeDecode(ctx context.Context, toWrite, toRead interface{}) error {
	err := c.Conn.EncodeDecode(ctx, toWrite, toRead)
	if isReadOnlyError(err) {
		logger.Warnf("redis connection to %s returned READONLY (server demoted to replica); discarding connection so the pool reconnects to the current master", c.Addr())
		// Stripping the resp.ErrConnUsable wrapper tells the pool to discard
		// this connection; callers still receive the underlying resp error.
		return resp.ErrConnUnusable(err)
	}
	return err
}

// Do routes the Action through this wrapper (rather than the embedded Conn)
// so the Action's EncodeDecode calls hit the READONLY detection above.
func (c readOnlyClosingConn) Do(ctx context.Context, a radix.Action) error {
	return a.Perform(ctx, c)
}

func isReadOnlyError(err error) bool {
	var respErr resp3.SimpleError
	return errors.As(err, &respErr) && strings.HasPrefix(respErr.S, "READONLY")
}

// wrapDialerCloseOnReadOnly returns a Dialer whose connections mark themselves
// unusable when a command fails with a READONLY error reply. It must be built
// from the fully-configured base Dialer: radix uses CustomConn in place of
// every other Dialer field, so the base Dialer is captured here to keep TLS,
// auth, timeouts, and write buffering working.
func wrapDialerCloseOnReadOnly(base radix.Dialer) radix.Dialer {
	return radix.Dialer{
		CustomConn: func(ctx context.Context, network, addr string) (radix.Conn, error) {
			conn, err := base.Dial(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return readOnlyClosingConn{Conn: conn}, nil
		},
	}
}

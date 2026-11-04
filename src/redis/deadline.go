package redis

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

// ErrTooManyInflight is returned when a Redis client already has the maximum
// number of commands in flight (REDIS_MAX_INFLIGHT).
var ErrTooManyInflight = errors.New("too many Redis commands in flight")

// do runs fn, which performs Redis I/O, and returns when fn completes or ctx is
// done, whichever comes first. onSuccess, if set, runs on the caller's
// goroutine after fn succeeded.
//
// radix v4 does not return when a command's context is done: the caller keeps
// waiting until the connection reads the reply or fails. A caller that gave up
// on a reply would then hold the request, and the goroutine serving it, for as
// long as Redis takes to answer, or until the I/O timeout closes the
// connection. Running fn on its own goroutine lets the caller return at its
// deadline; fn finishes in the background, bounded by the I/O timeout. Since
// fn may outlive the call, it must not write into memory the caller owns:
// replies are decoded into private receivers and copied out by onSuccess.
func (c *clientImpl) do(ctx context.Context, fn func(context.Context) error, onSuccess func()) error {
	// radix discards a healthy pooled connection when given an expired
	// context, so do not hand it one.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("not sending Redis command: %w", err)
	}

	if c.inflight != nil {
		select {
		case c.inflight <- struct{}{}:
		default:
			return ErrTooManyInflight
		}
	}
	release := func() {
		if c.inflight != nil {
			<-c.inflight
		}
	}

	if ctx.Done() == nil {
		// The context can never be done: no need for another goroutine.
		defer release()
		err := fn(ctx)
		if err == nil && onSuccess != nil {
			onSuccess()
		}
		return err
	}

	// Do not hand the caller's deadline to radix. When a context is done
	// while radix is reading a reply, radix interrupts the read and, if part
	// of the reply had arrived, closes the connection. The connection was
	// healthy, just slower than the caller's deadline, and the pool discards
	// it and stalls while it does. On a Redis that is slow but healthy, the
	// calls stalled behind each discard miss their deadline in turn, so the
	// connection churn and latency keep feeding each other. The caller does not
	// need radix to honor its deadline (it stops waiting below), so the
	// command runs to completion, bounded by the I/O timeout, and the reply is
	// simply dropped. The backstop bounds a command waiting for a connection
	// and then for its reply when the I/O timeout fires late.
	rctx, cancel := context.WithoutCancel(ctx), context.CancelFunc(func() {})
	if c.ioTimeout > 0 {
		rctx, cancel = context.WithTimeout(rctx, 2*c.ioTimeout)
	}

	done := make(chan error, 1)
	go func() {
		defer release()
		defer cancel()
		done <- fn(rctx)
	}()

	select {
	case err := <-done:
		if err == nil && onSuccess != nil {
			onSuccess()
		}
		return err
	case <-ctx.Done():
		// Prefer a reply that arrived just in time.
		select {
		case err := <-done:
			if err == nil && onSuccess != nil {
				onSuccess()
			}
			return err
		default:
		}
		return fmt.Errorf("gave up waiting for Redis reply: %w", ctx.Err())
	}
}

// newReply returns a private receiver of the same type as rcv, for radix to
// decode a reply into. It is copied into rcv by copyReply once the command
// completed and the caller is still waiting for it.
func newReply(rcv interface{}) interface{} {
	switch rcv.(type) {
	case nil:
		return nil
	case *uint64:
		return new(uint64)
	}
	t := reflect.TypeOf(rcv)
	if t.Kind() != reflect.Pointer {
		return rcv
	}
	return reflect.New(t.Elem()).Interface()
}

func copyReply(rcv, reply interface{}) {
	if rcv == nil || reply == nil {
		return
	}
	switch dst := rcv.(type) {
	case *uint64:
		*dst = *reply.(*uint64)
		return
	}
	dst := reflect.ValueOf(rcv)
	if dst.Kind() != reflect.Pointer || dst.Pointer() == reflect.ValueOf(reply).Pointer() {
		return
	}
	dst.Elem().Set(reflect.ValueOf(reply).Elem())
}

func commitReplies(pipeline Pipeline) {
	for _, pa := range pipeline {
		copyReply(pa.rcv, pa.reply)
	}
}

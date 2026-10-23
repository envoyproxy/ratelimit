package redis

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mediocregopher/radix/v4"
	"github.com/mediocregopher/radix/v4/resp"
	"github.com/mediocregopher/radix/v4/resp/resp3"
	"github.com/mediocregopher/radix/v4/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const readOnlyErrMsg = "READONLY You can't write against a read only replica."

// readOnlyReply is what a real connection returns for a READONLY error reply:
// the resp error wrapped in ErrConnUsable (see resp3's unmarshaling).
func readOnlyReply() error {
	return resp.ErrConnUsable{Err: resp3.SimpleError{S: readOnlyErrMsg}}
}

func TestIsReadOnlyError(t *testing.T) {
	assert.True(t, isReadOnlyError(readOnlyReply()))
	assert.True(t, isReadOnlyError(resp3.SimpleError{S: readOnlyErrMsg}))
	assert.False(t, isReadOnlyError(nil))
	assert.False(t, isReadOnlyError(resp.ErrConnUsable{Err: resp3.SimpleError{S: "ERR unknown command"}}))
	assert.False(t, isReadOnlyError(errors.New("READONLY-looking plain error is not a resp error")))
	assert.False(t, isReadOnlyError(&net.OpError{Op: "read", Err: errors.New("connection reset")}))
}

// stubConn returns a radix.Conn whose replies are driven by fn, wrapped the
// same way newClientImpl wraps real connections.
func wrappedStubConn(fn func(ctx context.Context, args []string) interface{}) radix.Conn {
	return readOnlyClosingConn{Conn: radix.NewStubConn("tcp", "127.0.0.1:6379", fn)}
}

func TestReadOnlyClosingConnUnwrapsReadOnlyErrors(t *testing.T) {
	conn := wrappedStubConn(func(ctx context.Context, args []string) interface{} {
		return readOnlyReply()
	})
	defer conn.Close()

	err := conn.Do(context.Background(), radix.Cmd(nil, "SET", "foo", "bar"))
	require.Error(t, err)

	// The caller still sees the resp error...
	var respErr resp3.SimpleError
	assert.True(t, errors.As(err, &respErr))
	assert.Equal(t, readOnlyErrMsg, respErr.S)

	// ...but the ErrConnUsable wrapper is gone, which is what tells the pool
	// to discard this connection.
	assert.False(t, errors.As(err, new(resp.ErrConnUsable)))
}

func TestReadOnlyAwarePipelineDelegatesDoToEmbeddedConn(t *testing.T) {
	conn := wrappedStubConn(func(ctx context.Context, args []string) interface{} {
		assert.Equal(t, []string{"PING"}, args)
		return "PONG"
	})
	defer conn.Close()

	pipeline := newReadOnlyAwarePipeline()
	pipeline.Conn = conn
	defer func() { pipeline.Conn = nil }()

	var res string
	require.NoError(t, pipeline.Do(context.Background(), radix.Cmd(&res, "PING")))
	assert.Equal(t, "PONG", res)
}

func TestReadOnlyAwarePipelineUnwrapsReadOnlyErrors(t *testing.T) {
	conn := wrappedStubConn(func(ctx context.Context, args []string) interface{} {
		switch args[0] {
		case "INCRBY":
			return resp3.SimpleError{S: readOnlyErrMsg}
		case "EXPIRE":
			return "OK"
		default:
			t.Fatalf("unexpected command: %v", args)
			return nil
		}
	})
	defer conn.Close()

	var hits uint64
	pipeline := newReadOnlyAwarePipeline()
	pipeline.Append(radix.FlatCmd(&hits, "INCRBY", "foo", 1))
	pipeline.Append(radix.FlatCmd(nil, "EXPIRE", "foo", 1))

	err := conn.Do(context.Background(), pipeline)
	require.Error(t, err)

	var respErr resp3.SimpleError
	assert.True(t, errors.As(err, &respErr))
	assert.Equal(t, readOnlyErrMsg, respErr.S)

	// A pipeline-level READONLY reply is still a demoted-master signal. It must
	// strip ErrConnUsable so the enclosing pool discards the stale connection.
	assert.False(t, errors.As(err, new(resp.ErrConnUsable)))
}

func TestReadOnlyAwarePipelineKeepsOtherErrorsUsable(t *testing.T) {
	conn := wrappedStubConn(func(ctx context.Context, args []string) interface{} {
		switch args[0] {
		case "INCRBY":
			return resp3.SimpleError{S: "ERR value is not an integer or out of range"}
		case "EXPIRE":
			return "OK"
		default:
			t.Fatalf("unexpected command: %v", args)
			return nil
		}
	})
	defer conn.Close()

	var hits uint64
	pipeline := newReadOnlyAwarePipeline()
	pipeline.Append(radix.FlatCmd(&hits, "INCRBY", "foo", "not-a-number"))
	pipeline.Append(radix.FlatCmd(nil, "EXPIRE", "foo", 1))

	err := conn.Do(context.Background(), pipeline)
	require.Error(t, err)

	var respErr resp3.SimpleError
	assert.True(t, errors.As(err, &respErr))
	assert.Equal(t, "ERR value is not an integer or out of range", respErr.S)

	assert.True(t, errors.As(err, new(resp.ErrConnUsable)))
}

func TestReadOnlyClosingConnKeepsOtherErrorsUsable(t *testing.T) {
	conn := wrappedStubConn(func(ctx context.Context, args []string) interface{} {
		return resp.ErrConnUsable{Err: resp3.SimpleError{S: "ERR value is not an integer or out of range"}}
	})
	defer conn.Close()

	err := conn.Do(context.Background(), radix.Cmd(nil, "INCRBY", "foo", "bar"))
	require.Error(t, err)

	// Ordinary application errors keep the ErrConnUsable wrapper so the pool
	// keeps the connection.
	assert.True(t, errors.As(err, new(resp.ErrConnUsable)))
}

func TestReadOnlyClosingConnPassesSuccessThrough(t *testing.T) {
	conn := wrappedStubConn(func(ctx context.Context, args []string) interface{} {
		return "OK"
	})
	defer conn.Close()

	var res string
	require.NoError(t, conn.Do(context.Background(), radix.Cmd(&res, "SET", "foo", "bar")))
	assert.Equal(t, "OK", res)
}

// TestPoolDiscardsConnOnReadOnly proves the end-to-end mechanism: a READONLY
// reply on a pooled connection makes the pool discard it and dial a fresh one,
// after which commands succeed (as they would against the newly promoted
// master).
func TestPoolDiscardsConnOnReadOnly(t *testing.T) {
	var dials, closes int64

	// The first dialed "server" answers writes with READONLY (a demoted
	// master); every later dial reaches a healthy master.
	dialer := wrapDialerCloseOnReadOnly(radix.Dialer{
		CustomConn: func(ctx context.Context, network, addr string) (radix.Conn, error) {
			demoted := atomic.AddInt64(&dials, 1) == 1
			return radix.NewStubConn(network, addr, func(ctx context.Context, args []string) interface{} {
				if args[0] == "PING" {
					return "PONG"
				}
				if demoted {
					return readOnlyReply()
				}
				return "OK"
			}), nil
		},
	})

	pool, err := (radix.PoolConfig{
		Dialer: dialer,
		Size:   1,
		Trace: trace.PoolTrace{
			ConnClosed: func(trace.PoolConnClosed) { atomic.AddInt64(&closes, 1) },
		},
	}).New(context.Background(), "tcp", "127.0.0.1:6379")
	require.NoError(t, err)
	defer pool.Close()

	// First write hits the demoted master and fails with READONLY.
	err = pool.Do(context.Background(), radix.Cmd(nil, "SET", "foo", "bar"))
	require.Error(t, err)
	var respErr resp3.SimpleError
	assert.True(t, errors.As(err, &respErr))

	// The pool discards the stale connection and reconnects; the retried
	// write reaches the new master.
	assert.Eventually(t, func() bool {
		var res string
		if err := pool.Do(context.Background(), radix.Cmd(&res, "SET", "foo", "bar")); err != nil {
			return false
		}
		return res == "OK"
	}, 5*time.Second, 10*time.Millisecond)

	assert.GreaterOrEqual(t, atomic.LoadInt64(&dials), int64(2))
	assert.GreaterOrEqual(t, atomic.LoadInt64(&closes), int64(1))
}

func TestPoolDiscardsConnOnReadOnlyPipeline(t *testing.T) {
	var dials, closes int64

	dialer := wrapDialerCloseOnReadOnly(radix.Dialer{
		CustomConn: func(ctx context.Context, network, addr string) (radix.Conn, error) {
			demoted := atomic.AddInt64(&dials, 1) == 1
			return radix.NewStubConn(network, addr, func(ctx context.Context, args []string) interface{} {
				switch args[0] {
				case "PING":
					return "PONG"
				case "INCRBY":
					if demoted {
						return resp3.SimpleError{S: readOnlyErrMsg}
					}
					return uint64(1)
				case "EXPIRE":
					if demoted {
						return resp3.SimpleError{S: readOnlyErrMsg}
					}
					return int64(1)
				default:
					t.Fatalf("unexpected command: %v", args)
					return nil
				}
			}), nil
		},
	})

	pool, err := (radix.PoolConfig{
		Dialer:       dialer,
		Size:         1,
		PingInterval: -1,
		Trace: trace.PoolTrace{
			ConnClosed: func(trace.PoolConnClosed) { atomic.AddInt64(&closes, 1) },
		},
	}).New(context.Background(), "tcp", "127.0.0.1:6379")
	require.NoError(t, err)
	defer pool.Close()

	client := &clientImpl{
		client:                         pool,
		closeConnectionOnReadOnlyError: true,
	}

	var hits uint64
	pipeline := client.PipeAppend(Pipeline{}, &hits, "INCRBY", "foo", 1)
	pipeline = client.PipeAppend(pipeline, nil, "EXPIRE", "foo", 1)

	err = client.PipeDo(context.Background(), pipeline)
	require.Error(t, err)
	var respErr resp3.SimpleError
	assert.True(t, errors.As(err, &respErr))
	assert.Equal(t, readOnlyErrMsg, respErr.S)

	assert.Eventually(t, func() bool {
		hits = 0
		pipeline := client.PipeAppend(Pipeline{}, &hits, "INCRBY", "foo", 1)
		pipeline = client.PipeAppend(pipeline, nil, "EXPIRE", "foo", 1)
		return client.PipeDo(context.Background(), pipeline) == nil && hits == 1
	}, 5*time.Second, 10*time.Millisecond)

	assert.GreaterOrEqual(t, atomic.LoadInt64(&dials), int64(2))
	assert.GreaterOrEqual(t, atomic.LoadInt64(&closes), int64(1))
}

var benchmarkPipelineSink pipelineAction

func BenchmarkNewPipelineAppendTwoActions(b *testing.B) {
	var hits uint64
	actions := []radix.Action{
		radix.FlatCmd(&hits, "INCRBY", "foo", 1),
		radix.FlatCmd(nil, "EXPIRE", "foo", 1),
	}

	b.Run("native", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			pipeline := radix.NewPipeline()
			pipeline.Append(actions[0])
			pipeline.Append(actions[1])
			benchmarkPipelineSink = pipeline
		}
	})

	b.Run("read-only-aware", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			pipeline := newReadOnlyAwarePipeline()
			pipeline.Append(actions[0])
			pipeline.Append(actions[1])
			benchmarkPipelineSink = pipeline
		}
	})
}

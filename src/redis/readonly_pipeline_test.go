package redis

import (
	"context"
	"testing"
	"time"

	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newReadOnlyTestClient(t *testing.T, addr string, closeOnReadOnly bool) *clientImpl {
	t.Helper()
	store := stats.NewStore(stats.NewNullSink(), false)
	c := newClientImpl(context.Background(), store, false, "", "tcp", "single", addr, 1,
		0, 0, nil, false, nil, time.Second, "", "", 10*time.Millisecond, 100*time.Millisecond,
		time.Second, 1, closeOnReadOnly, nil, clientOptions{ioTimeout: time.Second}).(*clientImpl)
	t.Cleanup(func() { c.Close() })
	return c
}

// TestPipelineRecoversAfterFailoverWhenClosingOnReadOnly covers the way the
// service writes counters: a pipeline of INCRBY and EXPIRE. After a failover
// that leaves the demoted master running, the pooled connection still points
// at it and every pipeline fails with READONLY. With
// closeConnectionOnReadOnlyError the connection must be discarded so the next
// pipeline reaches the current master.
func TestPipelineRecoversAfterFailoverWhenClosingOnReadOnly(t *testing.T) {
	srv := newFakeRedis(t, nil)
	c := newReadOnlyTestClient(t, srv.addr(), true)

	var rcv uint64
	require.NoError(t, c.PipeDo(context.Background(), incrPipeline(c, &rcv)))
	require.Equal(t, 1, srv.connsAccepted())

	srv.demoteExistingConns()

	// The first pipeline after the failover reaches the demoted master.
	err := c.PipeDo(context.Background(), incrPipeline(c, &rcv))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "READONLY")

	// The connection was discarded, so the next pipeline dials a new one and
	// reaches the current master.
	assert.Eventually(t, func() bool {
		return c.PipeDo(context.Background(), incrPipeline(c, &rcv)) == nil
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, 2, srv.connsAccepted(), "the demoted connection must have been replaced")
}

// TestPipelineKeepsConnectionWhenNotClosingOnReadOnly is the control: without
// the option, the pool keeps reusing the demoted connection.
func TestPipelineKeepsConnectionWhenNotClosingOnReadOnly(t *testing.T) {
	srv := newFakeRedis(t, nil)
	c := newReadOnlyTestClient(t, srv.addr(), false)

	var rcv uint64
	require.NoError(t, c.PipeDo(context.Background(), incrPipeline(c, &rcv)))
	srv.demoteExistingConns()

	for i := 0; i < 5; i++ {
		err := c.PipeDo(context.Background(), incrPipeline(c, &rcv))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "READONLY")
	}
	assert.Equal(t, 1, srv.connsAccepted())
}

// dialFake returns a real radix connection to srv, once the server has
// accepted it.
func dialFake(t *testing.T, srv *fakeRedis) radix.Conn {
	t.Helper()
	conn, err := radix.Dialer{}.Dial(context.Background(), "tcp", srv.addr())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	var pong string
	require.NoError(t, conn.Do(context.Background(), radix.Cmd(&pong, "PING")))
	return conn
}

func TestReadOnlyPipelineReturnsReplies(t *testing.T) {
	conn := dialFake(t, newFakeRedis(t, nil))

	var incr, expire int
	p := newReadOnlyPipeline()
	p.Append(radix.Cmd(&incr, "INCRBY", "k", "1"))
	p.Append(radix.Cmd(&expire, "EXPIRE", "k", "60"))

	require.NoError(t, p.Perform(context.Background(), conn))
	assert.Equal(t, 1, incr)
	assert.Equal(t, 1, expire)
}

func TestReadOnlyPipelineKeepsConnectionOnOtherCommandErrors(t *testing.T) {
	conn := dialFake(t, newFakeRedis(t, nil))

	var incr int
	p := newReadOnlyPipeline()
	p.Append(radix.Cmd(nil, "LPUSH", "k", "v"))
	p.Append(radix.Cmd(&incr, "INCRBY", "k", "1"))

	err := p.Perform(context.Background(), conn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WRONGTYPE")
	assert.True(t, isConnUsable(err), "an error that does not mean the server is a replica must not discard the connection")
	assert.Equal(t, 1, incr, "the commands after the failed one are still answered")

	// The connection is still in step with the server.
	var pong string
	require.NoError(t, conn.Do(context.Background(), radix.Cmd(&pong, "PING")))
	assert.Equal(t, "PONG", pong)
}

func TestReadOnlyPipelineDiscardsConnectionOnReadOnly(t *testing.T) {
	srv := newFakeRedis(t, nil)
	conn := dialFake(t, srv)
	srv.demoteExistingConns()

	p := newReadOnlyPipeline()
	p.Append(radix.Cmd(nil, "INCRBY", "k", "1"))
	p.Append(radix.Cmd(nil, "EXPIRE", "k", "60"))

	err := p.Perform(context.Background(), conn)
	require.Error(t, err)
	assert.True(t, isReadOnlyError(err))
	assert.False(t, isConnUsable(err), "a READONLY reply must make the connection unusable so the pool discards it")
}

func TestReadOnlyPipelineProperties(t *testing.T) {
	p := newReadOnlyPipeline()
	p.Append(radix.Cmd(nil, "INCR", "a"))
	p.Append(radix.Cmd(nil, "EXPIRE", "b", "60"))

	props := p.Properties()
	assert.Equal(t, []string{"a", "b"}, props.Keys)
	assert.True(t, props.CanPipeline)
	assert.True(t, props.CanShareConn)
}

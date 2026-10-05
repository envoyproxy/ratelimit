package redis

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mediocregopher/radix/v4"
	"github.com/mediocregopher/radix/v4/resp/resp3"
	"github.com/mediocregopher/radix/v4/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testAction struct {
	key   string
	delay time.Duration
	err   error
}

func (a *testAction) Properties() radix.ActionProperties {
	return radix.ActionProperties{
		Keys:        []string{a.key},
		CanRetry:    true,
		CanPipeline: true,
	}
}

func (a *testAction) Perform(ctx context.Context, _ radix.Conn) error {
	if a.delay > 0 {
		timer := time.NewTimer(a.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return a.err
}

type recordingRedisClient struct {
	mu          sync.Mutex
	calls       []radix.Action
	inFlight    int
	maxInFlight int
}

func (c *recordingRedisClient) Do(ctx context.Context, action radix.Action) error {
	c.mu.Lock()
	c.calls = append(c.calls, action)
	c.inFlight++
	if c.inFlight > c.maxInFlight {
		c.maxInFlight = c.inFlight
	}
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.inFlight--
		c.mu.Unlock()
	}()

	if action, ok := action.(*testAction); ok {
		return action.Perform(ctx, nil)
	}
	return nil
}

func (c *recordingRedisClient) Close() error {
	return nil
}

func (c *recordingRedisClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *recordingRedisClient) maxConcurrentCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxInFlight
}

func TestEffectiveClusterPipelineParallelism(t *testing.T) {
	tests := []struct {
		name                  string
		configuredParallelism int
		poolSize              int
		want                  int
	}{
		{
			name:                  "serial legacy behavior",
			configuredParallelism: 1,
			poolSize:              10,
			want:                  1,
		},
		{
			name:                  "auto uses pool size",
			configuredParallelism: 0,
			poolSize:              10,
			want:                  10,
		},
		{
			name:                  "bounded below pool size",
			configuredParallelism: 8,
			poolSize:              10,
			want:                  8,
		},
		{
			name:                  "configured value is capped to pool size",
			configuredParallelism: 20,
			poolSize:              10,
			want:                  10,
		},
		{
			name:                  "pool ceiling never produces zero",
			configuredParallelism: 0,
			poolSize:              0,
			want:                  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, effectiveClusterPipelineParallelism(tt.configuredParallelism, tt.poolSize))
		})
	}
}

func TestEffectiveClusterPipelineParallelismRejectsNegativeConfig(t *testing.T) {
	assert.Panics(t, func() {
		effectiveClusterPipelineParallelism(-1, 10)
	})
}

func TestExecuteGroupedPipelineSingleActionFastPath(t *testing.T) {
	fakeClient := &recordingRedisClient{}
	client := &clientImpl{
		client:                     fakeClient,
		clusterPipelineParallelism: 1,
	}

	err := client.executeGroupedPipeline(context.Background(), Pipeline{
		{Key: "a", Action: &testAction{key: "a"}},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, fakeClient.callCount())
}

func TestExecuteGroupedPipelineSerialCompatibilityStopsOnFirstError(t *testing.T) {
	expectedErr := errors.New("redis failed")
	fakeClient := &recordingRedisClient{}
	client := &clientImpl{
		client:                     fakeClient,
		clusterPipelineParallelism: 1,
	}

	err := client.executeGroupedPipeline(context.Background(), Pipeline{
		{Key: "a", Action: &testAction{key: "a", err: expectedErr}},
		{Key: "b", Action: &testAction{key: "b"}},
	})

	require.ErrorIs(t, err, expectedErr)
	assert.Equal(t, 1, fakeClient.callCount())
	assert.Equal(t, 1, fakeClient.maxConcurrentCalls())
}

func TestExecuteGroupedPipelineGroupsSameKeyActions(t *testing.T) {
	fakeClient := &recordingRedisClient{}
	client := &clientImpl{
		client:                     fakeClient,
		clusterPipelineParallelism: 2,
	}

	err := client.executeGroupedPipeline(context.Background(), Pipeline{
		{Key: "a", Action: &testAction{key: "a"}},
		{Key: "a", Action: &testAction{key: "a"}},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, fakeClient.callCount())
}

func TestExecuteGroupedPipelineParallelismAllowsConcurrentGroups(t *testing.T) {
	fakeClient := &recordingRedisClient{}
	client := &clientImpl{
		client:                     fakeClient,
		clusterPipelineParallelism: 3,
	}

	err := client.executeGroupedPipeline(context.Background(), Pipeline{
		{Key: "a", Action: &testAction{key: "a", delay: 100 * time.Millisecond}},
		{Key: "b", Action: &testAction{key: "b", delay: 100 * time.Millisecond}},
		{Key: "c", Action: &testAction{key: "c", delay: 100 * time.Millisecond}},
	})

	require.NoError(t, err)
	assert.Equal(t, 3, fakeClient.callCount())
	assert.Equal(t, 3, fakeClient.maxConcurrentCalls())
}

func TestExecuteGroupedPipelineBoundedParallelism(t *testing.T) {
	fakeClient := &recordingRedisClient{}
	client := &clientImpl{
		client:                     fakeClient,
		clusterPipelineParallelism: 2,
	}

	err := client.executeGroupedPipeline(context.Background(), Pipeline{
		{Key: "a", Action: &testAction{key: "a", delay: 100 * time.Millisecond}},
		{Key: "b", Action: &testAction{key: "b", delay: 100 * time.Millisecond}},
		{Key: "c", Action: &testAction{key: "c", delay: 100 * time.Millisecond}},
	})

	require.NoError(t, err)
	assert.Equal(t, 3, fakeClient.callCount())
	assert.Equal(t, 2, fakeClient.maxConcurrentCalls())
}

func TestExecuteGroupedPipelineDiscardsConnOnReadOnly(t *testing.T) {
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
		isCluster:                      true,
		clusterPipelineParallelism:     1,
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

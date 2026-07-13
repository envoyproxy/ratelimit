package redis

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mediocregopher/radix/v4"
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
	// blockDelay, when > 0, makes Do block until ctx is done or blockDelay
	// elapses, regardless of the action type. Used to simulate a Redis
	// server that never responds (e.g. paused/unreachable).
	blockDelay time.Duration
	// lastCtx records the context passed to the most recent Do call, so
	// tests can assert whether a deadline was attached to it.
	lastCtx context.Context
}

func (c *recordingRedisClient) Do(ctx context.Context, action radix.Action) error {
	c.mu.Lock()
	c.calls = append(c.calls, action)
	c.lastCtx = ctx
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

	if c.blockDelay > 0 {
		timer := time.NewTimer(c.blockDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}

	if action, ok := action.(*testAction); ok {
		return action.Perform(ctx, nil)
	}
	return nil
}

func (c *recordingRedisClient) getLastCtx() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastCtx
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

// --- opTimeout tests ---
//
// These verify that clientImpl.opTimeout bounds how long a single Redis
// command/pipeline is allowed to park when the underlying Redis connection
// never responds (simulated via recordingRedisClient.blockDelay), rather
// than parking indefinitely on the caller's context (which may have no
// deadline at all, as is the case on the hot DoLimit path today).

func TestPipeDoReturnsWithinOpTimeoutWhenRedisHangs(t *testing.T) {
	fakeClient := &recordingRedisClient{blockDelay: time.Hour}
	client := &clientImpl{client: fakeClient, opTimeout: 30 * time.Millisecond}

	start := time.Now()
	err := client.PipeDo(context.Background(), Pipeline{
		{Key: "a", Action: &testAction{key: "a"}},
	})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 500*time.Millisecond, "PipeDo should return promptly once opTimeout elapses instead of blocking indefinitely")
}

func TestDoCmdReturnsWithinOpTimeoutWhenRedisHangs(t *testing.T) {
	fakeClient := &recordingRedisClient{blockDelay: time.Hour}
	client := &clientImpl{client: fakeClient, opTimeout: 30 * time.Millisecond}

	start := time.Now()
	err := client.DoCmd(nil, "GET", "foo")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 500*time.Millisecond, "DoCmd should return promptly once opTimeout elapses instead of blocking indefinitely")
}

func TestPipeDoAppliesOpTimeoutDeadlineToContext(t *testing.T) {
	fakeClient := &recordingRedisClient{}
	client := &clientImpl{client: fakeClient, opTimeout: 50 * time.Millisecond}

	err := client.PipeDo(context.Background(), Pipeline{
		{Key: "a", Action: &testAction{key: "a"}},
	})
	require.NoError(t, err)

	ctx := fakeClient.getLastCtx()
	require.NotNil(t, ctx)
	deadline, ok := ctx.Deadline()
	assert.True(t, ok, "expected ctx passed to the underlying client to carry a deadline when opTimeout > 0")
	assert.True(t, time.Until(deadline) <= 50*time.Millisecond)
}

func TestPipeDoWithoutOpTimeoutLeavesContextUnbounded(t *testing.T) {
	fakeClient := &recordingRedisClient{}
	client := &clientImpl{client: fakeClient, opTimeout: 0}

	err := client.PipeDo(context.Background(), Pipeline{
		{Key: "a", Action: &testAction{key: "a"}},
	})
	require.NoError(t, err)

	ctx := fakeClient.getLastCtx()
	require.NotNil(t, ctx)
	_, ok := ctx.Deadline()
	assert.False(t, ok, "expected ctx to have no deadline when opTimeout == 0 (preserves current behavior)")
}

func TestDoCmdAppliesOpTimeoutDeadlineToContext(t *testing.T) {
	fakeClient := &recordingRedisClient{}
	client := &clientImpl{client: fakeClient, opTimeout: 50 * time.Millisecond}

	err := client.DoCmd(nil, "GET", "foo")
	require.NoError(t, err)

	ctx := fakeClient.getLastCtx()
	require.NotNil(t, ctx)
	deadline, ok := ctx.Deadline()
	assert.True(t, ok, "expected ctx passed to the underlying client to carry a deadline when opTimeout > 0")
	assert.True(t, time.Until(deadline) <= 50*time.Millisecond)
}

func TestDoCmdWithoutOpTimeoutLeavesContextUnbounded(t *testing.T) {
	fakeClient := &recordingRedisClient{}
	client := &clientImpl{client: fakeClient, opTimeout: 0}

	err := client.DoCmd(nil, "GET", "foo")
	require.NoError(t, err)

	ctx := fakeClient.getLastCtx()
	require.NotNil(t, ctx)
	_, ok := ctx.Deadline()
	assert.False(t, ok, "expected ctx to have no deadline when opTimeout == 0 (preserves current behavior, matches context.Background() used today)")
}

// The opTimeout wrap happens before the isCluster branch in PipeDo, so it
// should also bound the cluster grouped-pipeline path (executeGroupedPipeline
// / doPipelineGroup), not just the single/sentinel pipeline. These two tests
// cover that path explicitly with clusterPipelineParallelism > 1 so multiple
// keys are grouped and dispatched concurrently via errgroup.

func TestPipeDoClusterReturnsWithinOpTimeoutWhenRedisHangs(t *testing.T) {
	fakeClient := &recordingRedisClient{blockDelay: time.Hour}
	client := &clientImpl{client: fakeClient, opTimeout: 30 * time.Millisecond, isCluster: true, clusterPipelineParallelism: 2}

	start := time.Now()
	err := client.PipeDo(context.Background(), Pipeline{
		{Key: "a", Action: &testAction{key: "a"}},
		{Key: "b", Action: &testAction{key: "b"}},
	})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 500*time.Millisecond, "cluster PipeDo should return promptly once opTimeout elapses instead of blocking indefinitely")
}

func TestPipeDoClusterAppliesOpTimeoutDeadlineToContext(t *testing.T) {
	fakeClient := &recordingRedisClient{}
	client := &clientImpl{client: fakeClient, opTimeout: 50 * time.Millisecond, isCluster: true, clusterPipelineParallelism: 2}

	err := client.PipeDo(context.Background(), Pipeline{
		{Key: "a", Action: &testAction{key: "a"}},
		{Key: "b", Action: &testAction{key: "b"}},
	})
	require.NoError(t, err)

	ctx := fakeClient.getLastCtx()
	require.NotNil(t, ctx)
	deadline, ok := ctx.Deadline()
	assert.True(t, ok, "expected ctx passed to the underlying client to carry a deadline in cluster mode when opTimeout > 0")
	assert.True(t, time.Until(deadline) <= 50*time.Millisecond)
}

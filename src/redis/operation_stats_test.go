package redis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	stats "github.com/lyft/gostats"
	"github.com/lyft/gostats/mock"
	"github.com/mediocregopher/radix/v4"
	"github.com/mediocregopher/radix/v4/resp"
	"github.com/mediocregopher/radix/v4/resp/resp3"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/settings"
	rlstats "github.com/envoyproxy/ratelimit/src/stats"
	"github.com/envoyproxy/ratelimit/src/stats/prom"
)

type callMetricsRedisClient struct {
	do func(context.Context, radix.Action) error
}

func (c callMetricsRedisClient) Do(ctx context.Context, action radix.Action) error {
	return c.do(ctx, action)
}

func (callMetricsRedisClient) Close() error { return nil }

type callMetricsTimerSink struct {
	stats.Sink
	beforeTimer func(string, float64)
}

func (s callMetricsTimerSink) FlushTimer(name string, value float64) {
	s.beforeTimer(name, value)
	s.Sink.FlushTimer(name, value)
}

func TestClientCallMetricsOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		outcome callOutcome
	}{
		{"success", nil, callSuccess},
		{"canceled", context.Canceled, callCanceled},
		{"wrapped canceled", fmt.Errorf("request: %w", context.Canceled), callCanceled},
		{"deadline", context.DeadlineExceeded, callDeadline},
		{"wrapped deadline", fmt.Errorf("request: %w", context.DeadlineExceeded), callDeadline},
		{"redis reply", resp3.SimpleError{S: "READONLY private-key"}, callRedisError},
		{"wrapped redis reply", resp.ErrConnUsable{Err: resp3.SimpleError{S: "WRONGTYPE private-key"}}, callRedisError},
		{"redis blob", resp3.BlobError{B: []byte("ERR private-key")}, callRedisError},
		{"redis reply pointer", &resp3.SimpleError{S: "ERR private-key"}, callRedisError},
		{"redis blob pointer", &resp3.BlobError{B: []byte("ERR private-key")}, callRedisError},
		{"network timeout", &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}, callNetworkError},
		{"wrapped network timeout", fmt.Errorf("connection: %w", &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}), callNetworkError},
		{"eof", io.EOF, callNetworkError},
		{"unexpected eof", io.ErrUnexpectedEOF, callNetworkError},
		{"closed network", net.ErrClosed, callNetworkError},
		{"other error", errors.New("unexpected private-key response"), callOtherError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := mock.NewSink()
			store := stats.NewStore(sink, false)
			operations := newOperationStats(store.Scope("ratelimit.redis_pool"))
			client := callMetricsRedisClient{do: func(context.Context, radix.Action) error { return tc.err }}

			err := operations.pipeline.do(context.Background(), client, radix.Cmd(nil, "GET", "private-key"), 3)

			// BlobError contains a byte slice and is not comparable by errors.Is.
			// The wrapper must preserve the returned error's type and value.
			require.Equal(t, tc.err, err)
			if tc.err == nil || reflect.TypeOf(tc.err).Comparable() {
				require.ErrorIs(t, err, tc.err)
			}
			assert.Equal(t, uint64(1), operations.pipeline.started.Value())
			assert.Equal(t, uint64(3), operations.pipeline.actionsAttempted.Value())
			assert.Zero(t, operations.pipeline.inFlight.Value())
			for outcome, counter := range operations.pipeline.completed {
				want := uint64(0)
				if callOutcome(outcome) == tc.outcome {
					want = 1
				}
				assert.Equal(t, want, counter.Value(), "outcome %d", outcome)
			}
			sink.AssertTimerCallCount(t, "ratelimit.redis_pool.client.pipeline.call_duration", 1)
		})
	}
}

func TestClientCallMetricsDecodeRedisReplies(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply interface{}
		want  callOutcome
	}{
		{"success", "value", callSuccess},
		{"simple error", resp3.SimpleError{S: "WRONGTYPE private-key"}, callRedisError},
		{"blob error", resp3.BlobError{B: []byte("ERR private-key")}, callRedisError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := radix.NewStubConn("", "", func(context.Context, []string) interface{} { return tc.reply })
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			operations := newOperationStats(stats.NewStore(stats.NewNullSink(), false))
			client := &clientImpl{client: conn, operations: operations}
			var result string

			err := client.DoCmd(&result, "GET", "private-key")

			if tc.want == callSuccess {
				require.NoError(t, err)
				assert.Equal(t, "value", result)
			} else {
				require.Error(t, err)
			}
			assert.Equal(t, uint64(1), operations.command.completed[tc.want].Value())
			assert.Equal(t, uint64(1), operations.command.started.Value())
			assert.Zero(t, operations.pipeline.started.Value())
		})
	}
}

func TestClientCallMetricsRemainInFlightUntilReturn(t *testing.T) {
	sink := mock.NewSink()
	operations := newOperationStats(stats.NewStore(sink, false))
	entered, release := make(chan struct{}), make(chan struct{})
	client := callMetricsRedisClient{do: func(ctx context.Context, _ radix.Action) error {
		close(entered)
		<-release // Deliberately outlive cancellation, as a slow driver may do.
		return ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- operations.pipeline.do(ctx, client, radix.Cmd(nil, "GET", "private-key"), 1)
	}()
	defer close(release)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("client call did not start")
	}
	cancel()

	assert.Equal(t, uint64(1), operations.pipeline.inFlight.Value())
	assert.Zero(t, operations.pipeline.completed[callCanceled].Value())
	sink.AssertTimerNotExists(t, "client.pipeline.call_duration")
	select {
	case <-done:
		t.Fatal("metrics wrapper returned before the driver")
	default:
	}

	// A lower bound verifies that the full slow call is measured, even though
	// its caller's context was canceled before the delay.
	time.Sleep(20 * time.Millisecond)
	release <- struct{}{}
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("client call did not finish")
	}
	assert.Zero(t, operations.pipeline.inFlight.Value())
	assert.Equal(t, uint64(1), operations.pipeline.completed[callCanceled].Value())
	duration, ok := sink.LoadTimer("client.pipeline.call_duration")
	require.True(t, ok)
	assert.GreaterOrEqual(t, duration, 20.0, "StatsD duration is milliseconds")
	sink.AssertTimerCallCount(t, "client.pipeline.call_duration", 1)
}

func TestClientCallMetricsUseReturnedOutcomeWhenContextCanceled(t *testing.T) {
	operations := newOperationStats(stats.NewStore(stats.NewNullSink(), false))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := callMetricsRedisClient{do: func(context.Context, radix.Action) error { return nil }}

	require.NoError(t, operations.pipeline.do(ctx, client, radix.Cmd(nil, "GET", "key"), 1))

	assert.Equal(t, uint64(1), operations.pipeline.completed[callSuccess].Value())
	assert.Zero(t, operations.pipeline.completed[callCanceled].Value())
}

func TestClientCallMetricsRemainInFlightWhileReporting(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	sink := callMetricsTimerSink{
		Sink: stats.NewNullSink(),
		beforeTimer: func(string, float64) {
			close(entered)
			<-release
		},
	}
	operations := newOperationStats(stats.NewStore(sink, false))
	client := callMetricsRedisClient{do: func(context.Context, radix.Action) error { return nil }}
	done := make(chan error, 1)
	go func() {
		done <- operations.command.do(context.Background(), client, radix.Cmd(nil, "GET", "key"), 1)
	}()
	defer close(release)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timer reporting did not start")
	}

	assert.Equal(t, uint64(1), operations.command.completed[callSuccess].Value())
	assert.Equal(t, uint64(1), operations.command.inFlight.Value())
	select {
	case <-done:
		t.Fatal("client call returned while timer reporting was blocked")
	default:
	}
	release <- struct{}{}
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("client call did not finish")
	}
	assert.Zero(t, operations.command.inFlight.Value())
}

func TestClientCallMetricsReleaseGaugeWhenReportingPanics(t *testing.T) {
	reportingError := errors.New("timer sink failed")
	sink := callMetricsTimerSink{
		Sink:        stats.NewNullSink(),
		beforeTimer: func(string, float64) { panic(reportingError) },
	}
	operations := newOperationStats(stats.NewStore(sink, false))
	client := callMetricsRedisClient{do: func(context.Context, radix.Action) error { return nil }}

	assert.PanicsWithValue(t, reportingError, func() {
		_ = operations.command.do(context.Background(), client, radix.Cmd(nil, "GET", "key"), 1)
	})

	assert.Equal(t, uint64(1), operations.command.started.Value())
	assert.Equal(t, uint64(1), operations.command.completed[callSuccess].Value())
	assert.Zero(t, operations.command.inFlight.Value())
}

func TestPipelineMetricsCountAttemptedGroupsAndActions(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cluster     bool
		parallelism int
		keys        []string
		wantCalls   uint64
	}{
		{"single pipeline", false, 1, []string{"a", "a", "b"}, 1},
		{"cluster serial groups", true, 1, []string{"a", "a", "b"}, 2},
		{"cluster parallel groups", true, 2, []string{"a", "a", "b"}, 2},
		{"cluster single action", true, 2, []string{"a"}, 1},
		{"single empty pipeline", false, 1, nil, 1},
		{"cluster empty pipeline", true, 1, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operations := newOperationStats(stats.NewStore(stats.NewNullSink(), false))
			client := &clientImpl{
				client: &recordingRedisClient{}, operations: operations,
				isCluster: tc.cluster, clusterPipelineParallelism: tc.parallelism,
			}
			var pipeline Pipeline
			for _, key := range tc.keys {
				pipeline = client.PipeAppend(pipeline, nil, "GET", key)
			}

			require.NoError(t, client.PipeDo(context.Background(), pipeline))

			assert.Equal(t, tc.wantCalls, operations.pipeline.started.Value())
			assert.Equal(t, tc.wantCalls, operations.pipeline.completed[callSuccess].Value())
			assert.Equal(t, uint64(len(tc.keys)), operations.pipeline.actionsAttempted.Value())
			assert.Zero(t, operations.pipeline.inFlight.Value())
			assert.Zero(t, operations.command.started.Value())
		})
	}
}

func TestPipelineMetricsExcludeUnattemptedGroups(t *testing.T) {
	operations := newOperationStats(stats.NewStore(stats.NewNullSink(), false))
	client := &clientImpl{
		client: &recordingRedisClient{}, operations: operations,
		isCluster: true, clusterPipelineParallelism: 1,
	}
	err := client.PipeDo(context.Background(), Pipeline{
		{Key: "a", Action: &testAction{key: "a", err: io.EOF}},
		{Key: "b", Action: &testAction{key: "b"}},
	})

	require.ErrorIs(t, err, io.EOF)
	assert.Equal(t, uint64(1), operations.pipeline.started.Value())
	assert.Equal(t, uint64(1), operations.pipeline.actionsAttempted.Value())
	assert.Equal(t, uint64(1), operations.pipeline.completed[callNetworkError].Value())
	assert.Zero(t, operations.pipeline.completed[callSuccess].Value())
}

func TestRedisOperationMetricsUseServerScope(t *testing.T) {
	const childEnv = "RATELIMIT_REDIS_SCOPE_TEST_CHILD"
	if os.Getenv(childEnv) != "1" {
		// NewServer creates a config provider whose goroutines cannot all be
		// stopped by its current API. Keep this constructor check in an owned
		// child process; it never starts the rate-limit server or sends traffic.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRedisOperationMetricsUseServerScope$", "-test.count=1")
		cmd.Env = append(os.Environ(), childEnv+"=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}

	registry := prometheus.NewRegistry()
	prometheus.DefaultRegisterer = registry
	prometheus.DefaultGatherer = registry
	// The production sink's listener is confined to loopback in this child.
	// Gather directly from the registry; no HTTP requests are needed.
	promSink := prom.NewPrometheusSink(prom.WithAddr("127.0.0.1:0"))
	var durationName string
	var durationMilliseconds float64
	sink := callMetricsTimerSink{
		Sink: promSink,
		beforeTimer: func(name string, value float64) {
			durationName, durationMilliseconds = name, value
		},
	}
	store := stats.NewStore(sink, false)
	s := settings.Settings{ConfigType: "FILE", RuntimePath: t.TempDir()}
	srv := server.NewServer(s, "ratelimit", rlstats.NewStatManager(store, s), nil)
	defer srv.Stop()
	client := &clientImpl{
		client:     callMetricsRedisClient{do: func(context.Context, radix.Action) error { return nil }},
		operations: newOperationStats(srv.Scope().Scope("redis_pool")),
	}
	pipeline := client.PipeAppend(nil, nil, "GET", "first-private-key")
	pipeline = client.PipeAppend(pipeline, nil, "GET", "second-private-key")
	require.NoError(t, client.PipeDo(context.Background(), pipeline))
	require.Equal(t, "ratelimit.redis_pool.client.pipeline.call_duration", durationName)
	// The old, incorrect service prefix must not contribute to the mapped
	// Redis call counter. Redis pools receive the server's root scope.
	sink.FlushCounter("ratelimit.service.redis_pool.client.pipeline.calls_started", 100)
	store.Flush()

	var families map[string]*dto.MetricFamily
	require.Eventually(t, func() bool {
		collected, err := registry.Gather()
		if err != nil {
			return false
		}
		families = make(map[string]*dto.MetricFamily)
		for _, family := range collected {
			families[family.GetName()] = family
		}
		for _, name := range []string{"calls_started_total", "calls_completed_total", "calls_in_flight", "call_duration_seconds", "actions_attempted_total"} {
			if families["ratelimit_redis_client_"+name] == nil {
				return false
			}
		}
		return true
	}, time.Second, time.Millisecond)

	for _, expected := range []struct {
		name       string
		metricType dto.MetricType
		value      float64
	}{
		{"calls_started_total", dto.MetricType_COUNTER, 1},
		{"calls_completed_total", dto.MetricType_COUNTER, 1},
		{"calls_in_flight", dto.MetricType_GAUGE, 0},
		{"call_duration_seconds", dto.MetricType_HISTOGRAM, durationMilliseconds / 1000},
		{"actions_attempted_total", dto.MetricType_COUNTER, 2},
	} {
		family := families["ratelimit_redis_client_"+expected.name]
		require.Equal(t, expected.metricType, family.GetType(), expected.name)
		found := false
		for _, metric := range family.GetMetric() {
			labels := make(map[string]string)
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["operation"] != "pipeline" {
				continue
			}
			found = true
			wantLabels := map[string]string{"pool": "redis_pool", "operation": "pipeline"}
			if expected.name == "calls_completed_total" {
				wantLabels["outcome"] = "success"
			}
			assert.Equal(t, wantLabels, labels)
			switch expected.metricType {
			case dto.MetricType_COUNTER:
				assert.Equal(t, expected.value, metric.GetCounter().GetValue())
			case dto.MetricType_GAUGE:
				assert.Equal(t, expected.value, metric.GetGauge().GetValue())
			case dto.MetricType_HISTOGRAM:
				assert.Equal(t, uint64(1), metric.GetHistogram().GetSampleCount())
				assert.InDelta(t, expected.value, metric.GetHistogram().GetSampleSum(), 1e-12)
			}
		}
		assert.True(t, found, expected.name)
	}
}
